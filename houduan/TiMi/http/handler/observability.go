package handler

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/utils"
	"com.fibonacci.crowd/utils/kto"
	"com.fibonacci.crowd/utils/metrics"

	"github.com/gofiber/fiber/v2"
)

// ============================== F 模块：可观测性 ==============================
// 提供 GET /health（存活/依赖探活）与 GET /metrics（Prometheus 文本格式），
// 以及 HTTP 访问指标中间件。二者均不鉴权（供探针 / Prometheus 抓取），
// 生产建议在 Nginx 层限制 /metrics 的来源 IP。
//
// 指标命名统一 npower_ 前缀；进程内聚合，重启清零。
//
// ⚠ 本文件禁止使用 c.JSON() / c.Status().JSON()：
//   fiber v2.20.2 内置的 internal/go-json 通过 linkname 调 runtime.mapiterinit，
//   在 Go 1.24+（Swiss map）下会触发 "unexpected fault address / fatal error: fault" 直接崩进程。
//   本仓库既有代码一律走 utils.JsonOk(...) + c.Send(...)（encoding/json 预序列化），本文件沿用该约定。

const (
	MetricHTTPRequests = "npower_http_requests_total"
	MetricHTTPDuration = "npower_http_request_duration_seconds"
	MetricDBUp         = "npower_db_up"
	MetricRedisUp      = "npower_redis_up"
	MetricChainUp      = "npower_chain_up"
	MetricUptime       = "npower_uptime_seconds"
	MetricUp           = "npower_up"

	// chainProbeTimeout 链节点探测超时（仅后台采集器与 /health 使用）
	chainProbeTimeout = 3 * time.Second
	// depProbeTimeout MySQL/Redis 探测超时（快）
	depProbeTimeout = 800 * time.Millisecond
)

// healthData /health 响应体（显式结构体，避免走 fiber 的 JSON 编码器）
type healthData struct {
	Status        string            `json:"status"` // ok | degraded | down
	UptimeSeconds int64             `json:"uptimeSeconds"`
	Checks        map[string]string `json:"checks"`
	Time          string            `json:"time"`
}

type healthResp struct {
	Code int        `json:"code"`
	Data healthData `json:"data"`
}

// jsonSend 统一 JSON 出口：encoding/json 预序列化后 Send，绕开 fiber 内置编码器。
func jsonSend(c *fiber.Ctx, status int, payload interface{}) error {
	bs, err := json.Marshal(payload)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).Send(utils.JsonFail("响应序列化失败"))
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	return c.Status(status).Send(bs)
}

// MetricsMiddleware 记录请求数/耗时。
// 路径维度使用已注册路由模板（c.Route().Path）而非原始 URL，避免高基数。
func MetricsMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()

		route := c.Route().Path
		if route == "" {
			route = "unmatched"
		}
		metrics.Inc(MetricHTTPRequests, map[string]string{
			"method": c.Method(),
			"path":   route,
			"status": strconv.Itoa(c.Response().StatusCode()),
		})
		metrics.Add(MetricHTTPDuration+"_sum", time.Since(start).Seconds(), map[string]string{
			"method": c.Method(), "path": route,
		})
		metrics.Inc(MetricHTTPDuration+"_count", map[string]string{
			"method": c.Method(), "path": route,
		})
		return err
	}
}

// probeDB MySQL 探活
func probeDB() bool {
	if config.MysqlDBPool == nil {
		return false
	}
	sqlDB, err := config.MysqlDBPool.DB()
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), depProbeTimeout)
	defer cancel()
	return sqlDB.PingContext(ctx) == nil
}

// probeRedis Redis 探活（未配置 Redis 时视为不适用，返回 true 不计入降级）
func probeRedis() bool {
	if config.RedisClient == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), depProbeTimeout)
	defer cancel()
	return config.RedisClient.Ping(ctx).Err() == nil
}

// Health 依赖探活。
// 分级：mysql/redis 为关键依赖（失败 → status=down + HTTP 503）；
//
//	KTO 链为非关键（失败 → status=degraded + HTTP 200，链不可用不应让实例被摘除）。
func Health(c *fiber.Ctx) error {
	dbOK := probeDB()
	redisOK := probeRedis()
	chainOK := kto.ChainReachable(chainProbeTimeout)

	status := "ok"
	httpStatus := fiber.StatusOK
	if !dbOK || !redisOK {
		status = "down"
		httpStatus = fiber.StatusServiceUnavailable
	} else if !chainOK {
		status = "degraded"
	}

	metrics.Set(MetricDBUp, b2f(dbOK), nil)
	metrics.Set(MetricRedisUp, b2f(redisOK), nil)
	metrics.Set(MetricChainUp, b2f(chainOK), nil)

	return jsonSend(c, httpStatus, healthResp{
		Code: 0,
		Data: healthData{
			Status:        status,
			UptimeSeconds: int64(metrics.UptimeSeconds()),
			Checks: map[string]string{
				"mysql":    okText(dbOK),
				"redis":    okText(redisOK),
				"ktoChain": okText(chainOK),
			},
			Time: time.Now().Format(time.RFC3339),
		},
	})
}

// MetricsText 输出 Prometheus 文本暴露格式。
// 每次抓取刷新 DB/Redis 仪表；链可达性由后台采集器维护（避免抓取时阻塞数秒）。
func MetricsText(c *fiber.Ctx) error {
	metrics.Set(MetricUp, 1, nil)
	metrics.Set(MetricUptime, metrics.UptimeSeconds(), nil)
	metrics.Set(MetricDBUp, b2f(probeDB()), nil)
	metrics.Set(MetricRedisUp, b2f(probeRedis()), nil)

	c.Set(fiber.HeaderContentType, "text/plain; version=0.0.4; charset=utf-8")
	return c.SendString(metrics.Render())
}

// StartMetricsCollector 启动后台采集器：周期性刷新链节点可达性仪表。
// 返回停止函数（进程退出时调用；常驻服务可不调用）。
func StartMetricsCollector(interval time.Duration) func() {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	stop := make(chan struct{})
	go func() {
		// 启动即采一次，避免首个抓取窗口内无数据
		metrics.Set(MetricChainUp, b2f(kto.ChainReachable(chainProbeTimeout)), nil)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				metrics.Set(MetricChainUp, b2f(kto.ChainReachable(chainProbeTimeout)), nil)
			}
		}
	}()
	return func() { close(stop) }
}

func okText(ok bool) string {
	if ok {
		return "ok"
	}
	return "fail"
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
