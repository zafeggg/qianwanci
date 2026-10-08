package model

import (
	"com.fibonacci.crowd/config"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"strconv"
)

const (
	WalletTable           = "wallet"
	WalletPointTable      = "wallet_point"
	WalletTreeTable       = "wallet_tree"
	WalletTxTable         = "wallet_tx"
	WalletHashChargeTable = "wallet_hash_charge"
)

//Wallet 钱包
type Wallet struct {
	gorm.Model
	Address     string `gorm:"index"` //钱包地址
	Private     string //私钥
	TronAddress string `gorm:"index"` //波场
	TronPrivate string //
	Name        string
	Password    string //钱包密码
	Sign        string //恢复密钥
	Level       uint   `gorm:"index"` //0普通 1：F1 2:F2 3:F3
	Active      bool   //0未激活 1已激活
	Admin       bool   //0非超级管理员 1超级管理员
	Code        string `gorm:"index"` //邀请码
}

//WalletHashCharge 充值记录
type WalletHashCharge struct {
	gorm.Model
	Address string `gorm:"index"` //钱包地址
	Hash    string `gorm:"index"` //充值Hash
	Symbol  string `gorm:"index"`
	Amount  uint64
}

type WalletTx struct {
	gorm.Model
	From    string
	To      string
	Symbol  string
	Amount  uint64
	Desc    string
	Hash    string
	Success uint `gorm:"default:0"` //0成功 1失败
	Type    uint
}

//WalletPoint 钱包积分
type WalletPoint struct {
	gorm.Model
	Address string `gorm:"index"` //钱包地址
	Symbol  string
	Amount  uint64
}

//WalletTree 钱包邀请关系
//index_Ancestor_Distance
//index_Descendant_Distance
type WalletTree struct {
	Ancestor   uint `gorm:"index"` //祖先
	Descendant uint `gorm:"index"` //子代
	Distance   uint `gorm:"index"` //隔代
}

type WalletModel struct {
	DB *gorm.DB
}

func NewWalletModel() *WalletModel {
	return &WalletModel{DB: config.MysqlDBPool}
}

func (wm *WalletModel) FindAncestorPath(walletId uint) (ancestors []uint, err error) {
	if err = wm.DB.Table(WalletTreeTable).Order("`distance`").Select("`ancestor`").Find(&ancestors, "`descendant` = ? and `distance` >= 0", walletId).Error; err != nil {
		return
	}
	ancestors = append([]uint{walletId}, ancestors...)

	//移除未激活

	log.WithFields(log.Fields{"方法": "找到父路径"}).Infoln("find path", ancestors)
	return
}

func (wm *WalletModel) FindAncestorPathAge(walletId uint, age int) (ancestors []uint, err error) {
	if err = wm.DB.Table(WalletTreeTable).Order("`distance`").Select("`ancestor`").Find(&ancestors, "`descendant` = ? and `distance` = ?", walletId, age).Error; err != nil {
		return
	}
	//移除未激活

	log.WithFields(log.Fields{"方法": "找到指定代父路径"}).Infoln("find age: "+strconv.Itoa(age)+", path: ", ancestors)
	return
}

//DirectDescendants 直推账号
func (wm *WalletModel) DirectDescendants(walletId uint) (descendants []uint, err error) {
	var des []uint
	if err = wm.DB.Table(WalletTreeTable).Order("`distance`").Select("`descendant`").Find(&des, "`ancestor` = ? and `distance` = ?", walletId, 0).Error; err != nil {
		return
	}

	//移除未激活
	var wallets []Wallet
	if err = wm.DB.Table(WalletTable).Select("id", "active").Find(&wallets, "`id` IN ?", des).Error; err != nil {
		log.Errorln("search all wallets err: ", err)
		return
	}

	for _, wallet := range wallets {
		if wallet.Active {
			descendants = append(descendants, wallet.ID)
		}
	}

	log.WithFields(log.Fields{"方法": "找到直推子代"}).Infoln("find direct path: ", descendants)
	return
}

//FindDescendantPath 伞下全部后代（N次方新增）
//闭包表查询：ancestor = walletId 的所有 descendant（含各代）。
//注意：注册主路径（Registration→InviteWithTx）不写自身行，但 ops 导入渠道（Invite）会写 (id,id,0) 自身行，
//      故显式排除 descendant = walletId，保证伞下集合在任何数据形态下都不含自身（否则团队收益会把自身投资计入伞下）。
//用于团队收益（伞下该期总投资额比例制）计算，见 core/impl/reward.go calcTeamReward。
func (wm *WalletModel) FindDescendantPath(walletId uint) (descendants []uint, err error) {
	if err = wm.DB.Table(WalletTreeTable).Order("`distance`").Select("`descendant`").
		Find(&descendants, "`ancestor` = ? and `distance` >= 0 and `descendant` <> ?", walletId, walletId).Error; err != nil {
		return
	}
	log.WithFields(log.Fields{"方法": "找到伞下全部后代", "walletId": walletId}).Infoln("find descendant path: ", descendants)
	return
}
