package model

import (
	"gorm.io/gorm"
	"time"
)

// ============================== hash_power 算力矿机账户（N次方新增表） ==============================
// 用途：倒2/倒3 轮失败时，扣 50% 币不退，按爆仓价折算为算力进入矿机挖矿，直到三倍出局。
// 对应技术方案 3.2 新增表 hash_power。

const HashPowerTable = "hash_power"

// HashPower 爆仓算力矿机账户
type HashPower struct {
	gorm.Model
	Address      string     `gorm:"index"` //钱包地址
	Symbol       string     `gorm:"index"` //被扣代币（算力基准币）
	Power        float64    //算力值 = 扣除数量 × 爆仓价(U)
	Price        float64    //爆仓时币价(U)
	Mode         uint       `gorm:"default:0"` //0默认三倍出局 1最长300天三倍出局 2TM加速
	TotalReward  float64    `gorm:"default:0"` //累计产出(币本位)
	RemainReward float64    `gorm:"default:0"` //剩余可提
	TargetReward float64    `gorm:"default:0"` //三倍目标(币本位, 入账时按爆仓价快照)
	Status       uint       `gorm:"default:0"` //0挖矿中 1已出局
	StartTime    time.Time  //入账时间
	FinishTime   *time.Time //出局时间
}
