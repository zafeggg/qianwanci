// Package metrics 极简进程内指标注册表（F 模块：可观测性）。
//
// 设计取舍：不引入 prometheus/client_golang（依赖体积大、本机模块代理受限），
// 自研计数器 + Prometheus 文本暴露格式（/metrics 可直接被 Prometheus 抓取）。
// 线程安全；进程内聚合，重启即清零（与 Prometheus 计数语义一致）。
package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// 指标类型（决定 /metrics 输出中的 # TYPE 行）
const (
	TypeCounter = "counter"
	TypeGauge   = "gauge"
)

type sample struct {
	name   string
	labels string
	kind   string
	value  float64
}

var (
	mu      sync.RWMutex
	samples = map[string]*sample{}
	started = time.Now()
)

// labelString 把标签渲染成 Prometheus 形式 {k="v",k2="v2"}；空标签返回 ""。
// 标签名按字典序排序，保证同一指标的不同采集顺序产出稳定键。
func labelString(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%s=%q", k, labels[k])
	}
	sb.WriteByte('}')
	return sb.String()
}

func keyOf(name, labels string) string { return name + labels }

// store 写入一个指标值（kind 只在首次登记时生效）。
func store(name string, labels map[string]string, kind string, v float64) {
	ls := labelString(labels)
	k := keyOf(name, ls)
	mu.Lock()
	s := samples[k]
	if s == nil {
		s = &sample{name: name, labels: ls, kind: kind}
		samples[k] = s
	}
	s.value = v
	mu.Unlock()
}

// Inc 计数器 +1
func Inc(name string, labels map[string]string) { Add(name, 1, labels) }

// Add 计数器/仪表累加
func Add(name string, v float64, labels map[string]string) {
	ls := labelString(labels)
	k := keyOf(name, ls)
	mu.Lock()
	s := samples[k]
	if s == nil {
		s = &sample{name: name, labels: ls, kind: TypeCounter}
		samples[k] = s
	}
	s.value += v
	mu.Unlock()
}

// Set 仪表赋值（DB 是否可用、在线数等）
func Set(name string, v float64, labels map[string]string) { store(name, labels, TypeGauge, v) }

// Unregister 移除指标（测试或动态标签回收用）
func Unregister(name string, labels map[string]string) {
	mu.Lock()
	delete(samples, keyOf(name, labelString(labels)))
	mu.Unlock()
}

// UptimeSeconds 进程已运行秒数
func UptimeSeconds() float64 { return time.Since(started).Seconds() }

// Snapshot 返回当前所有指标（key = name+labels，便于测试断言）
func Snapshot() map[string]float64 {
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]float64, len(samples))
	for k, s := range samples {
		out[k] = s.value
	}
	return out
}

// Render 输出 Prometheus 文本暴露格式（含 # TYPE 行，按指标名排序保证稳定输出）
func Render() string {
	mu.RLock()
	list := make([]*sample, 0, len(samples))
	for _, s := range samples {
		list = append(list, s)
	}
	mu.RUnlock()

	sort.Slice(list, func(i, j int) bool {
		if list[i].name != list[j].name {
			return list[i].name < list[j].name
		}
		return list[i].labels < list[j].labels
	})

	var sb strings.Builder
	lastType := ""
	for _, s := range list {
		if s.name != lastType {
			fmt.Fprintf(&sb, "# TYPE %s %s\n", s.name, s.kind)
			lastType = s.name
		}
		fmt.Fprintf(&sb, "%s%s %g\n", s.name, s.labels, s.value)
	}
	return sb.String()
}

// Reset 清空全部指标（仅测试使用）
func Reset() {
	mu.Lock()
	samples = map[string]*sample{}
	started = time.Now()
	mu.Unlock()
}
