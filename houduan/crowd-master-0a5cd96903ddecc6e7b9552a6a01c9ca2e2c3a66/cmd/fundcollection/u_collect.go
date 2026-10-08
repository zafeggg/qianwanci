//go:build ignore

// u_collect.go 为 USDT 归集扫描脚本（存量半成品，仅检查 TRX 余额、无实际转账逻辑）。
// 通过 //go:build ignore 排除出默认构建（解决与 collecting.go 的 main 重复声明），
// 需要单独执行时使用：go run u_collect.go -f ./collecting.yml
package main

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils/tron"
	"flag"
	log "github.com/sirupsen/logrus"
)

//Collection usdt
func main() {
	path := flag.String("f", "/Users/zhengjianfeng_1/go/src/crowd/config/collecting.yml", "-f 指定配置文件")
	flag.Parse()
	config.Init(*path) //mysql, redis, config file

	var walletPoints []model.WalletPoint
	if err := config.MysqlDBPool.Table(model.WalletPointTable).Find(&walletPoints, "symbol = ? and amount > 0", enum.USDT).Error; err != nil {
		log.Error("find u may wallets err", err)
		return
	}

	log.Infof("search wallet points length: %d , going handle \n", len(walletPoints))

	for _,  point := range walletPoints {
		log.Infof("handing point for address: %v", point.Address)

		var wallet model.Wallet
		if err := config.MysqlDBPool.Table(model.WalletTable).First(&wallet, "address = ?", point.Address).Error; err != nil {
			log.Errorf("find address: %v wallet info err: %v", point.Address, err)
			continue
		}

		//prepare collection u
		tronAddress := wallet.TronAddress
		//1.check trx
		trxBalance, err := tron.GetTrxBalance(tronAddress)
		if err != nil {
			log.Errorf("get trx balance err: %v", err)
			continue
		}
		//5 trx fee
		if trxBalance > 5000000 {
			continue
		}


	}
}
