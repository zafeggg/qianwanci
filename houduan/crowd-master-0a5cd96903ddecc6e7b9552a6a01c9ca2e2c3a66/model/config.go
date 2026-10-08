package model

import "gorm.io/gorm"

const (
	NotifyTable = "notify"
	ConfigTable = "config"
)

type Notify struct {
	gorm.Model
	WalletId uint `gorm:"index"`
	Content string
	Type uint //0 taskbar 1系统消息
}

type Config struct {
	gorm.Model
	Version string
	Download string
}

