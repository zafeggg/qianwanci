package main

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core"
	"com.fibonacci.crowd/core/impl"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils"
	"crypto/subtle"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/panjf2000/ants/v2"
	log "github.com/sirupsen/logrus"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var client = http.Client{
	Timeout: 15 * time.Second,
}

// callbackWarnOnce 未配置 Callback.Secret 时只告警一次
var callbackWarnOnce sync.Once

//Coin collection to address
func main() {
	path := flag.String("f", "/Users/zhengjianfeng_1/go/src/crowd/config/trs.yml", "-f 指定配置文件")
	flag.Parse()
	config.Init(*path) //mysql, redis, config file

	antPools, err := ants.NewPool(10000)
	if err != nil {
		panic(fmt.Sprintf("init ant pools err: %v", err))
	}

	var walletManager = impl.NewWalletManager()
	// 监听信号
	stopSignal := make(chan os.Signal, 1) //buffered：防信号发送时无人接收导致丢失（原为无缓冲，go vet 报 misuse）
	signal.Notify(stopSignal, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGQUIT)


	antPools2, err := ants.NewPool(5000)
	if err != nil {
		panic(fmt.Sprintf("init ant pools err: %v", err))
	}

	app := fiber.New()
	app.Use(recover.New())
	app.Use(logger.New())
	app.Post("/callback", func(c *fiber.Ctx) error {
		//复盘 Wave-3：可选鉴权——yml Callback.Secret 非空时要求请求头 X-Callback-Key 一致；
		//未配置时保持兼容（仅首次启动 Warn 一次提示不安全）。
		callbackSecret := config.EtcConfig.Callback.Secret
		if callbackSecret != "" {
			if subtle.ConstantTimeCompare([]byte(c.Get("X-Callback-Key")), []byte(callbackSecret)) != 1 {
				return c.Send(utils.JsonFail("回调密钥错误"))
			}
		} else {
			callbackWarnOnce.Do(func() {
				log.Warnln("充值回调未配置 Callback.Secret，任何来源都可触发入账（不安全，请配置后重启）")
			})
		}

		var request fiber.Map
		if err := c.BodyParser(&request); err != nil {
			return c.Send(utils.JsonFail("参数错误"))
		}

		log.Infoln("heart beat callback", request)

		//安全断言：字段缺失/类型不符直接拒绝，避免裸断言 panic
		if request["from"] == nil || request["to"] == nil || request["amount"] == nil || request["hash"] == nil {
			return c.Send(utils.JsonFail("参数不完整"))
		}
		from, ok1 := request["from"].(string)
		to, ok2 := request["to"].(string)
		amount, ok3 := request["amount"].(float64)
		hash, ok4 := request["hash"].(string)
		if !ok1 || !ok2 || !ok3 || !ok4 || from == "" || to == "" || hash == "" {
			return c.Send(utils.JsonFail("参数类型错误"))
		}

		err = antPools2.Submit(func() {
			var wallet model.Wallet
			if err = config.MysqlDBPool.Table(model.WalletTable).First(&wallet, "`tron_address` = ?", to).Error; err != nil {
				return
			}
			err = walletManager.Charge(wallet, from, enum.USDT, uint64(amount), hash)
			if err != nil {
				log.Errorln("callback charge wallet error", err, request)
				return
			}
		})

		if err != nil {
			return c.Send(utils.JsonFail(err.Error()))
		}

		return c.Send(utils.JsonOk("处理中"))
	})

	go func() {
		log.Fatal(app.Listen(config.EtcConfig.HttpPort))
	}()


	var stopWaiting = 5 * time.Second
	var timeSearch = 2 * 60 * time.Second
	end := make(chan int)
	go func() {
		for {
			select {
			case s := <-stopSignal:
				log.Infoln("stop signal:", s)
				for antPools.Running() > 0 {
					log.Infoln("waiting stop ants pools running: ", antPools.Running(), ", free:", antPools.Free())
					time.Sleep(stopWaiting)
				}

				log.Infoln("end ants pools running: ", antPools.Running(), ", free:", antPools.Free())
				end <- 1
			case <-time.After(timeSearch):
				//search all wallet
				var wallets []model.Wallet
				if err = config.MysqlDBPool.Table(model.WalletTable).Find(&wallets).Error; err != nil {
					log.Errorln("search all wallets err: ", err)
					return
				}

				log.Infof("total wallets length: %v", len(wallets))

				var walletHashes []model.WalletHashCharge
				if err = config.MysqlDBPool.Table(model.WalletHashChargeTable).Find(&walletHashes).Error; err != nil {
					log.WithFields(log.Fields{"method": "find local localHashes"}).Errorln(err)
					return
				}

				walletHashesMap := make(map[string]map[string]bool)
				for _, walletHash := range walletHashes {
					if _, ok := walletHashesMap[walletHash.Address]; !ok {
						walletHashesMap[walletHash.Address] = make(map[string]bool)
						walletHashesMap[walletHash.Address][walletHash.Hash] = true
					}else{
						walletHashesMap[walletHash.Address][walletHash.Hash] = true
					}
				}

				err = antPools.Submit(func() {
					for _, wallet := range wallets {
						address := wallet.Address
						localHashes := walletHashesMap[address]

						trxAddress := wallet.TronAddress
						handleTronChain(walletManager, wallet, trxAddress, localHashes)

						remoteHashes := make(map[string]Transaction)
						var total = 101
						var page = 1
						var limit = 100
						for total > page*limit {
							request := "https://www.kortho.io/server/GetTxsByAddr?address=" + address + "&limit=" + strconv.Itoa(limit) + "&page=" + strconv.Itoa(page)
							response, err := client.Get(request)
							if err != nil {
								log.WithFields(log.Fields{"method": "client get kto addr"}).Errorln("get request", err)
								return
							}
							if response.StatusCode != 200 {
								log.WithFields(log.Fields{"method": "client get kto addr"}).Errorln("status code", response.StatusCode)
								return
							}

							bs, err := ioutil.ReadAll(response.Body)
							if err != nil {
								log.WithFields(log.Fields{"method": "client get kto addr"}).Errorln("read body", err)
								return
							}
							var getByAddr GetTxsByAddr
							err = json.Unmarshal(bs, &getByAddr)
							if err != nil {
								log.WithFields(log.Fields{"method": "client get kto addr"}).Errorln("unmarshal body", err)
								return
							}
							transactionlist := getByAddr.Transactionlist
							for _, transaction := range transactionlist {
								if transaction.To == address {
									//转入
									remoteHashes[transaction.Hash] = transaction
								}
							}

							total = getByAddr.Total
							page++
						}

						for hash, transaction := range remoteHashes {
							if _, ok := localHashes[hash]; !ok {
								from := transaction.From
								symbol := transaction.TokenName

								var amount uint64
								if symbol == "KTO" {
									amount = uint64(transaction.Amount)
									if amount == 22000000 {
										//内部交易费补贴 不处理
										continue
									}
								}else{
									script := transaction.Script
									split := strings.Split(script, "\"")
									amountStr := split[2]
									amountScript, err :=  strconv.Atoi(strings.TrimSpace(amountStr))
									if err != nil {
										amount = 0
										log.WithFields(log.Fields{"Symbol": symbol}).Errorln("parse script amount error", err)
									}else{
										amount = uint64(amountScript)
									}
								}
								log.WithFields(log.Fields{"address": address, "from": from, "symbol": symbol, "amount": amount, "hash": hash}).Infoln("hash为新, 准备为账户地址充值")
								err = walletManager.Charge(wallet, from, symbol, amount, hash)
								if err != nil {
									log.WithFields(log.Fields{"address": address, "from": from, "symbol": symbol, "amount": amount, "hash": hash}).Errorln("充值失败", err)
								}
							}
						}
					}

				})

				if err != nil {
					log.Errorln("submit ant poos error", err)
				}

				log.Infoln("ants pools running: ", antPools.Running(), ", free:", antPools.Free())
			}
		}

	}()

	<-end
}

func handleTronChain(walletManager core.OnBoarding, wallet model.Wallet, address string, localHashes map[string]bool) {
	confirmReq := fmt.Sprintf("https://api.trongrid.io/v1/accounts/"+address+"/transactions/trc20?limit=10&contract_address=TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t&only_unconfirmed=false")
	resp, err := client.Get(confirmReq)
	if err != nil {
		log.Errorln("query accounts transactions error", err)
		return
	}
	bs, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Errorln("read accounts transactions body error", err)
		return
	}
	var result fiber.Map
	err = json.Unmarshal(bs, &result)
	if err != nil {
		log.Errorln("unmarshal body error", err)
		return
	}
	confirmedList, ok := result["data"].([]interface {})
	if !ok {
		return
	}

	remoteHashs := make(map[string]map[string]interface {})
	for _, confirmed := range confirmedList {
		resp := confirmed.(map[string]interface {})
		hash := resp["transaction_id"].(string)
		remoteHashs[hash] = resp
	}

	for hash, transaction := range remoteHashs {
		if _, ok := localHashes[hash]; !ok {
			from := transaction["from"].(string)
			value := transaction["value"].(string)
			trxInfo := transaction["token_info"].(map[string]interface {})
			symbol := trxInfo["symbol"].(string)

			amount, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				log.Errorln("tron transaction error value",transaction)
				return
			}

			log.WithFields(log.Fields{"address": address, "from": from, "symbol": symbol, "amount": amount, "hash": hash}).Infoln("hash为新, 准备为账户地址充值USDT")
			err = walletManager.Charge(wallet, from, symbol, amount, hash)
			if err != nil {
				log.WithFields(log.Fields{"address": address, "from": from, "symbol": symbol, "amount": amount, "hash": hash}).Errorln("充值失败", err)
			}
		}
	}
}

type GetTxsByAddr struct {
	Code            int           `json:"code"`
	Message         string        `json:"message"`
	Total           int           `json:"total"`
	Transactionlist []Transaction `json:"transactionlist"`
}

type Transaction struct {
	Nonce       int    `json:"nonce"`
	Blocknumber int    `json:"blocknumber"`
	Amount      int    `json:"amount"`
	From        string `json:"from"`
	To          string `json:"to"`
	Hash        string `json:"hash"`
	Signature   string `json:"signature"`
	Time        string `json:"time"`
	Script      string `json:"script"`
	Ord         struct {
		Id         string `json:"id"`
		Address    string `json:"address"`
		Price      int    `json:"price"`
		Hash       string `json:"hash"`
		Signature  string `json:"signature"`
		Ciphertext string `json:"ciphertext"`
		Tradename  string `json:"tradename"`
		Region     string `json:"region"`
	} `json:"ord"`
	Ktonum    int    `json:"ktonum"`
	Pcknum    int    `json:"pcknum"`
	Tag       int    `json:"tag"`
	Fee       int    `json:"fee"`
	TokenName string `json:"tokenName"`
	Decimals  int    `json:"decimals"`
	Status    bool   `json:"status"`
	Comment   string `json:"comment"`
}
