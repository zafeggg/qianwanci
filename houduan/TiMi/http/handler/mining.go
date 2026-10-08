package handler

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core"
	"com.fibonacci.crowd/core/impl"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/http/req"
	"com.fibonacci.crowd/http/resp"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils"
	"encoding/json"
	"errors"
	"github.com/gofiber/fiber/v2"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type MiningMachineHandler struct {
	DB           *gorm.DB
	MiningManger core.Mining
}

func NewMiningMachineHandler() *MiningMachineHandler {
	return &MiningMachineHandler{DB: config.MysqlDBPool, MiningManger: impl.NewMiningManager()}
}

func (h MiningMachineHandler) Home(c *fiber.Ctx) error {
	var err error
	var walletId uint
	if walletId, err = GetWalletId(c); err != nil {
		return err
	}

	var wallet model.Wallet
	if err = h.DB.Table(model.WalletTable).First(&wallet, "`id` = ?", walletId).Error; err != nil {
		return errors.New("钱包不存在")
	}

	var rewardLoss []model.VoteReward
	if err = h.DB.Order("`created_at` DESC").Table(model.VoteRewardTable).Find(&rewardLoss, "`address` = ? and loss > 0", wallet.Address).Error; err != nil {
		return errors.New("收益明细不存在")
	}

	var mineMachines []model.MineMachine
	if err = h.DB.Table(model.MineMachineTable).Find(&mineMachines).Error; err != nil {
		return errors.New("矿机不存在")
	}

	rls := make([]resp.MiningLoss, 0)
	for _, reward := range rewardLoss {
		var round model.ProjectRound
		if err = h.DB.Table(model.ProjectRoundTable).First(&round, "`id` = ?", reward.RoundId).Error; err != nil {
			log.WithFields(log.Fields{"mining reward loss": "err"}).Errorln(err)
			continue
		}

		rls = append(rls, resp.MiningLoss{
			Id:         reward.ID,
			Period:     round.Period,
			Round:      round.Round,
			Address:    wallet.Address,
			Loss:       reward.Loss,
			Symbol:     round.Symbol,
			CreateTime: reward.CreatedAt,
		})
	}

	var pcbBuff float64
	mls := make([]resp.MiningMachine, 0)
	for index, machine := range mineMachines {
		if index == 1 {
			pcbBuff = machine.Pcb / machine.Fusd
		}
		mls = append(mls, resp.MiningMachine{
			Id:   machine.ID,
			Fusd: machine.Fusd,
			Name: machine.Name,
			Pcb:  machine.Pcb,
		})
	}

	var walletPoint model.WalletPoint
	if err = h.DB.Table(model.WalletPointTable).First(&walletPoint, "address = ? and symbol = ?", wallet.Address, enum.FUSD).Error; err != nil {
		walletPoint = model.WalletPoint{}
	}

	return c.Send(utils.JsonOk(resp.MiningHome{
		Fusd:        utils.FixedFloat6(float64(walletPoint.Amount) / config.SymbolDictionary[enum.FUSD]),
		Buff:        pcbBuff,
		MachineList: mls,
		LossList:    rls,
	}))
}

func (h MiningMachineHandler) Exchange(c *fiber.Ctx) error {
	var err error
	var walletId uint
	if walletId, err = GetWalletId(c); err != nil {
		return err
	}

	var wallet model.Wallet
	if err = h.DB.Table(model.WalletTable).First(&wallet, "`id` = ?", walletId).Error; err != nil {
		return errors.New("钱包不存在")
	}

	var decryptData []byte
	if decryptData, err = DecryptData(c); err != nil {
		return c.Send(utils.JsonFail("decryptData error"))
	}
	var request req.MiningExchange
	if err = json.Unmarshal(decryptData, &request); err != nil {
		return errors.New("参数错误")
	}

	machineId := request.MineMachineId
	var mineMachine model.MineMachine
	if err = h.DB.Table(model.MineMachineTable).First(&mineMachine, "`id` = ?", machineId).Error; err != nil {
		return errors.New("矿机不存在")
	}

	if err = h.MiningManger.Exchange(wallet, mineMachine.Fusd, mineMachine.Pcb); err != nil {
		return err
	}

	return c.Send(utils.JsonOk(nil))
}

func (h MiningMachineHandler) Detail(c *fiber.Ctx) error {
	var err error
	var walletId uint
	if walletId, err = GetWalletId(c); err != nil {
		return err
	}

	var wallet model.Wallet
	if err = h.DB.Table(model.WalletTable).First(&wallet, "`id` = ?", walletId).Error; err != nil {
		return errors.New("钱包不存在")
	}

	var totalPcb float64
	var pcb float64
	if err = h.DB.Table(model.MiningExchangeTable).Where("`status` = ?", 0).Pluck("COALESCE(SUM(`pcb`), 0) as sumPcb", &totalPcb).Error; err != nil {
		return errors.New("总算力获取错误")
	}

	if err = h.DB.Table(model.MiningExchangeTable).Where("`status` = ? and `address` = ? ", 0, wallet.Address).Pluck("COALESCE(SUM(`pcb`), 0) as sumPcb", &pcb).Error; err != nil {
		return errors.New("获取个人算力错误")
	}

	var mining model.Mining
	if err = h.DB.Table(model.MiningTable).Where("`address` = ? ", wallet.Address).Find(&mining).Limit(1).Error; err != nil {
		return errors.New("获取奖励信息错误")
	}

	return c.Send(utils.JsonOk(resp.MiningPower{
		TotalPcb:    utils.FixedFloat2(totalPcb),
		Pcb:         utils.FixedFloat2(pcb),
		TotalReward: utils.FixedFloat2(mining.TotalReward),
		Reward:      utils.FixedFloat2(mining.RemainReward),
	}))
}

func (h MiningMachineHandler) Withdraw(c *fiber.Ctx) error {
	var err error
	var walletId uint
	if walletId, err = GetWalletId(c); err != nil {
		return err
	}

	var wallet model.Wallet
	if err = h.DB.Table(model.WalletTable).First(&wallet, "`id` = ?", walletId).Error; err != nil {
		return errors.New("钱包不存在")
	}

	var decryptData []byte
	if decryptData, err = DecryptData(c); err != nil {
		return c.Send(utils.JsonFail("decryptData error"))
	}
	var request req.MiningWithdraw
	if err = json.Unmarshal(decryptData, &request); err != nil {
		return errors.New("参数错误")
	}

	amount := request.Amount
	pwd := request.Pwd
	if !utils.ComparePasswords(wallet.Password, []byte(pwd)) {
		return errors.New("密码错误")
	}

	if err = h.MiningManger.Withdraw(wallet, amount); err != nil {
		return err
	}

	return c.Send(utils.JsonOk("成功"))
}

func (h MiningMachineHandler) Record(c *fiber.Ctx) error {
	var err error
	var walletId uint
	if walletId, err = GetWalletId(c); err != nil {
		return err
	}

	var wallet model.Wallet
	if err = h.DB.Table(model.WalletTable).First(&wallet, "`id` = ?", walletId).Error; err != nil {
		return errors.New("钱包不存在")
	}

	var rewards []model.MiningExchangeReward
	if err = h.DB.Order("`created_at` DESC").Table(model.MiningExchangeRewardTable).Where("`address` = ?", wallet.Address).Find(&rewards).Error; err != nil {
		return errors.New("总算力获取错误")
	}

	rds := make([]resp.MiningReward, 0)
	for _, reward := range rewards {
		rds = append(rds, resp.MiningReward{
			Id:         reward.ID,
			Reward:     reward.Amount,
			Hash:       reward.Hash,
			CreateTime: reward.CreatedAt,
		})
	}

	return c.Send(utils.JsonOk(rds))
}
