package metrics

import (
	"strings"
	"testing"
)

// F 模块指标注册表单测：不依赖 MySQL/Redis。
// 运行：go test -p 1 -count=1 ./utils/metrics

func TestCounterAndGaugeValues(t *testing.T) {
	Reset()

	Inc("npower_demo_total", map[string]string{"a": "1"})
	Inc("npower_demo_total", map[string]string{"a": "1"})
	Add("npower_demo_total", 3, map[string]string{"a": "2"})
	Set("npower_demo_gauge", 7, nil)

	snap := Snapshot()
	if got := snap[`npower_demo_total{a="1"}`]; got != 2 {
		t.Fatalf("计数器累加错误：期望 2，实际 %v", got)
	}
	if got := snap[`npower_demo_total{a="2"}`]; got != 3 {
		t.Fatalf("Add 错误：期望 3，实际 %v", got)
	}
	if got := snap["npower_demo_gauge"]; got != 7 {
		t.Fatalf("仪表赋值错误：期望 7，实际 %v", got)
	}
}

func TestLabelOrderIsStable(t *testing.T) {
	Reset()

	//同一组标签、不同书写顺序，必须归并成同一条时间序列（否则 /metrics 会出现重复指标）
	Inc("npower_lbl_total", map[string]string{"method": "GET", "path": "/x", "status": "200"})
	Inc("npower_lbl_total", map[string]string{"status": "200", "path": "/x", "method": "GET"})

	snap := Snapshot()
	if len(snap) != 1 {
		t.Fatalf("标签顺序不同被当成两条序列，实际条数=%d：%v", len(snap), snap)
	}
	for k, v := range snap {
		if v != 2 {
			t.Fatalf("归并后计数错误：%s = %v，期望 2", k, v)
		}
		if k != `npower_lbl_total{method="GET",path="/x",status="200"}` {
			t.Fatalf("标签未按字典序渲染：%s", k)
		}
	}
}

func TestRenderPrometheusFormat(t *testing.T) {
	Reset()

	Inc("npower_b_total", nil)
	Set("npower_a_gauge", 1.5, map[string]string{"kind": "x"})

	out := Render()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	//必须按指标名排序，且每个指标名前有且只有一条 # TYPE
	if len(lines) != 4 {
		t.Fatalf("行数错误，期望 4 行，实际 %d 行：\n%s", len(lines), out)
	}
	if lines[0] != "# TYPE npower_a_gauge gauge" {
		t.Fatalf("第 1 行应为 gauge 的 TYPE 行，实际：%s", lines[0])
	}
	if lines[1] != `npower_a_gauge{kind="x"} 1.5` {
		t.Fatalf("第 2 行错误：%s", lines[1])
	}
	if lines[2] != "# TYPE npower_b_total counter" {
		t.Fatalf("第 3 行应为 counter 的 TYPE 行，实际：%s", lines[2])
	}
	if lines[3] != "npower_b_total 1" {
		t.Fatalf("无标签指标渲染错误：%s", lines[3])
	}
	if strings.Count(out, "# TYPE npower_a_gauge") != 1 {
		t.Fatalf("同一指标的 TYPE 行重复输出")
	}
}

func TestCounterTypePersistsAcrossFirstWrite(t *testing.T) {
	Reset()

	//先用 Inc 登记为 counter，再用 Set 改值，类型不应被改成 gauge
	Inc("npower_mixed", nil)
	Set("npower_mixed", 9, nil)

	out := Render()
	if !strings.Contains(out, "# TYPE npower_mixed counter") {
		t.Fatalf("counter 类型被 Set 改写：\n%s", out)
	}
}

func TestUnregisterAndReset(t *testing.T) {
	Reset()

	Inc("npower_tmp_total", map[string]string{"k": "v"})
	if len(Snapshot()) != 1 {
		t.Fatal("登记后应有 1 条")
	}
	Unregister("npower_tmp_total", map[string]string{"k": "v"})
	if len(Snapshot()) != 0 {
		t.Fatal("Unregister 未移除指标")
	}

	Inc("npower_tmp_total", nil)
	Reset()
	if len(Snapshot()) != 0 {
		t.Fatal("Reset 未清空指标")
	}
	if Render() != "" {
		t.Fatalf("空注册表应渲染空串，实际：%q", Render())
	}
}

func TestUptimeMonotonic(t *testing.T) {
	a := UptimeSeconds()
	b := UptimeSeconds()
	if b < a {
		t.Fatalf("运行时长不单调：%v → %v", a, b)
	}
}
