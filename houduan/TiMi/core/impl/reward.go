package impl

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/model"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"strconv"
	"sync"
)

type RewardManager struct {
	DB               *gorm.DB
	WalletManager    core.OnBoarding
	WalletModel      *model.WalletModel
	Lock             sync.Mutex
	WalletPointSafe  core.WalletPointing
	HashPowerManager core.HashPowering //N次方新增：爆仓折算算力矿机
}

func NewRewardManager() core.Rewarding {
	return &RewardManager{
		DB:               config.MysqlDBPool,
		WalletModel:      model.NewWalletModel(),
		WalletManager:    NewWalletManager(),
		WalletPointSafe:  NewWalletPointSafe(),
		HashPowerManager: NewHashPowerManager(),
	}
}

// claimSettleRound 结算幂等抢占（复盘 Wave-2）：
// 在结算事务内【最先】把轮从 RoundCompute/RoundStarting 抢占为 RoundEnding。
// 返回 claimed=true 表示本次获得结算权；false 表示该轮已被结算/正在结算，调用方应直接跳过，
// 从而挡住 round 定时器与 ops EndRound 对同一轮的重复结算（防止重复发放收益/退款/算力）。
// 事务失败回滚后轮恢复原状态（Compute/Starting），可被下次结算重试；抢占本身不产生资金变动。
func claimSettleRound(tx *gorm.DB, roundId uint) (claimed bool, err error) {
	res := tx.Exec("UPDATE `"+model.ProjectRoundTable+"` SET `status` = ? WHERE `id` = ? AND `status` IN (?, ?)",
		enum.RoundEnding, roundId, enum.RoundCompute, enum.RoundStarting)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *RewardManager) Success(round model.ProjectRound) {
	r.Lock.Lock()
	defer r.Lock.Unlock()
	log.WithFields(log.Fields{"roundId": round.ID, "Method": "Success"}).Infoln("开始计算成功奖励")

	roundId := round.ID
	var err error
	var voteList = r.GetNoAdminVotes(round)
	voteAddressMap := make(map[string]model.Vote)
	voteWalletIdMap := make(map[uint]model.Vote)

	for _, vote := range voteList {
		var ok bool
		walletId := vote.WalletId
		address := vote.Address

		if roundId == vote.RoundId {
			//当前轮
			if _, ok = voteAddressMap[address]; !ok {
				voteAddressMap[address] = vote
			}
			if _, ok = voteWalletIdMap[walletId]; !ok {
				voteWalletIdMap[walletId] = vote
			}
		}
	}

	//2.计算静态收益: 获取当前轮的：币 * 0.13
	staticRewardMap := make(map[string]float64)
	for address, vote := range voteAddressMap {
		reward := vote.Amount * StaticRewardRate
		staticRewardMap[address] = reward
	}

	//N次方：团队收益（伞下该期总投资额比例制）—— 每轮成功只结算一次，
	//在事务外预计算，避免对每个参与者重复发放（原极差收益逻辑已废弃）
	teamRewardMap, err := r.calcTeamReward(round, voteList)
	if err != nil {
		//团队收益聚合失败则整轮不结算（轮保持 RoundCompute），
		//下个成功轮 End 时会对该轮重试结算，事务原子保证无部分发放（资金一致性）
		log.WithFields(log.Fields{"roundId": round.ID, "Method": "Success"}).Errorln("团队收益聚合失败，本轮暂不结算", err)
		return
	}
	var teamRefVoteId uint
	if len(voteList) > 0 {
		teamRefVoteId = voteList[0].ID
	}

	//动态收益记录
	err = r.DB.Transaction(func(tx *gorm.DB) error {
		//幂等抢占：同轮已被结算/结算中则跳过（防 round 定时与 ops EndRound 重复发放）
		claimed, claimErr := claimSettleRound(tx, round.ID)
		if claimErr != nil {
			return claimErr
		}
		if !claimed {
			log.WithFields(log.Fields{"roundId": round.ID, "Method": "Success"}).Infoln("该轮已在结算/已结束，跳过重复结算")
			return nil
		}

		//3.计算动态收益: 分享收益
		//4.计算动态收益: 团队管理
		for walletId, vote := range voteWalletIdMap {

			rewardShardMap := r.dynamicSharedRewardUpTreePath(walletId, vote)
			for address, reward := range rewardShardMap {
				if reward > 0 {
					voteReward := new(model.VoteReward)
					voteReward.Address = address
					voteReward.VoteId = vote.ID
					voteReward.RoundId = vote.RoundId
					voteReward.ProjectId = vote.ProjectId
					voteReward.DynamicShard = reward
					//收益记录
					if err = tx.Table(model.VoteRewardTable).Create(voteReward).Error; err != nil {
						return err
					}

					//新增到钱包余额
					rewardAmount, err := r.WalletPointSafe.AddPointAmount(tx, address, vote.Symbol, reward)
					if err != nil {
						return err
					}

					//4.记录wallet_tx
					wtx := new(model.WalletTx)
					wtx.Symbol = vote.Symbol
					wtx.Amount = rewardAmount
					wtx.From = RewardAddress
					wtx.To = address
					wtx.Type = enum.DynamicShard
					wtx.Desc = "[" + "第" + strconv.Itoa(int(round.Period)) + "期" + "第" + strconv.Itoa(int(round.Round)) + "轮" + "]" + enum.DynamicShardText
					if err = tx.Table(model.WalletTxTable).Create(wtx).Error; err != nil {
						return err
					}
				}
			}
		}

		//N次方：团队收益统一发放（伞下该期总投资额 × 等级比例，每轮只发一次）
		for address, reward := range teamRewardMap {
			if reward <= 0 {
				continue
			}
			voteReward := new(model.VoteReward)
			voteReward.Address = address
			voteReward.VoteId = teamRefVoteId
			voteReward.RoundId = round.ID
			voteReward.ProjectId = round.ProjectId
			voteReward.DynamicTeam = reward
			//收益记录
			if err = tx.Table(model.VoteRewardTable).Create(voteReward).Error; err != nil {
				return err
			}

			//新增到钱包余额
			rewardAmount, err := r.WalletPointSafe.AddPointAmount(tx, address, round.Symbol, reward)
			if err != nil {
				return err
			}

			//4.记录wallet_tx
			wtx := new(model.WalletTx)
			wtx.Symbol = round.Symbol
			wtx.Amount = rewardAmount
			wtx.From = RewardAddress
			wtx.To = address
			wtx.Type = enum.DynamicTeam
			wtx.Desc = "[" + "第" + strconv.Itoa(int(round.Period)) + "期" + "第" + strconv.Itoa(int(round.Round)) + "轮" + "]" + enum.DynamicTeamText
			if err = tx.Table(model.WalletTxTable).Create(wtx).Error; err != nil {
				return err
			}
		}

		//5.发放静态收益和退还本金
		for address, vote := range voteAddressMap {
			staticReward := staticRewardMap[address]

			voteReward := new(model.VoteReward)
			voteReward.Address = address
			voteReward.VoteId = vote.ID
			voteReward.RoundId = vote.RoundId
			voteReward.ProjectId = vote.ProjectId
			voteReward.Static = staticReward
			//1.收益记录
			if err = tx.Table(model.VoteRewardTable).Create(voteReward).Error; err != nil {
				return err
			}

			//2.新增收益钱包余额
			//新增到钱包余额
			staticRewardAmount, err := r.WalletPointSafe.AddPointAmount(tx, address, vote.Symbol, staticReward)
			if err != nil {
				return err
			}

			withdrawAmount, err := r.WalletPointSafe.AddPointAmount(tx, address, vote.Symbol, vote.Amount)
			if err != nil {
				return err
			}

			//4.记录wallet_tx
			if staticRewardAmount > 0 {
				wtx := new(model.WalletTx)
				wtx.Symbol = vote.Symbol
				wtx.Amount = staticRewardAmount
				wtx.From = RewardAddress
				wtx.To = address
				wtx.Type = enum.StaticReward
				wtx.Desc = "[" + "第" + strconv.Itoa(int(round.Period)) + "期" + "第" + strconv.Itoa(int(round.Round)) + "轮" + "]" + enum.StaticRewardText
				if err = tx.Table(model.WalletTxTable).Create(wtx).Error; err != nil {
					return err
				}
			}

			//5.百分百投入退还
			if withdrawAmount > 0 {
				withWtx := new(model.WalletTx)
				withWtx.Symbol = vote.Symbol
				withWtx.Amount = withdrawAmount
				withWtx.From = RewardAddress
				withWtx.To = address
				withWtx.Type = enum.SuccessWithdraw
				withWtx.Desc = "[" + "第" + strconv.Itoa(int(round.Period)) + "期" + "第" + strconv.Itoa(int(round.Round)) + "轮" + "]" + enum.SuccessWithdrawText
				if err = tx.Table(model.WalletTxTable).Create(withWtx).Error; err != nil {
					return err
				}
			}
		}

		//更新轮结束
		if err = tx.Model(model.ProjectRound{}).Where("`id` = ?", round.ID).Update("status", enum.RoundEnding).Error; err != nil {
			return err
		}

		if err != nil {
			log.WithFields(log.Fields{"Action": "计算收益", "Method": "Success"}).Error("成功轮返还本金收益结算", err)
			return err
		}
		return err
	})

}

//FailedNormal 百分百返回
func (r *RewardManager) FailedNormal(round model.ProjectRound) {
	r.Lock.Lock()
	defer r.Lock.Unlock()

	//百分百退还
	log.WithFields(log.Fields{"roundId": round.ID, "Method": "FailedNormal"}).Infoln("开始计算百分百退还本金")

	//1.获取参与项目的所有钱包地址
	var err error
	var voteList = r.GetNoAdminVotes(round)
	voteAddressMap := make(map[string]model.Vote)
	for _, vote := range voteList {
		if _, ok := voteAddressMap[vote.Address]; !ok {
			voteAddressMap[vote.Address] = vote
		}
	}

	err = r.DB.Transaction(func(tx *gorm.DB) error {
		var err error

		//幂等抢占：同轮已被结算/结算中则跳过（防 round 定时与 ops EndRound 重复发放）
		claimed, claimErr := claimSettleRound(tx, round.ID)
		if claimErr != nil {
			return claimErr
		}
		if !claimed {
			log.WithFields(log.Fields{"roundId": round.ID, "Method": "FailedNormal"}).Infoln("该轮已在结算/已结束，跳过重复结算")
			return nil
		}

		for address, vote := range voteAddressMap {
			amount := vote.Amount
			symbol := vote.Symbol

			//新增到钱包余额
			withdrawAmount, err := r.WalletPointSafe.AddPointAmount(tx, address, symbol, amount)
			if err != nil {
				return err
			}

			//创建退还记录
			wtx := new(model.WalletTx)
			wtx.Symbol = vote.Symbol
			wtx.Amount = withdrawAmount
			wtx.From = RewardAddress
			wtx.To = address
			wtx.Type = enum.Failed100Withdraw
			wtx.Desc = "[" + "第" + strconv.Itoa(int(round.Period)) + "期" + "第" + strconv.Itoa(int(round.Round)) + "轮" + "]" + enum.Failed100WithdrawText
			if err = tx.Table(model.WalletTxTable).Create(wtx).Error; err != nil {
				return err
			}
		}

		//更新轮结束
		if err = tx.Model(model.ProjectRound{}).Where("`id` = ?", round.ID).Update("status", enum.RoundEnding).Error; err != nil {
			return err
		}

		//同时更新项目结束
		if err = tx.Model(model.Project{}).Where("`id` = ?", round.ProjectId).Update("status", enum.ProjectEnding).Error; err != nil {
			return err
		}

		if err != nil {
			log.WithFields(log.Fields{"Action": "计算收益", "Method": "FailedNormal"}).Error("收益到钱包失败", err)
			return err
		}

		return err
	})
	if err != nil {
		log.WithFields(log.Fields{"roundId": round.ID, "Method": "FailedNormal"}).Errorln("失败计算", err)
	}

}

// splitFailureAmount 把一笔爆仓仓位拆成「被扣（折算算力）」与「退还用户」两部分。
//
// 需求口径（千万次#5/#7、N次方 42-48）：倒2/倒3 **扣除 50% 的币**，
// 需求#7 的算例把它写得更明确：「参与10万 则退回5万…此时获得算力 50000×0.1=5000」——
// 即一半退还、另一半折算算力。
//
// 为什么抽成纯函数：这是资金级口径，必须有单测钉死。旧实现（扣除额直接当返回值、
// 另一半从不入账）把倒2/倒3 用户的本金 100% 吃掉，且没有任何测试能发现。
func splitFailureAmount(amount, lossRate float64) (deducted, refund float64) {
	if amount <= 0 {
		return 0, 0
	}
	if lossRate <= 0 {
		return 0, amount //不扣：全额退还
	}
	if lossRate >= 1 {
		return amount, 0 //全扣（保留语义，实际业务不会这么配）
	}
	deducted = amount * lossRate
	refund = amount - deducted
	return
}

//Failed 倒2/倒3 爆仓结算：**退还另一半** + 被扣部分按爆仓价折算算力
//
// 规则依据（千万次需求#5/#7、N次方 42-48 行）：
//   - 倒2/倒3 扣除 50% 的币，即：被扣的 50% 折算算力，**另外 50% 退还给用户**
//     （需求#7 原文："倒数一轮参与10万 则退回5万…此时获得算力 50000x0.1=5000"）
//   - 被扣部分按爆仓时币价折算为固定算力：算力 = 数量 × 币价(U)
//
// ⚠ 2026-09-30 修复的两个资金级缺陷（修复前行为）：
//  1. 原实现只写 vote_reward.loss + 折算算力，**从不退还另外 50%** → 倒2/倒3 用户本金
//     100% 被吃掉（enum.Failed50Withdraw 全仓零引用）。现在未被扣除部分必定入账并写流水。
//  2. 原实现在爆仓价取不到时 `return errors.New(...)` → 整笔事务回滚，而 Failed 只在
//     "下一次失败轮"被触发（此时项目已结束）→ 该轮永久停在 RoundCompute：用户既没有币
//     也没有算力。现在爆仓价不可用时**不扣除**（本笔按全额退还处理）并打 ERROR 日志：
//     宁可少扣，也不能把用户资产扣成悬空；失败仍回滚，由 cmd/round 的补偿巡检重试。
func (r *RewardManager) Failed(rounds []model.ProjectRound) {
	r.Lock.Lock()
	defer r.Lock.Unlock()

	for _, round := range rounds {
		log.WithFields(log.Fields{"roundId": round.ID, "Method": "Failed"}).Infoln("开始爆仓结算（退还未扣部分 + 被扣部分折算算力）")

		//1.获取参与项目的所有钱包地址（vote 表按 (address,round) 单行累加，取一行即该地址本轮全额）
		var err error
		var voteList = r.GetNoAdminVotes(round)

		voteAddressMap := make(map[string]model.Vote)
		for _, vote := range voteList {
			if _, ok := voteAddressMap[vote.Address]; !ok {
				voteAddressMap[vote.Address] = vote
			}
		}

		//爆仓价在事务外解析：GetKtoSymbolPrice 可能触发外部行情请求，不应占用数据库事务
		priceMap := make(map[string]float64)
		for _, vote := range voteAddressMap {
			if _, ok := priceMap[vote.Symbol]; ok {
				continue
			}
			priceMap[vote.Symbol] = GetKtoSymbolPrice(vote.Symbol) //爆仓时币价(U)
		}

		err = r.DB.Transaction(func(tx *gorm.DB) error {
			//幂等抢占：同轮已被结算/结算中则跳过（防 round 定时与 ops EndRound 重复退还/重复折算算力）
			claimed, claimErr := claimSettleRound(tx, round.ID)
			if claimErr != nil {
				return claimErr
			}
			if !claimed {
				log.WithFields(log.Fields{"roundId": round.ID, "Method": "Failed"}).Infoln("该轮已在结算/已结束，跳过重复结算")
				return nil
			}

			for address, vote := range voteAddressMap {
				amount := vote.Amount
				symbol := vote.Symbol
				deducted, refund := splitFailureAmount(amount, LossRate) //被扣并折算算力的部分 / 退还用户的部分

				price := priceMap[symbol]
				if deducted > 0 && price <= 0 {
					log.WithFields(log.Fields{"Method": "Failed", "roundId": round.ID, "address": address, "symbol": symbol}).
						Errorln("爆仓价不可用：本笔不做扣除，按全额退还处理（请核查行情源后人工复核本轮）")
					deducted = 0
					refund = amount
				}

				//2.记录收益变化（Loss = 实际被扣数量）
				if deducted > 0 {
					voteReward := new(model.VoteReward)
					voteReward.Address = address
					voteReward.VoteId = vote.ID
					voteReward.RoundId = vote.RoundId
					voteReward.ProjectId = vote.ProjectId
					voteReward.Loss = deducted
					if err = tx.Table(model.VoteRewardTable).Create(voteReward).Error; err != nil {
						log.WithFields(log.Fields{"Method": "Failed", "address": address}).Error("创建收益失败", err)
						return err
					}
				}

				//3.退还未被扣除的部分（需求#5/#7：倒2/倒3 只扣一半）
				if refund > 0 {
					refundAmount, rErr := r.WalletPointSafe.AddPointAmount(tx, address, symbol, refund)
					if rErr != nil {
						log.WithFields(log.Fields{"Method": "Failed", "address": address}).Errorln("爆仓退还部分本金失败", rErr)
						return rErr
					}
					if refundAmount > 0 {
						wtx := new(model.WalletTx)
						wtx.Symbol = symbol
						wtx.Amount = refundAmount
						wtx.From = RewardAddress
						wtx.To = address
						wtx.Type = enum.Failed50Withdraw
						wtx.Desc = "[" + "第" + strconv.Itoa(int(round.Period)) + "期" + "第" + strconv.Itoa(int(round.Round)) + "轮" + "]" + enum.Failed50WithdrawText
						if err = tx.Table(model.WalletTxTable).Create(wtx).Error; err != nil {
							return err
						}
					}
				}

				//4.被扣部分按爆仓价折算算力入 hash_power 矿机（挖到三倍出局）
				if deducted > 0 {
					if err = r.HashPowerManager.BookPower(tx, address, symbol, deducted, price); err != nil {
						log.WithFields(log.Fields{"Method": "Failed", "address": address}).Errorln("爆仓折算算力入账失败", err)
						return err
					}
				}
			}

			//更新轮结束
			if err = tx.Model(model.ProjectRound{}).Where("`id` = ?", round.ID).Update("status", enum.RoundEnding).Error; err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			log.WithFields(log.Fields{"Action": "计算收益", "Method": "Failed", "roundId": round.ID}).
				Errorln("爆仓结算失败（轮保持待结算，由 cmd/round 补偿巡检重试）", err)
		}
	}
}

//calcTeamReward N次方团队收益：伞下该期总投资额比例制（F1 0.5% / F2 1% / F3 1.5%）
//规则：达到 F1/F2/F3 等级的用户 X，其收益 = Σ(X 伞下全部用户该期(project_id)累计投资额) × 等级比例。
//口径（2026-09-02 与业务确认）：按期累计、每成功轮结算一次。同一笔早期轮投资会随后续成功轮重复计入，
//      属业务确认的宽松激励模型，勿按"仅被结算轮"口径误改。
//说明：该期投资额按 vote 表 project_id 聚合（跨轮累计）；收益人 = 该期参与者 ∪ 其伞上祖先（去重）；
//      伞下集合经 FindDescendantPath 排除自身行，自身投资不计入自己伞下规模。
//返回 err 时调用方不得继续结算（否则团队收益静默漏发且无重试机会，见 Success）。
func (r *RewardManager) calcTeamReward(round model.ProjectRound, voteList []model.Vote) (rewardMap map[string]float64, err error) {
	rewardMap = make(map[string]float64)

	//1.该期全部非管理员投资，按用户聚合（伞下该期总投资额基础数据）
	type periodVote struct {
		WalletId uint
		Amount   float64
	}
	var periodVotes []periodVote
	if err = r.DB.Table(model.VoteTable).
		Select("`wallet_id`, SUM(`amount`) as amount").
		Where("`project_id` = ? and `is_admin` = ?", round.ProjectId, false).
		Group("`wallet_id`").Scan(&periodVotes).Error; err != nil {
		log.WithFields(log.Fields{"Method": "calcTeamReward", "projectId": round.ProjectId}).Errorln("聚合该期投资失败", err)
		return
	}
	periodAmountMap := make(map[uint]float64)
	for _, pv := range periodVotes {
		periodAmountMap[pv.WalletId] = pv.Amount
	}

	//2.收集收益人：该期参与者 ∪ 参与者的伞上祖先（去重）
	beneficiarySet := make(map[uint]struct{})
	for _, vote := range voteList {
		beneficiarySet[vote.WalletId] = struct{}{}
		if ancestors, aErr := r.WalletModel.FindAncestorPath(vote.WalletId); aErr == nil {
			for _, a := range ancestors {
				beneficiarySet[a] = struct{}{}
			}
		}
	}
	if len(beneficiarySet) == 0 {
		return
	}
	beneficiaryIds := make([]uint, 0, len(beneficiarySet))
	for id := range beneficiarySet {
		beneficiaryIds = append(beneficiaryIds, id)
	}

	//3.查询收益人等级，按等级比例计算收益
	var wallets []model.Wallet
	if err = r.DB.Table(model.WalletTable).Find(&wallets, "`id` IN ?", beneficiaryIds).Error; err != nil {
		log.WithFields(log.Fields{"Method": "calcTeamReward"}).Errorln("查询收益人失败", err)
		return
	}
	for _, w := range wallets {
		if w.Level < enum.Level1 {
			continue
		}
		//伞下全部后代（闭包表，不含自身）
		descendants, dErr := r.WalletModel.FindDescendantPath(w.ID)
		if dErr != nil {
			log.WithFields(log.Fields{"Method": "calcTeamReward", "walletId": w.ID}).Errorln("查询伞下后代失败", dErr)
			continue
		}
		//Σ 伞下该期投资额
		var sum float64
		for _, d := range descendants {
			sum += periodAmountMap[d]
		}
		if sum <= 0 {
			continue
		}
		var rate float64
		switch w.Level {
		case enum.Level1:
			rate = TeamRewardRate1
		case enum.Level2:
			rate = TeamRewardRate2
		case enum.Level3:
			rate = TeamRewardRate3
		default:
			continue
		}
		rewardMap[w.Address] = sum * rate
		log.WithFields(log.Fields{"Method": "calcTeamReward", "walletId": w.ID, "level": w.Level, "sum": sum, "rate": rate}).Infoln("团队收益计算完成")
	}
	return
}

func (r *RewardManager) dynamicSharedRewardUpTreePath(walletId uint, vote model.Vote) (rewardMap map[string]float64) {
	var err error
	var ancestors1 []uint
	var ancestors3 []uint
	var ancestors5 []uint
	if ancestors1, err = r.WalletModel.FindAncestorPathAge(walletId, 0); err != nil {
		log.WithFields(log.Fields{"方法": "计算动态分享收益"}).Errorln("找到一代受益者", err)
		return
	}
	if ancestors3, err = r.WalletModel.FindAncestorPathAge(walletId, 2); err != nil {
		log.WithFields(log.Fields{"方法": "计算动态分享收益"}).Errorln("找到三代受益者", err)
		return
	}
	if ancestors5, err = r.WalletModel.FindAncestorPathAge(walletId, 4); err != nil {
		log.WithFields(log.Fields{"方法": "计算动态分享收益"}).Errorln("找到五代受益者", err)
		return
	}
	log.WithFields(log.Fields{"方法": "计算动态分享收益"}).Debugln("找到一代/三代/五代受益者", ancestors1, ancestors3, ancestors5)

	ancestors := make([]uint, 0)
	ancestors = append(ancestors, ancestors1...)
	ancestors = append(ancestors, ancestors3...)
	ancestors = append(ancestors, ancestors5...)

	var wallets []model.Wallet
	if err = r.WalletModel.DB.Table(model.WalletTable).Find(&wallets, "`id` IN ? ", ancestors).Error; err != nil {
		log.WithFields(log.Fields{"方法": "计算动态分享收益"}).Errorln("查询钱包信息错误", ancestors, err)
		return
	}

	walletIdMap := make(map[uint]model.Wallet)
	for _, wallet := range wallets {
		if _, ok := walletIdMap[wallet.ID]; !ok {
			walletIdMap[wallet.ID] = wallet
		}
	}

	voteAmount := vote.Amount
	rewardMap = make(map[string]float64)
	for _, walletId := range ancestors1 {
		totalVoteAmount := voteAmount * DynamicShardReward1GenerateRate
		//满足直推2人
		ds, err := r.WalletModel.DirectDescendants(walletId)
		if err != nil {
			log.Errorln("DirectDescendants err", err)
			continue
		}
		count := len(ds)
		if count < 2 {
			log.Debugln("不满足直推2人")
			continue
		}

		if _, ok := walletIdMap[walletId]; ok {
			address := walletIdMap[walletId].Address
			if _, ok := rewardMap[address]; ok {
				rewardMap[address] += totalVoteAmount
			} else {
				rewardMap[address] = totalVoteAmount
			}
		}
	}

	for _, walletId := range ancestors3 {
		totalVoteAmount := voteAmount * DynamicShardReward3GenerateRate
		//满足直推5人
		ds, err := r.WalletModel.DirectDescendants(walletId)
		if err != nil {
			log.Errorln("DirectDescendants err", err)
			continue
		}
		count := len(ds)
		if count < 5 {
			log.Debugln("不满足直推5人")
			continue
		}

		if _, ok := walletIdMap[walletId]; ok {
			address := walletIdMap[walletId].Address
			if _, ok := rewardMap[address]; ok {
				rewardMap[address] += totalVoteAmount
			} else {
				rewardMap[address] = totalVoteAmount
			}
		}
	}

	for _, walletId := range ancestors5 {
		totalVoteAmount := voteAmount * DynamicShardReward5GenerateRate
		//满足直推10人
		ds, err := r.WalletModel.DirectDescendants(walletId)
		if err != nil {
			log.Errorln("DirectDescendants err", err)
			continue
		}
		count := len(ds)
		if count < 10 {
			log.Debugln("不满足直推10人")
			continue
		}

		if _, ok := walletIdMap[walletId]; ok {
			address := walletIdMap[walletId].Address
			if _, ok := rewardMap[address]; ok {
				rewardMap[address] += totalVoteAmount
			} else {
				rewardMap[address] = totalVoteAmount
			}
		}
	}
	return
}

func (r *RewardManager) GetNoAdminVotes(round model.ProjectRound) (votes []model.Vote) {
	if err := r.DB.Table(model.VoteTable).Find(&votes, "`round_id` = ? and `is_admin` = ?", round.ID, false).Error; err != nil {
		log.WithFields(log.Fields{"roundId": round.ID, "Method": "GetNoAdminVotes"}).Infoln("获取投资用户信息失败", err)
		return
	}
	return
}

func (r *RewardManager) GetAdminVotes(round model.ProjectRound) (votes []model.Vote) {
	if err := r.DB.Table(model.VoteTable).Find(&votes, "`round_id` = ? and `is_admin` = ?", round.ID, true).Error; err != nil {
		log.WithFields(log.Fields{"roundId": round.ID, "Method": "GetNoAdminVotes"}).Infoln("获取投资用户信息失败", err)
		return
	}
	return
}
