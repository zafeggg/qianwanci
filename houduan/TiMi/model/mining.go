package model

import "gorm.io/gorm"

const (
	MineMachineTable          = "mine_machine"
	MiningTable               = "mining"
	MiningExchangeTable       = "mining_exchange"
	MiningExchangeRewardTable = "mining_exchange_reward"
)

type MineMachine struct {
	gorm.Model
	Fusd float64 `gorm:"default:0"`
	Pcb  float64 `gorm:"default:0"`
	Name string
}

type Mining struct {
	gorm.Model
	Address      string `gorm:"index"`
	TotalReward  float64
	RemainReward float64
}

type MiningExchange struct {
	gorm.Model
	Address string  `gorm:"index"`
	Fusd    float64 `gorm:"default:0"`
	Pcb     float64 `gorm:"index;default:0"`
	Reward  float64 `gorm:"default:0"`

	RewardBuff float64 `gorm:"default:0"` //收益膨胀系数
	PcbBuff    float64 `gorm:"default:0"` //pcb膨胀系数
	Status     uint    `gorm:"default:0"` //0挖矿中 1停止挖矿
}

type MiningExchangeReward struct {
	gorm.Model
	Address    string `gorm:"index"`
	Hash       string
	ExchangeId uint
	Amount     float64 `gorm:"default:0"`
}
