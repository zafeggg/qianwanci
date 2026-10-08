package model

import (
	"gorm.io/gorm"
	"time"
)

const (
	ProjectTable      = "project"
	ProjectRoundTable = "project_round"
)

type Project struct {
	gorm.Model
	Period uint   `gorm:"index"` //第几期
	Symbol string //众筹代币
	Status uint   //0未开始 1进行中 2停止
}

type ProjectRound struct {
	gorm.Model
	ProjectId   uint      `gorm:"index"` //项目ID
	Period      uint      `gorm:"index"` //第几期
	Round       uint      `gorm:"index"` //第几轮
	TimeLimit   float64   //时间限制:秒
	TargetVote  float64   //目标众筹数量
	MinVote     float64   //最小投入数量
	MaxVote     float64   //最大投入数量
	CurrentVote float64   //众筹数量
	StartTime   time.Time //开始时间
	EndTime     time.Time //结束时间
	Count       uint      //参与人数
	Symbol      string    `gorm:"index"` //众筹代币
	Success     bool      //0 未成功 1成功
	Status      uint      `gorm:"index"` //0未开始 1进行中 2待结算 3已结束
}
