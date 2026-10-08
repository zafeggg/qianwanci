package handler

import (
	"com.fibonacci.crowd/core/impl"
	"testing"
)

// ============================== 提现额度口径单测（2026-09-30 owner 裁定）==============================
//
// owner 裁定：**单笔起提 100；单笔/单日/笔数一律无上限**。
// 对应实现：
//   - 下限：`impl.WithdrawMinAmount`（在 /api/withdraw 与原生路径里拦）—— 本文件不覆盖，由脚本断言（提 50 被拒/提 100 放行）
//   - 上限：`impl.WithdrawSingleMax` / `WithdrawDailyMax` / `WithdrawDailyCountMax`，**0 = 不限**
//     ⚠ 历史上的 10000 硬编码兜底与"最多 3 次/24h"计数已删除（见 handler/wallet.go），
//       否则"无上限"会在原生路径上被悄悄卡住 —— 这两条断言就是防止它被改回来。

func withLimits(t *testing.T, single, daily float64, count int, fn func()) {
	t.Helper()
	o1, o2, o3 := impl.WithdrawSingleMax, impl.WithdrawDailyMax, impl.WithdrawDailyCountMax
	impl.WithdrawSingleMax, impl.WithdrawDailyMax, impl.WithdrawDailyCountMax = single, daily, count
	t.Cleanup(func() {
		impl.WithdrawSingleMax, impl.WithdrawDailyMax, impl.WithdrawDailyCountMax = o1, o2, o3
	})
	fn()
}

func TestCheckWithdrawLimits_UnlimitedByRuling(t *testing.T) {
	//三个阈值全 0（owner 裁定的生产口径）= 不限制：任意大额都必须放行
	withLimits(t, 0, 0, 0, func() {
		for _, amount := range []float64{100, 10000, 10001, 1e6, 1e12} {
			if msg := CheckWithdrawLimits("0xabc", amount); msg != "" {
				t.Fatalf("无上限口径下 amount=%v 不应被拦，实际返回 %q", amount, msg)
			}
		}
	})
}

func TestCheckWithdrawLimits_SingleMaxStillWorks(t *testing.T) {
	//运维若显式配置单笔上限（>0），校验必须仍然生效（能力没被删掉，只是默认关闭）
	withLimits(t, 100, 0, 0, func() {
		if msg := CheckWithdrawLimits("0xabc", 101); msg == "" {
			t.Fatal("配置单笔上限 100 后，提 101 应被拒")
		}
		if msg := CheckWithdrawLimits("0xabc", 100); msg != "" {
			t.Fatalf("恰好等于上限应放行，实际 %q", msg)
		}
	})
}

func TestCheckWithdrawLimits_NilDBStillChecksSingleMax(t *testing.T) {
	//db 为空（未初始化）时不做日累计统计，但单笔上限这一条不依赖 DB，必须照拦
	withLimits(t, 50, 1000, 3, func() {
		if msg := checkWithdrawLimits(nil, "0xabc", 51); msg == "" {
			t.Fatal("单笔上限与 DB 无关，db=nil 时也应拦下")
		}
		if msg := checkWithdrawLimits(nil, "0xabc", 50); msg != "" {
			t.Fatalf("未超限应放行，实际 %q", msg)
		}
	})
}

func TestCheckWithdrawLimits_EmptyAddressPasses(t *testing.T) {
	//地址为空（调用方漏传）不应误判为"超限"，由身份校验层负责拒绝
	withLimits(t, 10, 20, 1, func() {
		if msg := CheckWithdrawLimits("", 999); msg != "" {
			t.Fatalf("地址为空应原样放行（由上层身份校验处理），实际 %q", msg)
		}
	})
}
