package handler

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils"
	"errors"
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

type SelfHandler struct {
	DB *gorm.DB
}

func (h SelfHandler) Community(c *fiber.Ctx) error {
	var err error
	var walletId uint
	if walletId, err = GetWalletId(c); err != nil {
		return err
	}

	var wallet model.Wallet
	if err = h.DB.Table(model.WalletTable).First(&wallet, "`id` = ?", walletId).Error; err != nil {
		return errors.New("钱包不存在")
	}

	//直推子代
	var descendants []uint
	if err = h.DB.Table(model.WalletTreeTable).Select("`descendant`").Find(&descendants, "ancestor = ? and distance = 0", walletId).Error; err != nil {
		return errors.New("关系树查询失败")
	}

	var ws []model.Wallet
	if err = h.DB.Table(model.WalletTable).Select("`name`, `address`, `active`, `level`").Find(&ws, "`id` IN ?", descendants).Error; err != nil {
		return errors.New("关系树钱包查询失败")
	}

	type wl struct {
		Name    string `json:"name"`
		Address string `json:"address"`
		Active  bool   `json:"active"`
	}
	wls := make([]wl, 0)

	var f1 int
	var f2 int
	var f3 int
	for _, w := range ws {
		switch w.Level {
		case enum.Level1:
			f1 += 1
		case enum.Level2:
			f2 += 1
		case enum.Level3:
			f3 += 1
		}

		wls = append(wls, wl{Name: w.Name, Address: w.Address, Active: w.Active})
	}

	return c.Send(utils.JsonOk(fiber.Map{"totalCount": len(wls), "walletList": wls, "f1": f1, "f2": f2, "f3": f3}))
}

func (h SelfHandler) InvitedCode(c *fiber.Ctx) error {
	var err error
	var walletId uint
	if walletId, err = GetWalletId(c); err != nil {
		return err
	}

	var wallet model.Wallet
	if err = h.DB.Table(model.WalletTable).First(&wallet, "`id` = ?", walletId).Error; err != nil {
		return errors.New("钱包不存在")
	}

	return c.Send(utils.JsonOk(fiber.Map{"code": wallet.Code}))
}

func (h SelfHandler) Home(c *fiber.Ctx) error {
	var err error
	var walletId uint
	if walletId, err = GetWalletId(c); err != nil {
		return err
	}

	var wallet model.Wallet
	if err = h.DB.Table(model.WalletTable).First(&wallet, "`id` = ?", walletId).Error; err != nil {
		return errors.New("钱包不存在")
	}

	return c.Send(utils.JsonOk(fiber.Map{"name": wallet.Name, "level": wallet.Level, "address": wallet.Address, "active": wallet.Active}))
}

func NewSelfHandler() *SelfHandler {
	return &SelfHandler{DB: config.MysqlDBPool}
}
