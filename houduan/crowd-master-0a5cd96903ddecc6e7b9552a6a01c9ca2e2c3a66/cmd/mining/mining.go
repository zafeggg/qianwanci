package main

import (
	"bytes"
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core/impl"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/robfig/cron/v3"
	log "github.com/sirupsen/logrus"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)


var httpClient = http.Client{}

//Small mining pool reward
func main() {

	path := flag.String("f", "/Users/zhengjianfeng_1/go/src/crowd/config/mining.yml", "-f 指定配置文件")
	fiboNetwork := flag.String("net", "http://154.85.45.39:6000", "-fibo 指定斐波主网")
	flag.Parse()

	config.Init(*path) //mysql, redis, config file

	rewardDailyFibo := 3498542274052 * 0.425
	cronNew := cron.New(cron.WithSeconds(), cron.WithLogger(cron.VerbosePrintfLogger(log.StandardLogger())))
	miningManager := impl.NewMiningManager()


	_, err := cronNew.AddFunc(config.EtcConfig.MiningJob.SyncPcb, func() {
		//同步pcb 到FIBO30
		var exchanges []model.MiningExchange
		if err := config.MysqlDBPool.Table(model.MiningExchangeTable).Find(&exchanges, "`status` = ?", 0).Error; err != nil {
			log.WithFields(log.Fields{"MiningExchange": "查询所有产矿兑换记录失败"}).Errorln(err)
			return
		}

		var poolPcb uint64
		for _, exchange := range exchanges {
			poolPcb += uint64(exchange.Pcb * config.SymbolDictionary[enum.PCB])
		}

		var request = make(fiber.Map)
		request["pcb"] = poolPcb
		jsonReq, err := json.Marshal(request)
		if err != nil {
			log.Errorln("marshal json request err", err)
			return
		}

		response, err := httpClient.Post(*fiboNetwork + "/syncCrowdPoolPcb", "application/json", bytes.NewBuffer(jsonReq))
		if err != nil {
			log.Errorln("post total mining pcb err", err)
			return
		}
		if response.StatusCode != 200 {
			log.Errorln("post total mining pcb err", err)
			return
		}

		log.Infoln("同步主网: ", *fiboNetwork, "众筹矿池pcb:", poolPcb, "响应:", response.Body)

	})
	if err != nil {
		panic(fmt.Sprintf("注册同步pcb任务失败: %v", err))
	}



	_, err = cronNew.AddFunc(config.EtcConfig.MiningJob.MiningPcb, func() {
		response, err := httpClient.Get(*fiboNetwork + "/totalMiningPcb")
		if err != nil {
			log.Errorln("get total mining pcb err", err)
			return
		}
		if response.StatusCode != 200 {
			log.Errorln("get total mining pcb status err", response.StatusCode)
			return
		}
		bs, err := ioutil.ReadAll(response.Body)
		if err != nil {
			log.Errorln("get total mining pcb read all err", err)
			return
		}

		var resp  fiber.Map
		err = json.Unmarshal(bs, &resp)
		if err != nil {
			log.Errorln("get total mining pcb unmarshal err", err)
			return
		}

		pcb := resp["data"].(float64)
		log.Infoln("get total mining pcb: ", pcb)

		totalPoolPcb := uint64(pcb)

		var exchanges []model.MiningExchange
		if err = config.MysqlDBPool.Table(model.MiningExchangeTable).Find(&exchanges, "`status` = ?", 0).Error; err != nil {
			log.WithFields(log.Fields{"MiningExchange": "查询所有产矿兑换记录失败"}).Errorln(err)
			return
		}

		var poolPcb uint64
		for _, exchange := range exchanges {
			poolPcb += uint64(exchange.Pcb * config.SymbolDictionary[enum.PCB])
		}

		for _, exchange := range exchanges {
			exPcb := uint64(exchange.Pcb * config.SymbolDictionary[enum.PCB])

			poolAmount := rewardDailyFibo * (float64(poolPcb) / float64(totalPoolPcb))
			amount := poolAmount * (float64(exPcb) / float64(poolPcb))
			reward := utils.FixedFloat2(amount / config.SymbolDictionary[enum.FIBO])

			log.Infoln("rewardDailyFibo: ", rewardDailyFibo, ", exPcb: ", exPcb, ", poolPcb", poolPcb, ", totalMiningPoolPcb", totalPoolPcb)
			log.Infoln("poolAmount: ", poolAmount, ", exAmount", amount, ", reward", reward)

			if err = miningManager.Reward(exchange.ID, reward); err != nil {
				log.WithFields(log.Fields{"MiningExchangeId": exchange.ID}).Errorln("记录产币记录失败", err)
			}
		}
	})
	if err != nil {
		panic(fmt.Sprintf("注册计算定时任务失败: %v", err))
	}

	cronNew.Start()
	defer cronNew.Stop()


	defer func() {
		if err := recover(); err != nil {
			fmt.Println("recover success.", err)
		}
	}()

	stopSignal := make(chan os.Signal)
	// 监听信号
	signal.Notify(stopSignal, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGQUIT)

	<-stopSignal
}
