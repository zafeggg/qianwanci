package impl

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils"
	"errors"
	"gorm.io/gorm"
)

type MiningManager struct {
	DB *gorm.DB
	WalletPointSafe core.WalletPointing
}

func (m MiningManager) Exchange(wallet model.Wallet, fusd float64, pcb float64) error {
	address := wallet.Address
	var walletPoint model.WalletPoint

	var err error
	if err = m.DB.Table(model.WalletPointTable).First(&walletPoint, "address = ? and symbol = ?", address, enum.FUSD).Error; err != nil {
		return errors.New("FUSD余额不足")
	}

	balance := float64(walletPoint.Amount) / config.SymbolDictionary[enum.FUSD]
	if balance < fusd {
		return errors.New("FUSD余额不足")
	}

	err = m.DB.Transaction(func(tx *gorm.DB) error {
		walletPoint.Amount -= uint64(fusd * config.SymbolDictionary[enum.FUSD])
		if err = tx.Model(&walletPoint).Update("amount", walletPoint.Amount).Error; err != nil {
			return err
		}

		exchange := new(model.MiningExchange)
		exchange.Address = address
		exchange.Pcb = pcb
		exchange.Fusd = fusd
		exchange.PcbBuff = pcb / fusd
		exchange.RewardBuff = MiningRewardBuff
		if err = tx.Table(model.MiningExchangeTable).Create(exchange).Error; err != nil {
			return err
		}
		return nil
	})

	return err
}

func (m MiningManager) Reward(exchangeId uint, reward float64) error {
	var err error
	var exchange model.MiningExchange
	err = m.DB.Table(model.MiningExchangeTable).First(&exchange, "`id` = ?", exchangeId).Error
	if err != nil {
		return errors.New("找不到兑换记录")
	}

	//1.判断是否停矿
	var stopMining bool
	totalReward := exchange.Reward + reward
	usdt := totalReward * GetKtoSymbolPrice(MiningRewardSymbol)
	if usdt > exchange.Fusd * exchange.RewardBuff {
		stopMining = true
	}


	//2.记录奖励
	err = m.DB.Transaction(func(tx *gorm.DB) error {

		hash, _ := utils.GetRandomHash()
		exchangeReward := new(model.MiningExchangeReward)
		exchangeReward.Hash = hash
		exchangeReward.Address = exchange.Address
		exchangeReward.ExchangeId = exchange.ID
		exchangeReward.Amount = reward
		if err = tx.Table(model.MiningExchangeRewardTable).Create(exchangeReward).Error; err != nil {
			return err
		}

		var mining model.Mining
		err = tx.Table(model.MiningTable).First(&mining, "`address` = ?", exchange.Address).Error
		switch err {
		case gorm.ErrRecordNotFound:
			mining.Address = exchange.Address
			mining.TotalReward = reward
			mining.RemainReward = reward
			if err = tx.Table(model.MiningTable).Create(&mining).Error; err != nil {
				return err
			}
		case nil:
			mining.TotalReward += reward
			mining.RemainReward += reward
			if err = tx.Table(model.MiningTable).Select("total_reward", "remain_reward").Updates(&mining).Error; err != nil {
				return err
			}
		default:
		}

		if err = tx.Model(&exchange).Update("reward", totalReward).Error; err != nil {
			return err
		}

		if stopMining {
			if err = tx.Model(&exchange).Update("status", 1).Error; err != nil {
				return err
			}
		}
		return nil
	})

	return err
}

func (m MiningManager) Withdraw(wallet model.Wallet, reward float64) error {

	var err error
	var mining model.Mining
	err = m.DB.Table(model.MiningTable).First(&mining, "`address` = ?", wallet.Address).Error
	switch err {
	case gorm.ErrRecordNotFound:
		return errors.New("余额不足")
	case nil:
		if reward < MiningWithdrawMinCount {
			return errors.New("低于最低起提数量50个")
		}

		if reward > mining.RemainReward || reward > mining.TotalReward {
			return errors.New("可提取余额不足")
		}

		err = m.DB.Transaction(func(tx *gorm.DB) error {
			//扣减余额
			mining.RemainReward -= reward
			if err = tx.Table(model.MiningTable).Select("remain_reward").Updates(&mining).Error; err != nil {
				return err
			}

			amount, err := m.WalletPointSafe.AddPointAmount(tx, wallet.Address, MiningRewardSymbol, reward)
			if err != nil {
				return err
			}

			//记录提取
			hash, _ := utils.GetRandomHash()
			wtx := new(model.WalletTx)
			wtx.Symbol = MiningRewardSymbol
			wtx.From = config.EtcConfig.KtoPool.Address
			wtx.To = wallet.Address
			wtx.Amount = amount
			wtx.Hash = hash
			wtx.Type = enum.MiningReward
			wtx.Desc = enum.MiningRewardText
			if err = tx.Table(model.WalletTxTable).Create(wtx).Error; err != nil {
				return err
			}

			return nil
		})

	default:
	}
	return err
}

func NewMiningManager() core.Mining {
	return MiningManager{
		DB: config.MysqlDBPool,
		WalletPointSafe: NewWalletPointSafe(),
	}
}
