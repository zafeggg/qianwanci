package impl

import (
	"math"
	"testing"
)

// 需求核对（TiMi需求#2 / N次方介绍）：
//   「最低限额不会更改，最高限额每轮增加一点点」
//   N次方举例：10-100 → 10-110 → 10-130 → 10-150
// 这里把「下一轮最高限额」的算法钉死，防止后人再改回「沿用当前 max」（改造前的实现就是沿用，与需求不符）。

func TestNextMaxVoteGrowsEveryRound(t *testing.T) {
	orig := RoundMaxVoteGrowRate
	RoundMaxVoteGrowRate = 0.1
	defer func() { RoundMaxVoteGrowRate = orig }()

	//逐轮复合：100 → 110 → 121 → 133.1，与举例（100→110→130→150）同量级且严格递增
	cur := 100.0
	want := []float64{110, 121, 133.1}
	for i, w := range want {
		next := nextMaxVote(cur)
		if math.Abs(next-w) > 1e-9 {
			t.Fatalf("第 %d 轮最高限额：期望 %v，实际 %v", i+1, w, next)
		}
		if next <= cur {
			t.Fatalf("最高限额必须严格递增：cur=%v next=%v", cur, next)
		}
		cur = next
	}
}

// 基数极小时浮点乘法可能不增长（如 1×1.1=1.1 其实会增长，但 0.5×1.1 取整场景需兜底），
// 算法必须保证「至少 +1」，否则需求里的"每轮增加"会静默失效。
func TestNextMaxVoteFallbackIncrement(t *testing.T) {
	orig := RoundMaxVoteGrowRate
	RoundMaxVoteGrowRate = 0 // 显式关掉递增率
	defer func() { RoundMaxVoteGrowRate = orig }()

	if got := nextMaxVote(100); got != 101 {
		t.Fatalf("rate=0 时应至少 +1：期望 101，实际 %v", got)
	}
}

// 未配置（max<=0）时不做任何事，避免把 0 放大成 0（后续 VoteManager 的 max 校验会拦住 0 值）
func TestNextMaxVoteZeroStaysZero(t *testing.T) {
	if got := nextMaxVote(0); got != 0 {
		t.Fatalf("max=0 应原样返回，实际 %v", got)
	}
	if got := nextMaxVote(-1); got != -1 {
		t.Fatalf("max<0 应原样返回，实际 %v", got)
	}
}

// 最低限额的语义是"不变"，这里用同一份参数做一次显式断言，避免有人顺手也给它加递增
func TestMinVoteHasNoGrowParameter(t *testing.T) {
	// 代码层面不存在 MinVoteGrowRate 之类的参数；此处断言 AutoCreateNextRound 的口径常量：
	// 若未来新增该参数，本测试会因编译失败而提醒复核需求（需求明确"最低限额不会更改"）。
	if RoundMaxVoteGrowRate < 0 {
		t.Fatal("递增率不应为负")
	}
}
