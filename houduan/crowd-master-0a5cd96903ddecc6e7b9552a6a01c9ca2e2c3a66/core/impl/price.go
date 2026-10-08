package impl

import (
	"com.fibonacci.crowd/config"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	log "github.com/sirupsen/logrus"
	"io/ioutil"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var FIBO20PriceMap = make(map[string]float64)
var httpClient = http.Client{}

type result struct {
	//Success bool              `json:"success"`
	Data map[string]interface{} `json:"data"`
}
type ktoUSDTResult struct {
	Data map[string]interface{} `json:"data"`
}

func RefreshFIBO20PricePrice() {
	go func() {
		for {
			select {
			case <-time.After(60 * time.Second):
				//读取所有token
				//查询价格
				//缓存到map
				for symbol, _ := range config.SymbolDictionary {
					if symbol == "FIBO" {
						prices := GetPriceVSMEXC(symbol)
						if prices != 0 {
							FIBO20PriceMap[symbol] = prices
							fmt.Println("价格已经加入了")
						}

					} else {
						price, err := getKtoSymbolUSDTPriceFallback(context.Background(), symbol)
						if err != nil {
							log.Error("getKtoSymbolUSDTPriceFallback error", err)
							price = 0.0
						}
						FIBO20PriceMap[symbol] = price
					}
				}
			}
		}
	}()
}

func GetKtoSymbolPrice(symbol string) float64 {
	if price, ok := FIBO20PriceMap[symbol]; ok {
		//get from cache
		return price
	} else {
		//fallback
		price, err := getKtoSymbolUSDTPriceFallback(context.Background(), symbol)
		if err != nil {
			return 0
		}
		return price
	}
}

func GetCny() float64 {
	client := http.Client{}
	url := "https://www.bcone.vip/api/currency/currency/getUsdtCny"
	body := "{}"
	res, err := client.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		return 6.42
	}
	defer res.Body.Close()
	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return 6.42
	}
	result := &result{}
	err = json.Unmarshal(b, result)
	if err != nil {
		return 6.42
	}
	price := result.Data["price"]
	return price.(float64)
}

// VSMEXC接口
func GetPriceVSMEXC(symbol string) float64 {
	resp, err := httpClient.Get("https://wallet.fibo.services/v1/priceMEXC?" + fmt.Sprintf("symbol=%v", symbol))
	if err != nil {
		fmt.Println("getPrice http: ", err.Error())
		return 0
	}
	respBs, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("getPrice read body: ", err.Error())
		return 0
	}

	var response map[string]interface{}
	err = json.Unmarshal(respBs, &response)
	if err != nil {
		fmt.Println("getPrice parse body json: ", err.Error())
		return 0
	}
	data, ok := response["data"].(float64)
	if !ok {
		return 0
	}
	priceBig := big.NewFloat(data)
	price, _ := priceBig.Float64()
	return price
}

// TMFixedUSDTPrice TM 暂定固定价（USDT 计价）。
// 依据：TiMi 规则⑧ TM 加速单价 3.4U/枚；上线前若 TM 接入真实行情，应移除此兜底改走交易所 ticker。
const TMFixedUSDTPrice = 3.4

func getKtoSymbolUSDTPriceFallback(ctx context.Context, symbol string) (float64, error) {
	// TM 为手续费币，暂无交易所行情，按固定价 3.4U 兜底（防提币手续费估价除零/Inf）
	if symbol == "TM" {
		return TMFixedUSDTPrice, nil
	}
	if symbol == "FUSD" {
		return 1, nil
	}
	if symbol == "DOX" {
		return 1, nil
	}
	if symbol == "USDT" {
		return 1, nil
	}

	var USDTPrice float64
	url := "https://www.bcone.vip/api/market/tickers/ticker?symbol=usdt_" + strings.ToLower(symbol)
	reqs, _ := http.NewRequest("GET", url, nil)
	reqs.WithContext(ctx)
	res, err := httpClient.Do(reqs)
	if err != nil {
		return 0.00, errors.New("GetUsdt request error")
	}
	defer res.Body.Close()
	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return 0.00, errors.New("GGetUsdt read all error")
	}
	result := &ktoUSDTResult{}
	err = json.Unmarshal(b, result)
	if err != nil {
		return 0.00, errors.New("GetUsdt unmarshal error")
	}
	if price, ok := result.Data["Last"].(string); ok {
		USDTPrice, err = strconv.ParseFloat(price, 64)
		if err != nil {
			return 0.00, errors.New("GetUsdt parseFloat error")

		}
	}
	return USDTPrice, nil
}
