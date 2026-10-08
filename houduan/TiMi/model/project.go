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

// ProjectRound 众筹轮次。
//
// 唯一索引 uk_project_round_no（2026-09-19 补，C 模块账本一致性）：
// 同一项目下轮号必须唯一——轮号重复会让「第 N 轮」的结算/查询命中多行；
// 历史上 /demo/bootstrap 复用轮号已实际产生过重复行（recon 会报该项 ERROR）。
// ⚠ 与 wallet 等表同口径：GORM 只对带 index 标签的 string 字段用 varchar(191)，
// 仅带 uniqueIndex 的字符串字段会建成 longtext 而无法建索引（详见 model/wallet.go 注释）。
type ProjectRound struct {
	gorm.Model
	ProjectId   uint      `gorm:"index;uniqueIndex:uk_project_round_no,priority:1"` //项目ID
	Period      uint      `gorm:"index"`                                            //第几期
	Round       uint      `gorm:"index;uniqueIndex:uk_project_round_no,priority:2"` //第几轮（同项目内唯一）
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

// 项目轮唯一索引的列顺序：project_id 需要在索引里占 priority:1，
// 但 ProjectId 字段同时带普通 index；GORM 允许同一字段属于多个索引，故在此显式声明。
const ProjectRoundUniqueIndex = "uk_project_round_no"
