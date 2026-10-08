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

// Wallet 钱包
// 唯一索引（2026-09-18 C模块加固）：地址与邀请码在数据库层唯一。
// 显式命名 uk_* 前缀：GORM AutoMigrate 按索引名判存，若沿用默认名 idx_*，
// 会因旧的同名非唯一索引已存在而跳过创建（唯一约束静默失效）。
//
// ⚠ 必须显式 size：GORM 的 MySQL 驱动只对带 `index` 标签的 string 字段用 varchar(191)，
// **仅带 `uniqueIndex` 的字段会建成 longtext**，MySQL 不允许在 longtext 上建索引 →
// AutoMigrate 只打日志不中断，唯一约束形同虚设（2026-09-18 实测 err 1170）。
// 现有库这几列已是 varchar(191)（历史 `index` 标签遗留），加 size:191 不触发 ALTER；
// **新建库则完全依赖 size 才能建出唯一索引**。
//
// 注：软删除（deleted_at）与唯一索引并存，已软删记录仍占位，属已知取舍。
type Wallet struct {
	gorm.Model
	Address string `gorm:"size:191;uniqueIndex:uk_wallet_address"` //钱包地址
	// EvmAddress EVM(0x) 地址，签名登录新增：私有链外的 EVM 生态地址与 KTO 地址并存。
	// 用指针保留可空：MySQL 唯一索引允许多个 NULL，历史 KTO 钱包此列为 NULL 不会互相冲突。
	// EVM 地址为 0x+40 hex = 42 字符，size=64 留余量。
	EvmAddress  *string `gorm:"size:64;uniqueIndex:uk_wallet_evm_address"`
	Private     string  //私钥
	TronAddress string  `gorm:"index"` //波场
	TronPrivate string  //
	Name        string
	Password    string //钱包密码
	Sign        string //恢复密钥
	Level       uint   `gorm:"index"` //0普通 1：F1 2:F2 3:F3
	Active      bool   //0未激活 1已激活
	Admin       bool   //0非超级管理员 1超级管理员
	Code        string `gorm:"size:191;uniqueIndex:uk_wallet_code"` //邀请码
}

// WalletHashCharge 充值记录
// hash 唯一：同一笔链上转账只能入账一次（防止监听重放/回调重试导致重复充值）。
type WalletHashCharge struct {
	gorm.Model
	Address string `gorm:"index"`                                           //钱包地址
	Hash    string `gorm:"size:191;uniqueIndex:uk_wallet_hash_charge_hash"` //充值Hash
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

// WalletPoint 钱包积分
type WalletPoint struct {
	gorm.Model
	Address string `gorm:"index"` //钱包地址
	Symbol  string
	Amount  uint64
}

// WalletTree 钱包邀请关系
// index_Ancestor_Distance
// index_Descendant_Distance
// 唯一索引（2026-09-18 C模块加固）：(ancestor,descendant,distance) 唯一，
// 防止注册/邀请并发或重放写入重复闭包边（重复边会让动态/团队收益重复计算）。
type WalletTree struct {
	Ancestor   uint `gorm:"index;uniqueIndex:uk_wallet_tree_edge,priority:1"` //祖先
	Descendant uint `gorm:"index;uniqueIndex:uk_wallet_tree_edge,priority:2"` //子代
	Distance   uint `gorm:"index;uniqueIndex:uk_wallet_tree_edge,priority:3"` //隔代
}

type WalletModel struct {
	DB *gorm.DB
}

func NewWalletModel() *WalletModel {
	return &WalletModel{DB: config.MysqlDBPool}
}

func (wm *WalletModel) FindAncestorPath(walletId uint) (ancestors []uint, err error) {
	//防御：排除自身行（历史 ops Invite 渠道 (id,id,0) 脏行），祖先链不重复含自身；自身由下方 prepend 一次
	if err = wm.DB.Table(WalletTreeTable).Order("`distance`").Select("`ancestor`").
		Find(&ancestors, "`descendant` = ? and `distance` >= 0 and `ancestor` <> ?", walletId, walletId).Error; err != nil {
		return
	}
	ancestors = append([]uint{walletId}, ancestors...)

	//移除未激活

	log.WithFields(log.Fields{"方法": "找到父路径"}).Infoln("find path", ancestors)
	return
}

// FindAncestorPathAge 指定代的父路径（1代=0,3代=2,5代=4；偏移约定，见 InviteWithTx）
// 防御：排除自身行（ancestor<>walletId）。历史 ops Invite 渠道曾写 (id,id,0) 脏行，
// 若不排除，用户会被算成自己的一代受益人（动态分享错发自身）。
func (wm *WalletModel) FindAncestorPathAge(walletId uint, age int) (ancestors []uint, err error) {
	if err = wm.DB.Table(WalletTreeTable).Order("`distance`").Select("`ancestor`").
		Find(&ancestors, "`descendant` = ? and `distance` = ? and `ancestor` <> ?", walletId, age, walletId).Error; err != nil {
		return
	}
	//移除未激活

	log.WithFields(log.Fields{"方法": "找到指定代父路径"}).Infoln("find age: "+strconv.Itoa(age)+", path: ", ancestors)
	return
}

// DirectDescendants 直推账号（distance=0 即直推，偏移约定）
// 防御：排除自身行 descendant<>walletId（历史 ops Invite 渠道 (id,id,0) 脏行，见 FindDescendantPath 注释）
func (wm *WalletModel) DirectDescendants(walletId uint) (descendants []uint, err error) {
	var des []uint
	if err = wm.DB.Table(WalletTreeTable).Order("`distance`").Select("`descendant`").
		Find(&des, "`ancestor` = ? and `distance` = ? and `descendant` <> ?", walletId, 0, walletId).Error; err != nil {
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

// FindDescendantPath 伞下全部后代（N次方新增）
// 闭包表查询：ancestor = walletId 的所有 descendant（含各代）。
// 注意：注册主路径（Registration→InviteWithTx）不写自身行，但 ops 导入渠道（Invite）会写 (id,id,0) 自身行，
//
//	故显式排除 descendant = walletId，保证伞下集合在任何数据形态下都不含自身（否则团队收益会把自身投资计入伞下）。
//
// 用于团队收益（伞下该期总投资额比例制）计算，见 core/impl/reward.go calcTeamReward。
func (wm *WalletModel) FindDescendantPath(walletId uint) (descendants []uint, err error) {
	if err = wm.DB.Table(WalletTreeTable).Order("`distance`").Select("`descendant`").
		Find(&descendants, "`ancestor` = ? and `distance` >= 0 and `descendant` <> ?", walletId, walletId).Error; err != nil {
		return
	}
	log.WithFields(log.Fields{"方法": "找到伞下全部后代", "walletId": walletId}).Infoln("find descendant path: ", descendants)
	return
}
