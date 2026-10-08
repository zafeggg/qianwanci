package main

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/model"
	"flag"
	log "github.com/sirupsen/logrus"
	"testing"
)

func Read() {
	path := flag.String("f", "/Users/zhengjianfeng_1/go/src/crowd/config/etc.yml", "-f 指定配置文件")
	flag.Parse()
	config.Init(*path) //mysql, redis, config file
}


func TestMiningPcb(t *testing.T) {
	Read()

	var exchanges []model.MiningExchange
	if err := config.MysqlDBPool.Table(model.MiningExchangeTable).Find(&exchanges, "`status` = ?", 0).Error; err != nil {
		log.WithFields(log.Fields{"MiningExchange": "查询所有产矿兑换记录失败"}).Errorln(err)
		return
	}

	t.Log(exchanges)
}
