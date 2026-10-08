package main

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/model"
	log "github.com/sirupsen/logrus"
	"os"
	"testing"
)

// Read 初始化测试配置（集成测试前置）。
// 修复（2026-09-30）：原实现把原作者的 macOS 绝对路径写死成 flag 默认值，任何其它机器上
// 都直接 panic。现在改为显式 opt-in（NPOWER_TEST_CONFIG），未设置或依赖不可用时跳过。
func Read(t *testing.T) {
	t.Helper()
	path := os.Getenv("NPOWER_TEST_CONFIG")
	if path == "" {
		t.Skip("跳过集成测试：需设置 NPOWER_TEST_CONFIG 指向可用配置（依赖 MySQL/Redis）")
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("跳过集成测试：未找到测试配置 %s", path)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("跳过集成测试：初始化依赖失败：%v", r)
		}
	}()
	config.Init(path) //mysql, redis, config file
}


func TestMiningPcb(t *testing.T) {
	Read(t)
	var exchanges []model.MiningExchange
	if err := config.MysqlDBPool.Table(model.MiningExchangeTable).Find(&exchanges, "`status` = ?", 0).Error; err != nil {
		log.WithFields(log.Fields{"MiningExchange": "查询所有产矿兑换记录失败"}).Errorln(err)
		return
	}

	t.Log(exchanges)
}
