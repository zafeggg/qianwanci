package handler

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/utils"

	"github.com/gofiber/fiber/v2"
	log "github.com/sirupsen/logrus"
)

// ============================== C 模块：请求级幂等 ==============================
// 语义：写请求（POST/PUT/PATCH/DELETE）携带 `Idempotency-Key`（或 `X-Idempotency-Key`）时，
// 同一 (method, route, key) 在 TTL 内只会真正执行业务一次：
//   - 首次：抢占 Redis 标记（PROCESSING）→ 执行业务 → 落库结果（DONE）
//   - 重放：命中 DONE 直接原样返回上次响应（状态码/Content-Type/body），并带 `Idempotency-Replayed: true`
//   - 并发：命中 PROCESSING 返回 409（同一键正在处理，请勿重复提交）
//   - 业务失败（5xx 或 handler 返回 error）：删除标记，允许客户端重试
//
// 缺省为「可选」：不带键的请求不受影响（兼容存量客户端与 ops 脚本）；
// 置 config.Idempotency.RequireKey = true 后，写请求不带键将返回 400。
//
// 依赖 Redis；Redis 不可用时降级为放行并告警（不阻断业务）。
// 注：本文件不使用 c.JSON()，原因见 observability.go 顶部说明。

const (
	HeaderIdempotencyKey    = "Idempotency-Key"
	HeaderIdempotencyKeyAlt = "X-Idempotency-Key"
	HeaderIdempotencyHit    = "Idempotency-Replayed"

	idemKeyPrefix  = "npower:idem:"
	idemProcessing = "PROCESSING"
	idemDone       = "DONE"
	// idemMaxCacheBody 超过该字节数的响应不缓存（避免把大响应写进 Redis）
	idemMaxCacheBody = 64 * 1024
	// idemDefaultTTL 幂等记录默认保留时长
	idemDefaultTTL = 600 * time.Second
)

type idemRecord struct {
	State       string `json:"state"`
	Status      int    `json:"status"`
	ContentType string `json:"contentType"`
	Body        string `json:"body"`
}

func idemTTL() time.Duration {
	if config.EtcConfig != nil && config.EtcConfig.Idempotency.TTLSeconds > 0 {
		return time.Duration(config.EtcConfig.Idempotency.TTLSeconds) * time.Second
	}
	return idemDefaultTTL
}

func idemRequired() bool {
	return config.EtcConfig != nil && config.EtcConfig.Idempotency.RequireKey
}

// idemKeyOf 拼接 Redis 键：按 method + 路由模板 + 客户端键隔离，避免不同接口互相污染。
func idemKeyOf(c *fiber.Ctx, key string) string {
	route := c.Route().Path
	if route == "" {
		route = c.Path()
	}
	return idemKeyPrefix + c.Method() + ":" + route + ":" + key
}

// IdempotencyMiddleware 请求级幂等中间件（挂在全局，只对写方法生效）
func IdempotencyMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		switch c.Method() {
		case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
			return c.Next()
		}

		key := strings.TrimSpace(c.Get(HeaderIdempotencyKey))
		if key == "" {
			key = strings.TrimSpace(c.Get(HeaderIdempotencyKeyAlt))
		}
		if key == "" {
			if idemRequired() {
				c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
				return c.Status(fiber.StatusBadRequest).
					Send(utils.JsonFailCode(400, "写请求必须携带 "+HeaderIdempotencyKey+" 头"))
			}
			return c.Next()
		}
		if len(key) > 128 {
			c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
			return c.Status(fiber.StatusBadRequest).
				Send(utils.JsonFailCode(400, HeaderIdempotencyKey+" 过长（上限 128 字节）"))
		}

		if config.RedisClient == nil {
			log.Warnln("[idem] Redis 不可用，本次请求跳过幂等保护")
			return c.Next()
		}

		ctx := context.Background()
		redisKey := idemKeyOf(c, key)
		ttl := idemTTL()

		seed, _ := json.Marshal(idemRecord{State: idemProcessing})
		acquired, err := config.RedisClient.SetNX(ctx, redisKey, seed, ttl).Result()
		if err != nil {
			log.WithFields(log.Fields{"err": err}).Warnln("[idem] 抢占幂等键失败，放行本次请求")
			return c.Next()
		}

		if !acquired {
			raw, gerr := config.RedisClient.Get(ctx, redisKey).Result()
			if gerr != nil {
				// 键在读取前刚好过期：放行，走正常业务
				return c.Next()
			}
			var rec idemRecord
			if jerr := json.Unmarshal([]byte(raw), &rec); jerr == nil && rec.State == idemDone {
				if rec.ContentType != "" {
					c.Set(fiber.HeaderContentType, rec.ContentType)
				}
				c.Set(HeaderIdempotencyHit, "true")
				return c.Status(rec.Status).Send([]byte(rec.Body))
			}
			c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
			c.Set(HeaderIdempotencyHit, "true")
			return c.Status(fiber.StatusConflict).
				Send(utils.JsonFailCode(409, "重复请求：同一幂等键正在处理中，请勿重复提交"))
		}

		// 首次执行
		handlerErr := c.Next()

		status := c.Response().StatusCode()
		body := c.Response().Body()
		if handlerErr != nil || status >= 500 {
			// 未成功：释放键，允许客户端用同一键重试
			config.RedisClient.Del(ctx, redisKey)
			return handlerErr
		}
		if len(body) > idemMaxCacheBody {
			// 响应过大不缓存；释放键避免后续重试被误判为"处理中"
			config.RedisClient.Del(ctx, redisKey)
			return handlerErr
		}

		rec := idemRecord{
			State:       idemDone,
			Status:      status,
			ContentType: string(c.Response().Header.ContentType()),
			Body:        string(body),
		}
		if bs, jerr := json.Marshal(rec); jerr == nil {
			if serr := config.RedisClient.Set(ctx, redisKey, bs, ttl).Err(); serr != nil {
				log.WithFields(log.Fields{"err": serr}).Warnln("[idem] 写入幂等结果失败（重放将不会命中缓存）")
			}
		}
		return handlerErr
	}
}
