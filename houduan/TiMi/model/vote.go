package model

import "gorm.io/gorm"

const (
	VoteTable       = "vote"
	VoteRewardTable = "vote_reward"
)

//Vote 投资记录
type Vote struct {
	gorm.Model
	ProjectId uint    `gorm:"index"`           //项目期ID
	RoundId   uint    `gorm:"index"`           //参与众筹ID
	Period    uint    `gorm:"index"`           //第几期
	Round     uint    `gorm:"index"`           //第几轮
	WalletId  uint    `gorm:"index"`           //钱包ID
	IsAdmin   bool    `gorm:"index;default:0"` //是否为超级账户
	Level     uint    `gorm:"index"`           //0普通 1：F1 2:F2 3:F3
	Address   string  `gorm:"index"`           //钱包地址
	Count     uint    //投入数量
	Symbol    string  //投资代币符号
	Amount    float64 //参与数量
}

type VoteReward struct {
	gorm.Model
	ProjectId    uint    `gorm:"index"` //项目期ID
	RoundId      uint    `gorm:"index"` //参与众筹ID
	VoteId       uint    `gorm:"index"` //参与ID
	Address      string  `gorm:"index"` //钱包地址
	DynamicTeam  float64 //动态团队收益
	DynamicShard float64 //动态分享收益
	Static       float64 //静态收益
	Loss         float64 //亏损收益
}
