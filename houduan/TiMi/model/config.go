package model

import "gorm.io/gorm"

const (
	NotifyTable = "notify"
	ConfigTable = "config"
)

type Notify struct {
	gorm.Model
	WalletId uint `gorm:"index"`
	Content  string
	Type     uint //0 taskbar 1系统消息
}

type Config struct {
	gorm.Model
	Version  string
	Download string
	// Params 运行参数 JSON（迭代0 参数化，见 config/params.go 与 core/impl/params.go；
	// 由 ops /ops/params 接口维护，启动时各程序 LoadNpowerParams 读取）
	Params string `gorm:"type:longtext"`
}
