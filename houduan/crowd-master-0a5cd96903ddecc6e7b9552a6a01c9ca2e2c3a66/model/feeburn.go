package model

import "gorm.io/gorm"

// ============================== fee_burn 手续费销毁记录（N次方新增表） ==============================
// 用途：提币 3% 手续费（以手续费币种支付）先记入本表待销毁，由 burn 定时程序汇总链上销毁，
//      直至流通量仅剩 7777 枚（通缩设计）。对应技术方案 3.2 新增表 fee_burn。

const FeeBurnTable = "fee_burn"

// FeeBurn 手续费销毁记录
type FeeBurn struct {
	gorm.Model
	Symbol string `gorm:"index"` //手续费币种
	Amount uint64 //销毁数量(最小单位)
	TxHash string `gorm:"index"`     //链上销毁交易hash
	Status uint   `gorm:"default:0"` //0待销毁 1已销毁 2上链失败待重试 3销毁中(已抢占,防重复销毁,滞留需人工核查)
}
