package handler

import (
	"encoding/json"
	"strings"
	"time"

	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/model"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v4"
	log "github.com/sirupsen/logrus"
)

// ============================== E 模块：运维操作审计 ==============================
// 挂在 /ops 路由组上（见 cmd/ops/ops.go），对每次调用落一行审计。
// 用中间件而非改写 17 个 handler：新增路由自动被覆盖，不会漏。
//
// 取舍：同步写库（ops 调用量极低），保证「调用即留痕」；写库失败只告警不影响业务。
// 注：本文件不使用 c.JSON()，原因见 observability.go 顶部说明。

// sensitiveKeyParts 命中即脱敏的字段名片段（小写比对）
var sensitiveKeyParts = []string{"pwd", "password", "private", "pri", "secret", "key", "token", "sign"}

// redactBody 对请求体做字段级脱敏。
// 顺序很关键：**先解析脱敏，再截断**。若先按字节截断，大请求体会被截成非法 JSON，
// 于是整段内容被丢弃（拿不到任何审计信息）。body 超过 maxParse 才整体隐藏，避免审计中间件被大包拖垮。
func redactBody(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	const (
		maxParse = 256 * 1024
		maxKeep  = 4096
	)
	if len(raw) > maxParse {
		return "[请求体过大，内容已隐藏]"
	}

	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return "[非 JSON 或解析失败，内容已隐藏]"
	}
	for k := range m {
		lk := strings.ToLower(k)
		for _, part := range sensitiveKeyParts {
			if strings.Contains(lk, part) {
				m[k] = "***"
				break
			}
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return "[序列化失败，内容已隐藏]"
	}
	if len(out) > maxKeep {
		return string(out[:maxKeep]) + "...[已截断]"
	}
	return string(out)
}

// opsActor 从 JWT 中取操作者（gofiber/jwt v3 把 *jwt.Token 放在 Locals("user")）
func opsActor(c *fiber.Ctx) (uint, string) {
	t, ok := c.Locals("user").(*jwt.Token)
	if !ok || t == nil {
		return 0, ""
	}
	claims, ok := t.Claims.(jwt.MapClaims)
	if !ok {
		return 0, ""
	}
	name, _ := claims["name"].(string)
	var wid uint
	switch v := claims["walletId"].(type) {
	case float64:
		wid = uint(v)
	case int:
		wid = uint(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			wid = uint(n)
		}
	}
	return wid, name
}

// respCodeOf 从响应体提取业务码（本项目响应统一 {code:...}）。
// /ops 的响应既有 {code:200,...} 也有 {code:600,...}，取不到记 -1。
func respCodeOf(body []byte) int {
	if len(body) == 0 {
		return -1
	}
	var probe struct {
		Code *int `json:"code"`
	}
	if err := json.Unmarshal(body, &probe); err != nil || probe.Code == nil {
		return -1
	}
	return *probe.Code
}

// OpsAuditMiddleware 运维操作审计中间件
func OpsAuditMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if config.MysqlDBPool == nil {
			return c.Next()
		}
		start := time.Now()
		body := c.Body()
		// 先取请求体里的 account（/ops/sign 此时还没有 JWT）
		bodyAccount := ""
		{
			var m map[string]interface{}
			if json.Unmarshal(body, &m) == nil {
				if a, ok := m["account"].(string); ok {
					bodyAccount = a
				}
			}
		}

		handlerErr := c.Next()

		path := c.Route().Path
		if path == "" {
			path = c.Path()
		}
		wid, name := opsActor(c)
		if name == "" {
			name = bodyAccount
		}
		if name == "" {
			name = "-"
		}

		rec := model.OpsAudit{
			Account:  name,
			WalletId: wid,
			Method:   c.Method(),
			Path:     path,
			IP:       c.IP(),
			Body:     redactBody(body),
			Status:   c.Response().StatusCode(),
			RespCode: respCodeOf(c.Response().Body()),
			CostMs:   time.Since(start).Milliseconds(),
		}
		if handlerErr != nil {
			rec.ErrMsg = handlerErr.Error()
		}

		if err := config.MysqlDBPool.Table(model.OpsAuditTable).Create(&rec).Error; err != nil {
			log.WithFields(log.Fields{"err": err, "path": path}).Errorln("[ops-audit] 审计落库失败")
		}
		return handlerErr
	}
}
