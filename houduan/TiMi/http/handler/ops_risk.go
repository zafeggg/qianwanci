package handler

import (
	"strconv"

	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils"

	"github.com/gofiber/fiber/v2"
)

// ============================== D/E 模块：风控名单与审计查询（运维侧） ==============================
// 挂在 /ops 组下（见 cmd/ops/ops.go），受既有 JWT + 审计中间件保护。
// 有了这几个接口，遇到盗号/攻击可以「一键封停」，不必改代码或停服。
// 注：本文件不使用 c.JSON()，原因见 observability.go 顶部说明。

// RiskAdd 加入黑/白名单
// body: {address, kind(0黑名单/1白名单), reason}
func (h OpsHandler) RiskAdd(c *fiber.Ctx) error {
	var req fiber.Map
	if err := c.BodyParser(&req); err != nil {
		return err
	}
	address, _ := req["address"].(string)
	if address == "" {
		return c.Send(utils.JsonFail("请输入地址"))
	}
	kind := uint(0)
	if v, ok := req["kind"].(float64); ok {
		kind = uint(v)
	}
	if kind != model.RiskKindBlacklist && kind != model.RiskKindWhitelist {
		return c.Send(utils.JsonFail("kind 非法（0=黑名单，1=白名单）"))
	}
	reason, _ := req["reason"].(string)
	operator := opsOperatorOf(c)

	if err := model.AddRiskAddress(address, kind, reason, operator); err != nil {
		return c.Send(utils.JsonFail("写入名单失败: " + err.Error()))
	}
	return c.Send(utils.JsonOk(fiber.Map{"address": address, "kind": kind}))
}

// RiskRemove 移出黑/白名单
// body: {address, kind(0黑名单/1白名单)}
func (h OpsHandler) RiskRemove(c *fiber.Ctx) error {
	var req fiber.Map
	if err := c.BodyParser(&req); err != nil {
		return err
	}
	address, _ := req["address"].(string)
	if address == "" {
		return c.Send(utils.JsonFail("请输入地址"))
	}
	kind := uint(0)
	if v, ok := req["kind"].(float64); ok {
		kind = uint(v)
	}
	if err := model.RemoveRiskAddress(address, kind); err != nil {
		return c.Send(utils.JsonFail("移出名单失败: " + err.Error()))
	}
	return c.Send(utils.JsonOk(fiber.Map{"address": address, "kind": kind}))
}

// RiskList 名单列表（kind 缺省返回全部）
func (h OpsHandler) RiskList(c *fiber.Ctx) error {
	var rows []model.RiskAddress
	q := config.MysqlDBPool.Table(model.RiskAddressTable)
	if v := c.Query("kind"); v != "" {
		q = q.Where("`kind` = ?", v)
	}
	if err := q.Order("`id` desc").Limit(500).Find(&rows).Error; err != nil {
		return c.Send(utils.JsonFail("查询名单失败: " + err.Error()))
	}
	out := make([]fiber.Map, 0, len(rows))
	for _, r := range rows {
		out = append(out, fiber.Map{
			"id": r.ID, "address": r.Address, "kind": r.Kind,
			"reason": r.Reason, "operator": r.Operator, "createdAt": r.CreatedAt,
		})
	}
	return c.Send(utils.JsonOk(fiber.Map{"total": len(out), "list": out}))
}

// AuditList 运维操作审计查询（E 模块）
// 支持 path 前缀过滤与 limit，按时间倒序。
func (h OpsHandler) AuditList(c *fiber.Ctx) error {
	limit := 100
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	var rows []model.OpsAudit
	q := config.MysqlDBPool.Table(model.OpsAuditTable)
	if p := c.Query("path"); p != "" {
		q = q.Where("`path` = ?", p)
	}
	if a := c.Query("account"); a != "" {
		q = q.Where("`account` = ?", a)
	}
	if err := q.Order("`id` desc").Limit(limit).Find(&rows).Error; err != nil {
		return c.Send(utils.JsonFail("查询审计失败: " + err.Error()))
	}
	out := make([]fiber.Map, 0, len(rows))
	for _, r := range rows {
		out = append(out, fiber.Map{
			"id": r.ID, "account": r.Account, "walletId": r.WalletId,
			"method": r.Method, "path": r.Path, "ip": r.IP,
			"body": r.Body, "status": r.Status, "respCode": r.RespCode,
			"errMsg": r.ErrMsg, "costMs": r.CostMs, "createdAt": r.CreatedAt,
		})
	}
	return c.Send(utils.JsonOk(fiber.Map{"total": len(out), "list": out}))
}

// opsOperatorOf 取当前运维操作者名（JWT claim name，登录接口在审计中间件里单独取 body.account）
func opsOperatorOf(c *fiber.Ctx) string {
	_, name := opsActor(c)
	if name == "" {
		return "-"
	}
	return name
}
