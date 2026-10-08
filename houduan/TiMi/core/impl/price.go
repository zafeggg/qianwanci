package impl

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/enum"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	log "github.com/sirupsen/logrus"
	"io/ioutil"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
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
						if err != nil || price <= 0 {
							//复盘 Wave-4：行情失败不缓存 0，让 GetKtoSymbolPrice 回源实时获取/静态价
							if err != nil {
								log.WithFields(log.Fields{"symbol": symbol}).Debugln("行情刷新失败，跳过缓存", err)
							}
							delete(FIBO20PriceMap, symbol)
							continue
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
		RecordPriceSample(symbol, price)
		return price
	} else {
		//fallback
		price, err := getKtoSymbolUSDTPriceFallback(context.Background(), symbol)
		if err != nil {
			return 0
		}
		RecordPriceSample(symbol, price)
		return price
	}
}

/* ==================== 4 小时均价（需求#8：算力保底按"4小时均价"折算） ====================
 *
 * 需求原文：模式 1/2 的每日最低产出 = (算力 × 3) / 300 / **4小时均价**。
 * 原实现直接取实时价近似，行情抖动会直接改变"保底产出"（用户当天看到的保底忽高忽低）。
 *
 * 实现方式（明确的近似，不冒充交易所 K 线）：
 *   - 每取到一次行情就把 (时间戳, 价格) 记入 Redis ZSET（`npower:price:samples:<symbol>`），
 *     进程内按 60s/币种节流，避免热点路径（如逐笔结算）把 Redis 打满；
 *   - 计算时取**最近 4 小时**的样本均值；
 *   - 样本数不足（< 3 条，例如刚部署）→ 回落实时价，并把来源标成 "spot"，
 *     接口与日志都会带上来源，避免"看着是均价、其实是实时价"的误导。
 *   - 常驻采样由 StartPriceSampler 负责（npower 启动时挂 10 分钟一次的 ticker），
 *     这样即使没有用户请求，样本也会持续累积。
 */

const (
	priceSampleKeyPrefix = "npower:price:samples:"
	priceWindowSeconds   = int64(4 * 3600) //4 小时窗口
	priceMinSamples      = 3               //低于该样本数视为"样本不足"，回落实时价
	priceSampleThrottle  = 60 * time.Second
	priceSampleKeepSecs  = int64(6 * 3600) //只保留 6 小时样本
)

// PriceSample 一次行情采样
type PriceSample struct {
	At    int64   //Unix 秒
	Price float64
}

// meanPriceSamples 纯函数：取窗口内样本均值。
// 过滤规则：时间超出窗口的丢掉；价格 <=0 或非有限值的丢掉（行情源异常时不能污染均价）。
// 返回 ok=false 表示有效样本不足（调用方应回落实时价）。
func meanPriceSamples(samples []PriceSample, now int64, window int64, minSamples int) (float64, bool) {
	var sum float64
	var n int
	for _, s := range samples {
		if s.At < now-window || s.At > now {
			continue
		}
		if !(s.Price > 0) || math.IsInf(s.Price, 0) || math.IsNaN(s.Price) {
			continue
		}
		sum += s.Price
		n++
	}
	if n < minSamples {
		return 0, false
	}
	return sum / float64(n), true
}

var (
	priceSampleLastAt sync.Map //symbol -> int64(unix 秒)：进程内节流
)

// RecordPriceSample 记录一次行情采样（节流 + 只保留近 6 小时）。
// Redis 不可用时静默跳过：价格历史是"增强"，不能让主流程失败。
func RecordPriceSample(symbol string, price float64) {
	if config.RedisClient == nil || symbol == "" || !(price > 0) {
		return
	}
	now := time.Now().Unix()
	if v, ok := priceSampleLastAt.Load(symbol); ok {
		if last, _ := v.(int64); now-last < int64(priceSampleThrottle.Seconds()) {
			return
		}
	}
	priceSampleLastAt.Store(symbol, now)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		key := priceSampleKeyPrefix + symbol
		member := fmt.Sprintf("%d:%.10f", now, price)
		if err := config.RedisClient.ZAdd(ctx, key, &redis.Z{Score: float64(now), Member: member}).Err(); err != nil {
			log.WithFields(log.Fields{"symbol": symbol, "err": err}).Debugln("[price] 记录行情采样失败（忽略）")
			return
		}
		//清理过期样本（按 score 删窗口外的）
		config.RedisClient.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(now-priceSampleKeepSecs, 10))
		config.RedisClient.Expire(ctx, key, time.Duration(priceSampleKeepSecs+3600)*time.Second)
	}()
}

// loadPriceSamples 读取窗口内样本（按 score 区间取，再解析 member "ts:price"）
func loadPriceSamples(symbol string) []PriceSample {
	if config.RedisClient == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	now := time.Now().Unix()
	key := priceSampleKeyPrefix + symbol
	vals, err := config.RedisClient.ZRangeByScore(ctx, key, &redis.ZRangeBy{
		Min: strconv.FormatInt(now-priceWindowSeconds, 10),
		Max: strconv.FormatInt(now, 10),
	}).Result()
	if err != nil {
		log.WithFields(log.Fields{"symbol": symbol, "err": err}).Debugln("[price] 读取行情样本失败（回落实时价）")
		return nil
	}
	out := make([]PriceSample, 0, len(vals))
	for _, v := range vals {
		i := strings.LastIndex(v, ":")
		if i <= 0 {
			continue
		}
		at, aerr := strconv.ParseInt(v[:i], 10, 64)
		p, perr := strconv.ParseFloat(v[i+1:], 64)
		if aerr != nil || perr != nil {
			continue
		}
		out = append(out, PriceSample{At: at, Price: p})
	}
	return out
}

// Price4hWithSource 返回 (币价, 来源)：来源为 "avg4h"（4 小时均价）或 "spot"（实时价/降级）。
// 4 小时均价主要用于算力保底口径（需求#8）；手续费换算等仍用实时价。
func Price4hWithSource(symbol string) (float64, string) {
	spot := GetKtoSymbolPrice(symbol)
	if avg, ok := meanPriceSamples(loadPriceSamples(symbol), time.Now().Unix(), priceWindowSeconds, priceMinSamples); ok {
		return avg, "avg4h"
	}
	return spot, "spot"
}

// GetSymbolPrice4h 4 小时均价（样本不足时即实时价）
func GetSymbolPrice4h(symbol string) float64 {
	p, _ := Price4hWithSource(symbol)
	return p
}

// StartPriceSampler 常驻采样：每 interval 抓一次给定币种的行情并记录样本。
// 由 npower 启动时调用（这样即便没有用户请求，均价也有样本可用）。
func StartPriceSampler(interval time.Duration, symbols ...string) {
	if len(symbols) == 0 {
		return
	}
	go func() {
		//启动先采一次，缩短"上线后前几分钟没有均价"的窗口
		for _, s := range symbols {
			RecordPriceSample(s, GetKtoSymbolPrice(s))
		}
		t := time.NewTicker(interval)
		defer t.Stop()
		for range t.C {
			for _, s := range symbols {
				RecordPriceSample(s, GetKtoSymbolPrice(s))
			}
		}
	}()
	log.WithFields(log.Fields{"interval": interval.String(), "symbols": symbols}).Infoln("[price] 4 小时均价采样器已启动")
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
	//复盘 Wave-3：外部接口字段类型不可信，改用类型安全取值，杜绝裸断言 panic
	switch p := price.(type) {
	case float64:
		if p > 0 {
			return p
		}
	case string:
		if f, perr := strconv.ParseFloat(p, 64); perr == nil && f > 0 {
			return f
		}
	}
	return 6.42
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

func getKtoSymbolUSDTPriceFallback(ctx context.Context, symbol string) (float64, error) {
	if symbol == "FUSD" {
		return 1, nil
	}
	if symbol == "DOX" {
		return 1, nil
	}
	if symbol == "USDT" {
		return 1, nil
	}
	if symbol == "TM" {
		return 3.4, nil //N次方：TM 暂定 3.4U/枚（技术方案 1.6 / 需求#9），静态价兜底
	}
	//N次方：FIBO 静态兜底价（默认 0 = 关闭）。
	//本函数只在 FIBO20PriceMap 缓存未命中（即行情源取价失败）时才被调用，
	//所以有实时行情时静态价不会覆盖真实价。见 core/impl/config.go#FiboStaticPrice。
	if symbol == enum.FIBO && FiboStaticPrice > 0 {
		return FiboStaticPrice, nil
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
