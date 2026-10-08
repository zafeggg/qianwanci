package impl

import (
	"math"
	"testing"
)

// ============================== 4 小时均价单测（2026-09-30 新增）==============================
//
// 需求#8：模式 1/2 的每日最低产出 = (算力 × 3) / 300 / **4小时均价**。
// 纯函数 meanPriceSamples 负责"窗口过滤 + 异常值过滤 + 样本不足判定"，
// 这三条规则任何一条写错，都会让"保底产出"在行情抖动/刚部署时算错。

func TestMeanPriceSamples_Normal(t *testing.T) {
	now := int64(1_000_000)
	samples := []PriceSample{
		{At: now - 10, Price: 0.10},
		{At: now - 100, Price: 0.20},
		{At: now - 1000, Price: 0.30},
	}
	avg, ok := meanPriceSamples(samples, now, 4*3600, 3)
	if !ok {
		t.Fatal("3 条有效样本应满足最低样本数")
	}
	if math.Abs(avg-0.20) > 1e-12 {
		t.Fatalf("均值应为 0.20，实际 %v", avg)
	}
}

func TestMeanPriceSamples_WindowFilter(t *testing.T) {
	now := int64(1_000_000)
	samples := []PriceSample{
		{At: now - 10, Price: 1.0},
		{At: now - 20, Price: 3.0},
		{At: now - 5*3600, Price: 999.0}, //超出 4 小时窗口，必须排除
		{At: now - 30, Price: 2.0},
	}
	avg, ok := meanPriceSamples(samples, now, 4*3600, 3)
	if !ok {
		t.Fatal("窗口内 3 条应满足最低样本数")
	}
	if math.Abs(avg-2.0) > 1e-12 {
		t.Fatalf("窗口外样本必须被排除（均值应 2.0），实际 %v", avg)
	}
}

func TestMeanPriceSamples_RejectsBadPrices(t *testing.T) {
	now := int64(1_000_000)
	samples := []PriceSample{
		{At: now - 10, Price: 2.0},
		{At: now - 20, Price: 0},              //0 = 行情不可用
		{At: now - 30, Price: -1},             //负价
		{At: now - 40, Price: math.NaN()},     //NaN
		{At: now - 50, Price: math.Inf(1)},    //+Inf
		{At: now - 60, Price: 4.0},
	}
	avg, ok := meanPriceSamples(samples, now, 4*3600, 2)
	if !ok {
		t.Fatal("2 条有效样本应满足最低样本数")
	}
	if math.Abs(avg-3.0) > 1e-12 {
		t.Fatalf("0/负/NaN/Inf 都必须被排除（均值应 3.0），实际 %v", avg)
	}
}

func TestMeanPriceSamples_InsufficientSamples(t *testing.T) {
	now := int64(1_000_000)
	//只有 2 条有效样本，minSamples=3 → 必须报"样本不足"（调用方据此回落实时价）
	samples := []PriceSample{{At: now - 10, Price: 1.0}, {At: now - 20, Price: 1.0}}
	if _, ok := meanPriceSamples(samples, now, 4*3600, 3); ok {
		t.Fatal("样本不足必须返回 ok=false（否则会把 1~2 个瞬时价当成均价）")
	}
	if _, ok := meanPriceSamples(nil, now, 4*3600, 1); ok {
		t.Fatal("空样本必须返回 ok=false")
	}
}

func TestMeanPriceSamples_FutureSamplesIgnored(t *testing.T) {
	now := int64(1_000_000)
	//时钟漂移导致的"未来"样本不应参与（否则一个错时间的样本能长期拉偏均价）
	samples := []PriceSample{
		{At: now - 10, Price: 1.0},
		{At: now - 20, Price: 1.0},
		{At: now + 100, Price: 1000.0},
	}
	avg, ok := meanPriceSamples(samples, now, 4*3600, 2)
	if !ok {
		t.Fatal("2 条有效样本应满足最低样本数")
	}
	if math.Abs(avg-1.0) > 1e-12 {
		t.Fatalf("未来样本必须被忽略（均值应 1.0），实际 %v", avg)
	}
}
