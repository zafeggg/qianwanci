package main

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/utils/kto"
	"com.fibonacci.crowd/utils/tron"
	"context"
	"encoding/json"
	"flag"
	log "github.com/sirupsen/logrus"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//Collection funding to address
func main() {
	path := flag.String("f", "/Users/zhengjianfeng_1/go/src/crowd/config/collecting.yml", "-f 指定配置文件")
	flag.Parse()
	config.Init(*path) //mysql, redis, config file

	//订阅频道
	pubsub := config.RedisClient.Subscribe(context.Background(), enum.FundCollectionQueue)

	// 用管道来接收消息
	ch := pubsub.Channel()

	// 监听信号
	stopSignal := make(chan os.Signal, 1) //buffered：防信号发送时无人接收导致丢失（原为无缓冲，go vet 报 misuse）
	signal.Notify(stopSignal, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGQUIT)

	end := make(chan int)

	// 处理消息
	go func() {
		for  {
			select {
			case s := <- stopSignal:
				log.Infoln("stop signal:", s)
				pubsub.Close()

				end <- 1
			case msg := <- ch:
				log.Printf("当前节点：%d，消费到数据，channel:%s；message:%s\n", 1, msg.Channel, msg.Payload)
				var data enum.FundCollectionData
				err := json.Unmarshal([]byte(msg.Payload), &data)
				if err != nil {
					log.Errorf("消费: %s 失败: %s \n", msg.Payload, err)
					continue
				}
				//进行归集合
				if data.IsTron {
					//转trx
					trxId, err := tron.TransferTrxFee(config.TronPoolPrivateKey(), data.TronAddr)
					if err != nil {
						log.Errorln("补充交易费trx失败: ", trxId)
						continue
					}
					//等待交易费转账成功 50秒
					time.Sleep(50000)

					//转usdt
					trxId, err = tron.TransferUsdt(data.TronAddrPrivate, config.EtcConfig.TronPool.Address, data.Amount)
					if err != nil {
						log.Errorln("trx链转账失败: ", trxId)
						continue
					}
				} else {
					//转kto
					//转fibo
					_, hash, err := kto.KTOonChainSync(enum.KTO, config.EtcConfig.KtoPool.Address, config.KtoPoolPrivateKey(), data.KtoAddr, 22000000)
					if err != nil {
						log.Errorln("补充交易费kto失败: ", hash, ", from: ", config.EtcConfig.KtoPool.Address, ", to: ", data.KtoAddr, ", amount: ", data.Amount)
						continue
					}
					log.Errorln("上链交易费成功: ", hash)

					_, hash, err = kto.KTOonChainSync(data.Symbol, data.KtoAddr, data.KtoAddrPrivate, config.EtcConfig.KtoPool.Address, data.Amount)
					if err != nil {
						log.Errorln("kto链转帐失败: ", hash, ", from: ", data.KtoAddr, ", to: ", config.EtcConfig.KtoPool.Address, ", amount: ", data.Amount)
						continue
					}
				}
			}
		}
	}()

	<- end
}
