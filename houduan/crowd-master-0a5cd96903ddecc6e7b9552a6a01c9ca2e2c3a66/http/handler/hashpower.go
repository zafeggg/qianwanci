package handler

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core"
	"com.fibonacci.crowd/core/impl"
	"com.fibonacci.crowd/http/req"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils"
	"encoding/json"
	"errors"
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

// ============================== HashPower 算力矿机 API（N次方新增） ==============================
// 路由：/hashpower/home（我的算力矿机）、/hashpower/withdraw（提取 FIBO）
// 与 /mining 挖矿 API 并列，独立管理爆仓折算的算力账户。

type HashPowerHandler struct {
	DB               *gorm.DB
	HashPowerManager core.HashPowering
}

func NewHashPowerHandler() *HashPowerHandler {
	return &HashPowerHandler{
		DB:               config.MysqlDBPool,
		HashPowerManager: impl.NewHashPowerManager(),
	}
}

// Home 我的算力矿机首页：账户列表 + 汇总（算力/累计产出/剩余可提）
func (h HashPowerHandler) Home(c *fiber.Ctx) error {
	var err error
	var walletId uint
	if walletId, err = GetWalletId(c); err != nil {
		return err
	}

	var wallet model.Wallet
	if err = h.DB.Table(model.WalletTable).First(&wallet, "`id` = ?", walletId).Error; err != nil {
		return errors.New("钱包不存在")
	}

	home, err := h.HashPowerManager.Home(wallet.Address)
	if err != nil {
		return err
	}

	//账户列表转为对外响应（隐藏敏感字段）
	type powerResp struct {
		Id           uint    `json:"id"`
		Symbol       string  `json:"symbol"`       //被扣代币
		Power        float64 `json:"power"`        //算力(U)
		Price        float64 `json:"price"`        //爆仓时币价(U)
		Mode         uint    `json:"mode"`         //0默认三倍 1最长300天 2TM加速
		TotalReward  float64 `json:"totalReward"`  //累计产出(FIBO)
		RemainReward float64 `json:"remainReward"` //剩余可提(FIBO)
		Status       uint    `json:"status"`       //0挖矿中 1已出局
		StartTime    string  `json:"startTime"`
	}
	list := make([]powerResp, 0, len(home.AccountList))
	for _, p := range home.AccountList {
		list = append(list, powerResp{
			Id:           p.ID,
			Symbol:       p.Symbol,
			Power:        utils.FixedFloat4(p.Power),
			Price:        utils.FixedFloat4(p.Price),
			Mode:         p.Mode,
			TotalReward:  utils.FixedFloat6(p.TotalReward),
			RemainReward: utils.FixedFloat6(p.RemainReward),
			Status:       p.Status,
			StartTime:    p.StartTime.Format("2006-01-02 15:04:05"),
		})
	}

	return c.Send(utils.JsonOk(fiber.Map{
		"totalPower":   utils.FixedFloat4(home.TotalPower),   //总算力(U)
		"totalReward":  utils.FixedFloat6(home.TotalReward),  //累计产出(FIBO)
		"remainReward": utils.FixedFloat6(home.RemainReward), //剩余可提(FIBO)
		"accountList":  list,
	}))
}

// Withdraw 提取算力矿机产出（FIBO），最低起提 50 个，需钱包密码
func (h HashPowerHandler) Withdraw(c *fiber.Ctx) error {
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
	var request req.MiningWithdraw //复用挖矿提取请求结构（amount/pwd）
	if err = json.Unmarshal(decryptData, &request); err != nil {
		return errors.New("参数错误")
	}

	if !utils.ComparePasswords(wallet.Password, []byte(request.Pwd)) {
		return errors.New("密码错误")
	}

	if err = h.HashPowerManager.Withdraw(wallet, request.Amount); err != nil {
		return err
	}

	return c.Send(utils.JsonOk("成功"))
}
