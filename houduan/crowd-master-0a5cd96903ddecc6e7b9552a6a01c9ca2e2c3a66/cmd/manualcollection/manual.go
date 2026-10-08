package main

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils/kto"
	"flag"
	"fmt"
	"github.com/sirupsen/logrus"
)

func main() {
	path := flag.String("f", "/Users/zhengjianfeng_1/go/src/crowd/config/etc.yml", "-f 指定配置文件")
	symbol := flag.String("symbol", "FIBO", "-symbol 指定归集代币")

	flag.Parse()
	config.Init(*path) //mysql, redis, config file

	//搜索所有钱包
	var wallets []model.Wallet
	err := config.MysqlDBPool.Table(model.WalletTable).Find(&wallets).Error
	if err != nil {
		logrus.Error("查询钱包地址错误", err)
		return
	}

	collectionSymbol := *symbol
	ktoSymbol := "KTO"
	collectionAddr := config.EtcConfig.KtoPool.Address
	collectionAddrPrivate := config.EtcConfig.KtoPool.Private

	for _, wallet := range wallets {
		logrus.WithFields(logrus.Fields{"地址": wallet.Address}).Infoln("=========正在处理钱包余额信息========")
		address := wallet.Address
		private := wallet.Private

		//找到所有余额
		//补贴kto
		//执行转账
		balance := kto.GetBalance(collectionSymbol, address)
		if balance > 0 {
			logrus.WithFields(logrus.Fields{"地址": wallet.Address}).Infoln(fmt.Sprintf("钱包%s余额有币: %d", collectionSymbol, balance))

			feeBalance := kto.GetBalance(ktoSymbol, address)

			//5500000
			if feeBalance < 5500000 {
				logrus.WithFields(logrus.Fields{"地址": wallet.Address}).Infoln("补贴交易费: ", 5500000)

				_, _, err := kto.KTOonChainSync(ktoSymbol, collectionAddr, collectionAddrPrivate, address, 5500000)
				if err != nil {
					logrus.WithFields(logrus.Fields{"地址": wallet.Address}).Infoln("补贴交易费失败: ", balance)
					continue
				}
			}

			_, _, err := kto.KTOonChainSync(collectionSymbol, address, private, collectionAddr, balance)
			if err != nil {
				logrus.WithFields(logrus.Fields{"地址": wallet.Address}).Infoln(fmt.Sprintf("归集合%s失败: %d", collectionSymbol, balance))
				continue
			}
		}
		logrus.WithFields(logrus.Fields{"地址": wallet.Address}).Infoln("=========处理钱包余额信息完成========")
	}

}
