package model

import (
	"context"
	"strconv"
	"sync"
	"time"

	"com.fibonacci.crowd/config"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// RiskAddressTable 风控名单表名
const RiskAddressTable = "risk_address"

// 名单类型
const (
	RiskKindBlacklist uint = 0 //黑名单：禁止参投/提现/领取
	RiskKindWhitelist uint = 1 //白名单：开启白名单后仅名单内地址可参投/提现
)

// RiskAddress 风控地址名单（D 模块，2026-09-18 新增）。
// 背景：全仓此前无任何黑名单/白名单机制。资金通道上线前必须有「一键封停」能力，
// 否则遇到盗号、洗钱、攻击只能改代码或停服。
type RiskAddress struct {
	gorm.Model
	// ⚠ 必须显式 size：GORM 的 MySQL 驱动只对带 `index` 标签的 string 字段用 varchar(191)，
	// **仅带 `uniqueIndex` 的会建成 longtext**，而 MySQL 不允许在 longtext 上建索引 →
	// AutoMigrate 只打日志不中断，唯一约束静默失效（2026-09-18 实测 err 1170）。
	Address  string `gorm:"size:64;uniqueIndex:uk_risk_address_kind,priority:1"`
	Kind     uint   `gorm:"uniqueIndex:uk_risk_address_kind,priority:2"` //0黑名单 1白名单
	Reason   string `gorm:"size:255"`                                    //加入原因（审计用）
	Operator string `gorm:"size:64"`                                     //操作人（审计用）
}

// riskCache 名单查询缓存：风控检查在每次写请求上执行，不能每条都打 DB。
// 30 秒 TTL + 变更时显式失效，兼顾性能与「封停生效速度」。
var (
	riskCacheMu   sync.RWMutex
	riskCache     = map[string]bool{}
	riskCacheTime time.Time
)

const riskCacheTTL = 30 * time.Second

func riskCacheKey(kind uint, address string) string {
	return strconv.FormatUint(uint64(kind), 10) + "|" + address
}

// loadRiskCache 全量加载名单（表很小，全量比按需查询更简单且不会漏）
func loadRiskCache() {
	if config.MysqlDBPool == nil {
		return
	}
	var rows []RiskAddress
	if err := config.MysqlDBPool.Table(RiskAddressTable).Find(&rows).Error; err != nil {
		//查询失败保持旧缓存（宁可沿用旧名单，也不要误放行）
		return
	}
	next := make(map[string]bool, len(rows))
	for _, r := range rows {
		next[riskCacheKey(r.Kind, r.Address)] = true
	}
	riskCacheMu.Lock()
	riskCache = next
	riskCacheTime = time.Now()
	riskCacheMu.Unlock()
}

// IsListed 判断地址是否在指定名单中（带 30s 缓存）
func IsListed(kind uint, address string) bool {
	if address == "" {
		return false
	}
	riskCacheMu.RLock()
	fresh := time.Since(riskCacheTime) < riskCacheTTL
	hit := riskCache[riskCacheKey(kind, address)]
	riskCacheMu.RUnlock()

	if fresh {
		return hit
	}
	loadRiskCache()
	riskCacheMu.RLock()
	defer riskCacheMu.RUnlock()
	return riskCache[riskCacheKey(kind, address)]
}

// InvalidateRiskCache 名单变更后立即失效缓存（ops 增删名单时调用）
func InvalidateRiskCache() {
	riskCacheMu.Lock()
	riskCacheTime = time.Time{}
	riskCacheMu.Unlock()
}

// RiskChannel 风控名单变更广播频道。
// 名单校验跑在 npower 进程，增删名单在 ops 进程；只靠 TTL 失效意味着「封停/解封」有最长 30s 延迟，
// 对安全响应来说太慢。这里复用项目既有的 Redis 广播模式（见 core/impl/params.go 参数热更新），
// ops 变更后广播、各进程订阅即失效本地缓存，做到秒级生效。
const RiskChannel = "npower:risk:reload"

// PublishRiskChange 广播名单变更（Redis 不可用时静默失败，靠 TTL 兜底）
func PublishRiskChange() {
	if config.RedisClient == nil {
		return
	}
	if err := config.RedisClient.Publish(context.Background(), RiskChannel, "1").Err(); err != nil {
		log.WithFields(log.Fields{"err": err}).Warnln("[risk] 广播名单变更失败（本次变更最迟 30s 后生效）")
	}
}

// WatchRiskChange 订阅名单变更广播并失效本地缓存（各常驻服务启动时调用一次）
func WatchRiskChange() {
	if config.RedisClient == nil {
		return
	}
	pubsub := config.RedisClient.Subscribe(context.Background(), RiskChannel)
	ch := pubsub.Channel()
	go func() {
		for range ch {
			InvalidateRiskCache()
		}
	}()
}

// AddRiskAddress 加入名单（幂等：已存在则更新原因；已软删则复活）
//
// ⚠ 必须用 Unscoped()：`uk_risk_address_kind` 是普通唯一索引，**软删除的行仍然占用**
// (address,kind) 这个键。若只在未删除范围内查找，FirstOrCreate 会尝试 INSERT 并撞唯一键，
// 表现为「封停 → 解封 → 再封停」失败（2026-09-18 实测复现）。Unscoped 找回历史行并复活（deleted_at 置 NULL）。
func AddRiskAddress(address string, kind uint, reason, operator string) error {
	if config.MysqlDBPool == nil {
		return gorm.ErrInvalidDB
	}
	rec := RiskAddress{Address: address, Kind: kind, Reason: reason, Operator: operator}
	err := config.MysqlDBPool.Table(RiskAddressTable).Unscoped().
		Where("`address` = ? and `kind` = ?", address, kind).
		Assign(map[string]interface{}{
			"reason":     reason,
			"operator":   operator,
			"deleted_at": nil,
		}).
		FirstOrCreate(&rec).Error
	if err != nil {
		return err
	}
	InvalidateRiskCache()
	PublishRiskChange()
	return nil
}

// RemoveRiskAddress 移出名单
func RemoveRiskAddress(address string, kind uint) error {
	if config.MysqlDBPool == nil {
		return gorm.ErrInvalidDB
	}
	if err := config.MysqlDBPool.Table(RiskAddressTable).
		Where("`address` = ? and `kind` = ?", address, kind).
		Delete(&RiskAddress{}).Error; err != nil {
		return err
	}
	InvalidateRiskCache()
	PublishRiskChange()
	return nil
}
