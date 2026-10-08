package impl

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/model"
	"errors"
	"fmt"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"time"
)

// ============================== HashPower 算力矿机（N次方新增） ==============================
// 爆仓补偿：倒2/倒3 轮失败时扣 50% 币，按爆仓价折算算力入矿机，挖到三倍（金本位）出局。
// 三种模式：
//   0 默认三倍出局：按算力占比分配矿池日产出（每日区块总产出 × 0.3），无保底，产出周期长
//   1 最长300天三倍出局：保底每日产出 = (算力×3) / 300 / 4h均价
//   2 TM加速（暂定 3.4U/枚）：300 天三倍出局（保底同模式1）
// 金本位：出局条件 = 累计产出价值(累计币量×实时价) ≥ 算力×3

type HashPowerManager struct {
	DB              *gorm.DB
	WalletPointSafe core.WalletPointing
}

func NewHashPowerManager() core.HashPowering {
	return &HashPowerManager{
		DB:              config.MysqlDBPool,
		WalletPointSafe: NewWalletPointSafe(),
	}
}

// BookPower 爆仓折算入账：amount 为被扣币数量（= 投入额 × 50%），price 为爆仓时币价(U)
// 算力 power = amount × price（U 计价）；三倍目标（金本位）targetReward = power × 3 / price（币量快照）
func (h *HashPowerManager) BookPower(tx *gorm.DB, address string, symbol string, amount float64, price float64) error {
	if amount <= 0 || price <= 0 {
		return errors.New("爆仓算力折算参数错误")
	}

	power := amount * price
	targetReward := power * HashPowerBuff / price //金本位三倍（币量）

	hp := new(model.HashPower)
	hp.Address = address
	hp.Symbol = symbol
	hp.Power = power
	hp.Price = price
	hp.Mode = HashPowerModeDefault //默认三倍出局（模式1/2 由产品运营配置）
	hp.TotalReward = 0
	hp.RemainReward = 0
	hp.TargetReward = targetReward
	hp.Status = 0
	hp.StartTime = time.Now()
	if err := tx.Table(model.HashPowerTable).Create(hp).Error; err != nil {
		log.WithFields(log.Fields{"Method": "BookPower", "address": address}).Error("创建算力矿机账户失败", err)
		return err
	}

	//记录钱包流水（算力入账，币未返还）
	wtx := new(model.WalletTx)
	wtx.Symbol = symbol
	wtx.From = RewardAddress
	wtx.To = address
	wtx.Amount = uint64(amount * config.SymbolDictionary[symbol])
	wtx.Type = enum.HashPowerIn
	wtx.Desc = enum.HashPowerInText
	if err := tx.Table(model.WalletTxTable).Create(wtx).Error; err != nil {
		return err
	}
	return nil
}

// DailyReward 每日产币（由 cmd/hashpower 定时任务调用）
// rewardDailyFibo：FIBO 每日区块总产出（配置化，如 3498542274052 × 0.425）
// 分配：矿池日产出 = rewardDailyFibo × HashPowerPoolDailyRatio(0.3)
//       个人日产出 = 矿池日产出 × (个人算力 / 总算力)
// 保底（模式1/2）：日产出 ≥ (算力×3)/300/4h均价
// 出局（金本位）：累计产出币量 × 实时币价 ≥ 算力 × 3
func (h *HashPowerManager) DailyReward(rewardDailyFibo float64) error {
	var powers []model.HashPower
	if err := h.DB.Table(model.HashPowerTable).Find(&powers, "`status` = ?", 0).Error; err != nil {
		log.WithFields(log.Fields{"Method": "DailyReward"}).Errorln("查询挖矿中的算力账户失败", err)
		return err
	}
	if len(powers) == 0 {
		log.Infoln("DailyReward: 无挖矿中的算力账户")
		return nil
	}

	//总算力（U）
	var powerSum float64
	for _, p := range powers {
		powerSum += p.Power
	}
	if powerSum <= 0 {
		return errors.New("总算力为0")
	}

	//矿池日产出
	poolDaily := rewardDailyFibo * HashPowerPoolDailyRatio

	//FIBO 实时价（同一轮产币价格一致，只取一次；保底与出局判定共用）
	fiboPrice := GetKtoSymbolPrice(enum.FIBO)

	for _, p := range powers {
		//1.按算力占比分配
		reward := poolDaily * (p.Power / powerSum)

		//2.保底（模式1/2：最长300天三倍出局）
		if p.Mode == HashPowerMode300 || p.Mode == HashPowerModeTM {
			//4h 均价：当前以 FIBO 实时价近似（产出币为 FIBO，价格必须与产出币一致，
			//不能取 p.Symbol（爆仓币），否则 USDT 爆仓账户的保底会错算约百倍）
			if fiboPrice > 0 {
				floor := (p.Power * HashPowerBuff) / HashPowerMaxDays / fiboPrice
				if floor > reward {
					reward = floor
				}
			} else {
				//FIBO 无报价时跳过保底（防御除零/Inf 写库），仅按占比分配
				log.WithFields(log.Fields{"Method": "DailyReward", "id": p.ID}).Warnln("FIBO 价格获取失败，模式1/2 本轮跳过保底")
			}
		}

		if reward <= 0 {
			continue
		}

		//3.累计产出（币本位）
		totalReward := p.TotalReward + reward

		//4.金本位出局判定：累计产出价值 ≥ 算力×3
		var stopMining bool
		if fiboPrice > 0 && totalReward*fiboPrice >= p.Power*HashPowerBuff {
			stopMining = true
		}

		//5.事务：更新账户 + 记录流水
		err := h.DB.Transaction(func(tx *gorm.DB) error {
			hp := new(model.HashPower)
			if err := tx.Table(model.HashPowerTable).First(hp, "`id` = ?", p.ID).Error; err != nil {
				return err
			}
			hp.TotalReward = totalReward
			hp.RemainReward = hp.RemainReward + reward
			if stopMining {
				hp.Status = 1
				now := time.Now()
				hp.FinishTime = &now
			}
			if err := tx.Table(model.HashPowerTable).Select("total_reward", "remain_reward", "status", "finish_time").Updates(hp).Error; err != nil {
				return err
			}

			//记录产币流水（类型沿用 MiningReward，语义为挖矿收益）
			wtx := new(model.WalletTx)
			wtx.Symbol = enum.FIBO
			wtx.From = config.EtcConfig.KtoPool.Address
			wtx.To = hp.Address
			wtx.Amount = uint64(reward * config.SymbolDictionary[enum.FIBO])
			wtx.Type = enum.MiningReward
			wtx.Desc = enum.MiningRewardText
			return tx.Table(model.WalletTxTable).Create(wtx).Error
		})
		if err != nil {
			log.WithFields(log.Fields{"Method": "DailyReward", "id": p.ID}).Errorln("记录算力产币失败", err)
			continue
		}
		log.WithFields(log.Fields{"Method": "DailyReward", "id": p.ID, "reward": reward, "stop": stopMining}).Infoln("算力矿机产币成功")
	}
	return nil
}

// Withdraw 提取矿机产出（FIBO），最低起提 MiningWithdrawMinCount 个
// 支持多账户合并提取：汇总该地址全部算力账户（含已出局）剩余可提
func (h *HashPowerManager) Withdraw(wallet model.Wallet, amount float64) error {
	if amount < MiningWithdrawMinCount {
		return errors.New(fmt.Sprintf("低于最低起提数量%d个", MiningWithdrawMinCount))
	}

	var powers []model.HashPower
	if err := h.DB.Table(model.HashPowerTable).Find(&powers, "`address` = ?", wallet.Address).Error; err != nil {
		return errors.New("查询算力账户失败")
	}
	if len(powers) == 0 {
		return errors.New("无算力矿机账户")
	}

	//可提取总额
	var remainTotal float64
	for _, p := range powers {
		remainTotal += p.RemainReward
	}
	if amount > remainTotal {
		return errors.New("可提取余额不足")
	}

	//按账户顺序扣减（先扣先得）。并发安全：扣减用原子 UPDATE + remain_reward >= cut 条件，
	//防止两个并发提取请求基于同一旧快照重复扣减（超发）。
	//注意：MySQL RR 隔离下事务内快照读不会看到并发修改，故 RowsAffected=0（被并发扣走）时不重读重试，
	//直接返回冲突错误让上层重试，避免死循环。
	toWithdraw := amount
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		for i := range powers {
			if toWithdraw <= 0 {
				break
			}
			hp := new(model.HashPower)
			if err := tx.Table(model.HashPowerTable).First(hp, "`id` = ?", powers[i].ID).Error; err != nil {
				return err
			}
			if hp.RemainReward <= 0 {
				continue
			}
			cut := toWithdraw
			if hp.RemainReward < cut {
				cut = hp.RemainReward
			}
			res := tx.Table(model.HashPowerTable).
				Where("`id` = ? and `remain_reward` >= ?", hp.ID, cut).
				Update("remain_reward", hp.RemainReward-cut)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return errors.New("提取冲突，请稍后重试")
			}
			toWithdraw -= cut
		}
		if toWithdraw > 0 {
			return errors.New("可提取余额不足")
		}

		//入账 FIBO 余额
		amountWithDecimal, err := h.WalletPointSafe.AddPointAmount(tx, wallet.Address, enum.FIBO, amount)
		if err != nil {
			return err
		}

		//记录提取流水
		wtx := new(model.WalletTx)
		wtx.Symbol = enum.FIBO
		wtx.From = config.EtcConfig.KtoPool.Address
		wtx.To = wallet.Address
		wtx.Amount = amountWithDecimal
		wtx.Type = enum.MiningReward
		wtx.Desc = enum.MiningRewardText + "(算力矿机提取)"
		return tx.Table(model.WalletTxTable).Create(wtx).Error
	})
	return err
}

// Home 我的算力矿机首页数据：账户列表 + 汇总
func (h *HashPowerManager) Home(address string) (core.HashPowerHome, error) {
	var home core.HashPowerHome
	var powers []model.HashPower
	if err := h.DB.Table(model.HashPowerTable).Order("`created_at` DESC").Find(&powers, "`address` = ?", address).Error; err != nil {
		return home, errors.New("查询算力账户失败")
	}
	home.AccountList = powers
	for _, p := range powers {
		home.TotalPower += p.Power
		home.TotalReward += p.TotalReward
		home.RemainReward += p.RemainReward
	}
	return home, nil
}
