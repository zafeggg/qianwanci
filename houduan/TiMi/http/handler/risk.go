package handler

import (
	"context"
	"strconv"
	"sync"
	"time"

	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core/impl"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils"
	"com.fibonacci.crowd/utils/metrics"

	"github.com/gofiber/fiber/v2"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// ============================== D 模块：风控 ==============================
// 三件事：① 限流 ② 黑/白名单 ③ 提现额度。
// 阈值集中在 core/impl/config.go 的「风控」段（0 = 不限制）。
//
// ⚠ 提现额度三项默认全为 0（不限制）——机制已就绪，**数值必须由 owner 拍板**后再启用。
// 注：本文件不使用 c.JSON()，原因见 observability.go 顶部说明。

const (
	metricRateLimited = "npower_risk_ratelimited_total"
	metricRiskBlocked = "npower_risk_address_blocked_total"
)

func isWriteMethod(m string) bool {
	switch m {
	case fiber.MethodPost, fiber.MethodPut, fiber.MethodPatch, fiber.MethodDelete:
		return true
	}
	return false
}

// ---------------- ① 限流（固定窗口）----------------
// 优先用 Redis（多实例共享计数）；Redis 不可用时退化为进程内计数（单实例仍有效）。
// 固定窗口而非滑动窗口：实现简单、内存/键数量可控，对「防脚本刷接口」足够。

var (
	localRLMu sync.Mutex
	localRL   = map[string]int{}
)

func rateLimitAllowRedis(key string, limit int) (bool, int) {
	ctx := context.Background()
	n, err := config.RedisClient.Incr(ctx, key).Result()
	if err != nil {
		return true, limit //Redis 异常不阻断业务
	}
	if n == 1 {
		config.RedisClient.Expire(ctx, key, 70*time.Second)
	}
	remaining := limit - int(n)
	if remaining < 0 {
		remaining = 0
	}
	return int(n) <= limit, remaining
}

func rateLimitAllowLocal(key string, limit int) (bool, int) {
	localRLMu.Lock()
	defer localRLMu.Unlock()
	//粗粒度清理：条目过多时整体重置，避免无界增长（限流器本身是防滥用，不需要精确）
	if len(localRL) > 10000 {
		localRL = map[string]int{}
	}
	localRL[key]++
	n := localRL[key]
	remaining := limit - n
	if remaining < 0 {
		remaining = 0
	}
	return n <= limit, remaining
}

// RateLimitMiddleware 单 IP 限流（读/写分别计数）。
// 挂载点见 cmd/main.go：全局挂载，/health 与 /metrics 豁免（探针不能被限流）。
func RateLimitMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if c.Method() == fiber.MethodOptions {
			return c.Next()
		}
		path := c.Path()
		if path == "/health" || path == "/metrics" {
			return c.Next()
		}

		scope, limit := "read", impl.RequestRatePerMinute
		if isWriteMethod(c.Method()) {
			scope, limit = "write", impl.WriteRatePerMinute
		}
		if limit <= 0 {
			return c.Next()
		}

		bucket := time.Now().Unix() / 60
		key := "npower:rl:" + scope + ":" + c.IP() + ":" + strconv.FormatInt(bucket, 10)

		var ok bool
		var remaining int
		if config.RedisClient != nil {
			ok, remaining = rateLimitAllowRedis(key, limit)
		} else {
			ok, remaining = rateLimitAllowLocal(key, limit)
		}

		c.Set("X-RateLimit-Limit", strconv.Itoa(limit))
		c.Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		if ok {
			return c.Next()
		}

		metrics.Inc(metricRateLimited, map[string]string{"scope": scope})
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
		c.Set("Retry-After", strconv.Itoa(60-int(time.Now().Unix()%60)))
		return c.Status(fiber.StatusTooManyRequests).
			Send(utils.JsonFailCode(429, "请求过于频繁，请稍后再试"))
	}
}

// ---------------- ② 黑/白名单 ----------------

// riskAddressOf 取本次写请求涉及的地址。
// 会话身份优先：会话票是**密码学证明过**的地址，而 body 里的 address 只是客户端自述。
// 原实现只读 body，改一下 body 就能拿别人的地址绕过（或伪造进）黑/白名单；
// 无会话时（EnforceSession=false 的存量前端/脚本直连）仍回落到 body，保持兼容。
func riskAddressOf(c *fiber.Ctx) string {
	if sess := sessionAddress(c); sess != "" {
		return sess
	}
	var m map[string]interface{}
	if err := c.BodyParser(&m); err == nil {
		if a, ok := m["address"].(string); ok {
			return a
		}
	}
	return ""
}

// RiskCheckAddress 名单校验：命中黑名单直接拒绝；开启白名单时非白名单地址拒绝。
// 返回 true 表示放行；false 表示已写入拒绝响应（调用方直接 return nil）。
func RiskCheckAddress(c *fiber.Ctx) bool {
	if !impl.BlacklistEnabled && !impl.WhitelistEnabled {
		return true
	}
	addr := riskAddressOf(c)
	if addr == "" {
		return true
	}
	if impl.BlacklistEnabled && model.IsListed(model.RiskKindBlacklist, addr) {
		metrics.Inc(metricRiskBlocked, map[string]string{"kind": "blacklist"})
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
		_ = c.Status(fiber.StatusForbidden).
			Send(utils.JsonFailCode(403, "该地址已被限制，如有疑问请联系客服"))
		return false
	}
	if impl.WhitelistEnabled && !model.IsListed(model.RiskKindWhitelist, addr) {
		metrics.Inc(metricRiskBlocked, map[string]string{"kind": "whitelist"})
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
		_ = c.Status(fiber.StatusForbidden).
			Send(utils.JsonFailCode(403, "当前处于白名单期，该地址暂不可用"))
		return false
	}
	return true
}

// RiskGuardMiddleware 把名单校验挂到写请求上（GET 不校验，读接口不影响）
func RiskGuardMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if !isWriteMethod(c.Method()) {
			return c.Next()
		}
		//鉴权与运维接口不参与名单（否则无法封停/解封）
		p := c.Path()
		if len(p) >= 9 && p[:9] == "/api/auth" {
			return c.Next()
		}
		if len(p) >= 5 && p[:5] == "/ops/" {
			return c.Next()
		}
		if !RiskCheckAddress(c) {
			return nil
		}
		return c.Next()
	}
}

// ---------------- ③ 提现额度 ----------------

// CheckWithdrawLimits 检查提现额度（0 = 不限制）。返回空串表示通过，否则为拒绝原因。
//
// 口径说明：以 wallet_tx 中 Type=BlockOut（区块链出账）且 from=用户地址 的记录为准，
// 统计「当日累计金额」与「当日笔数」；单笔上限直接比传入的 amount（主单位）。
// 若上线时 wallet_tx 出账语义调整（例如改为 to=用户地址），此处需同步。
//
// ⚠ 2026-09-30 修复的换算缺陷：原实现用**固定** scale = SymbolDictionary[FeeSymbol]（TM=1e8）
// 去除所有流水，而 wallet_tx.amount 存的是**各币种自己的最小单位**（USDT 1e6 / KTO 1e12），
// 于是提 USDT 时日累计被算成实际的 1/100、KTO 差 1e4 倍 → owner 定值后上限完全不是预期值。
// 现在按 symbol 分组、用各自精度换算。
//
// 残留说明（不改变语义，仅登记）：不同币种的"主单位"金额仍直接相加（阈值参数按主单位计价），
// 跨币种折算成 USD 会改变 owner 已确认的参数语义，故不擅自改；如需严格口径请改为 USD 计价阈值。
func CheckWithdrawLimits(address string, amount float64) string {
	return checkWithdrawLimits(config.MysqlDBPool, address, amount)
}

// CheckWithdrawLimitsTx 事务内版本：把额度校验放进扣账事务，避免并发/重放绕过日累计与日笔数。
func CheckWithdrawLimitsTx(tx *gorm.DB, address string, amount float64) string {
	return checkWithdrawLimits(tx, address, amount)
}

func checkWithdrawLimits(db *gorm.DB, address string, amount float64) string {
	if address == "" {
		return ""
	}
	// ⚠ 2026-09-30：**单笔上限的校验不依赖 DB**，放在最前面。
	//   原实现开头是 `if address == "" || db == nil { return "" }` ——
	//   DB 池未初始化/异常时会**静默跳过单笔上限**（fail-open）。
	//   单笔上限只是个数值比较、本来不需要 DB，因此这里把它提到 DB 判断之前。
	if impl.WithdrawSingleMax > 0 && amount > impl.WithdrawSingleMax {
		return "单笔提现超过上限 " + strconv.FormatFloat(impl.WithdrawSingleMax, 'f', -1, 64)
	}
	if impl.WithdrawDailyMax <= 0 && impl.WithdrawDailyCountMax <= 0 {
		//owner 2026-09-30 裁定：单日累计与笔数**不限制**（默认 0）→ 直接放行，连统计都不查
		return ""
	}
	if db == nil {
		//日累计/日笔数必须查账本，DB 不可用时按"不限制"放行（与既有口径一致，并有 WARN 可查）
		log.Warnln("[risk] 提现日累计校验需要数据库，但 DB 池不可用，本次按不限制放行")
		return ""
	}
	if impl.WithdrawDailyMax > 0 || impl.WithdrawDailyCountMax > 0 {
		//按币种分别汇总，再各自换算成主单位（wallet_tx.amount 存各币种最小单位）
		var rows []struct {
			Symbol string
			Total  float64
			Cnt    int
		}
		if err := db.Raw(
			"SELECT `symbol` AS symbol, COALESCE(SUM(`amount`),0) AS total, COUNT(*) AS cnt FROM wallet_tx "+
				"WHERE `from` = ? AND `type` = ? AND `created_at` >= CURDATE() GROUP BY `symbol`",
			address, enum.BlockOut).Scan(&rows).Error; err != nil {
			log.WithFields(log.Fields{"err": err, "address": address}).Warnln("[risk] 提现额度统计失败，按不限制放行")
			return ""
		}
		var todayMain float64
		var todayCnt int
		for _, r := range rows {
			scale := float64(config.SymbolDictionary[r.Symbol])
			if scale <= 0 {
				scale = 1
			}
			todayMain += r.Total / scale
			todayCnt += r.Cnt
		}
		if impl.WithdrawDailyCountMax > 0 && todayCnt+1 > impl.WithdrawDailyCountMax {
			return "今日提现笔数已达上限 " + strconv.Itoa(impl.WithdrawDailyCountMax)
		}
		if impl.WithdrawDailyMax > 0 && todayMain+amount > impl.WithdrawDailyMax {
			return "今日提现累计已达上限 " + strconv.FormatFloat(impl.WithdrawDailyMax, 'f', -1, 64)
		}
	}
	return ""
}
