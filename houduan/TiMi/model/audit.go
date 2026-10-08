package model

import "gorm.io/gorm"

// OpsAuditTable 运维操作审计表名
const OpsAuditTable = "ops_audit"

// OpsAudit 运维后台操作审计（E 模块）。
//
// 背景：`cmd/ops` 的 17 条路由可直接开期/开轮/结束轮/批量建钱包/改等级/改运行参数/改算力模式/触发测试造数，
// 全部是高危写操作，而此前**没有任何留痕**——出了事故无法追责，也无法回溯「谁在什么时候把参数改成了什么」。
//
// 记录策略：每次 ops 调用落一行（含失败），请求体经脱敏后原样保存。
// 表由 cmd/ops 启动时 AutoMigrate 创建。
type OpsAudit struct {
	gorm.Model
	Account  string `gorm:"size:64;index"`  //操作者：JWT claim name（登录接口记请求体 account）
	WalletId uint   `gorm:"index"`          //操作者 walletId（超级账号为 0）
	Method   string `gorm:"size:16"`        //HTTP 方法
	Path     string `gorm:"size:191;index"` //路由模板（低基数，便于聚合）
	IP       string `gorm:"size:64"`        //客户端 IP
	Body     string `gorm:"type:text"`      //请求体（已脱敏）
	Status   int    //HTTP 状态码
	RespCode int    //业务码（响应体 code 字段；0/200 为成功）
	ErrMsg   string `gorm:"type:text"` //失败原因（handler 返回的 error）
	CostMs   int64  //耗时（毫秒）
}
