package impl

import (
	"math"
	"testing"
)

// ============================== 爆仓结算口径单测（2026-09-30 新增）==============================
//
// 背景：2026-09-30 审计发现 reward.Failed（倒2/倒3 结算）**只扣不退** —— 只把 amount*LossRate
// 记成亏损并折算算力，另一半本金从不入账，等于把用户本金 100% 吃掉；且整个包此前没有一行
// 爆仓结算的测试（reward_test.go 是空文件），所以这个资金级缺陷长期无人发现。
//
// 需求原文（千万次#5 / #7）：
//   - 「倒2 倒3 扣除一半」
//   - 「爆仓赔付：50%被扣走的币 … 比如：倒数第一轮参与10万 则退回5万，币价此时0.1u，
//      此时获得算力：50000x0.1=5000 算力」——"退回5万"就是退还另一半。

func TestSplitFailureAmount_HalfDeductedHalfRefunded(t *testing.T) {
	deducted, refund := splitFailureAmount(100000, 0.5)
	if math.Abs(deducted-50000) > 1e-9 {
		t.Fatalf("被扣部分应为 50%%（50000），实际 %v", deducted)
	}
	if math.Abs(refund-50000) > 1e-9 {
		t.Fatalf("退还部分应为 50%%（50000），实际 %v", refund)
	}
	//需求#7 的算力算例：被扣的 5 万按爆仓价 0.1U 折算 → 算力 = 50000 × 0.1 = 5000
	if power := deducted * 0.1; math.Abs(power-5000) > 1e-9 {
		t.Fatalf("算力折算应为 5000，实际 %v", power)
	}
}

func TestSplitFailureAmount_AlwaysConservesPrincipal(t *testing.T) {
	//任何 LossRate 下"被扣 + 退还"都必须等于本金：这是防止再次出现"钱凭空消失"的不变量
	for _, amount := range []float64{0, 1, 7.5, 100, 1e9} {
		for _, rate := range []float64{-1, 0, 0.1, 0.5, 0.9, 1, 1.5} {
			deducted, refund := splitFailureAmount(amount, rate)
			if deducted < 0 || refund < 0 {
				t.Fatalf("不允许出现负数：amount=%v rate=%v → deducted=%v refund=%v", amount, rate, deducted, refund)
			}
			if amount > 0 && math.Abs((deducted+refund)-amount) > 1e-6 {
				t.Fatalf("本息必须守恒：amount=%v rate=%v → %v + %v = %v", amount, rate, deducted, refund, deducted+refund)
			}
		}
	}
}

func TestSplitFailureAmount_EdgeCases(t *testing.T) {
	//LossRate=0（配置为不扣）：全额退还，不折算算力
	if d, r := splitFailureAmount(100, 0); d != 0 || r != 100 {
		t.Fatalf("rate=0 应全额退还：deducted=%v refund=%v", d, r)
	}
	//LossRate>=1：全扣（保留语义）
	if d, r := splitFailureAmount(100, 1); d != 100 || r != 0 {
		t.Fatalf("rate=1 应全扣：deducted=%v refund=%v", d, r)
	}
	//非正数本金：不产生任何记账（避免把 0 或负数写进流水/算力）
	if d, r := splitFailureAmount(0, 0.5); d != 0 || r != 0 {
		t.Fatalf("amount=0 应为空操作：deducted=%v refund=%v", d, r)
	}
	if d, r := splitFailureAmount(-5, 0.5); d != 0 || r != 0 {
		t.Fatalf("amount<0 应为空操作：deducted=%v refund=%v", d, r)
	}
}

// ============================== 失败轮结算计划单测 ==============================
//
// 背景：失败分支原先只处理 F-1/F-2/F，漏掉"三进一出到期轮 F-MinRound"（第 N 轮应在第 N+MinRound
// 轮结束时结算，无论该轮成败），导致该轮仓位永久停在待结算、本息一分不发。

func TestFailureRoundPlan_SettlesDueRoundOnFailure(t *testing.T) {
	//第 6 轮爆仓（MinRound=3）：到期该结算的是第 3 轮，倒2/倒3 是第 5/4 轮
	due, hasDue, failed := failureRoundPlan(6, 3)
	if !hasDue || due != 3 {
		t.Fatalf("第6轮失败时必须结算第3轮：due=%v hasDue=%v", due, hasDue)
	}
	if len(failed) != 2 || failed[0] != 5 || failed[1] != 4 {
		t.Fatalf("倒2/倒3 应为 [5,4]，实际 %v", failed)
	}
}

func TestFailureRoundPlan_EarlyRounds(t *testing.T) {
	//第 1 轮失败：没有倒2/倒3，也没有到期轮
	due, hasDue, failed := failureRoundPlan(1, 3)
	if hasDue || due != 0 || len(failed) != 0 {
		t.Fatalf("第1轮失败应无事可做：due=%v hasDue=%v failed=%v", due, hasDue, failed)
	}
	//第 2 轮失败：只有倒2（第1轮）
	_, hasDue2, failed2 := failureRoundPlan(2, 3)
	if hasDue2 || len(failed2) != 1 || failed2[0] != 1 {
		t.Fatalf("第2轮失败应只处理第1轮：hasDue=%v failed=%v", hasDue2, failed2)
	}
	//第 3 轮失败：倒2/倒3 = 第2/1 轮
	_, hasDue3, failed3 := failureRoundPlan(3, 3)
	if hasDue3 || len(failed3) != 2 || failed3[0] != 2 || failed3[1] != 1 {
		t.Fatalf("第3轮失败应处理第2/1轮：hasDue=%v failed=%v", hasDue3, failed3)
	}
	//第 4 轮失败：此时第 1 轮已到期（4-3=1），必须走成功结算，不能被当成倒3 扣半
	due4, hasDue4, failed4 := failureRoundPlan(4, 3)
	if !hasDue4 || due4 != 1 {
		t.Fatalf("第4轮失败时第1轮到期：due=%v hasDue=%v", due4, hasDue4)
	}
	if len(failed4) != 2 || failed4[0] != 3 || failed4[1] != 2 {
		t.Fatalf("第4轮失败时倒2/倒3 应为 [3,2]，实际 %v", failed4)
	}
}

func TestFailureRoundPlan_MinRoundCollisionPriority(t *testing.T) {
	//MinRound=1（配置被改小）：到期轮 = F-1，与倒2 重叠 → 该轮按三进一出结算，不倒扣
	due, hasDue, failed := failureRoundPlan(5, 1)
	if !hasDue || due != 4 {
		t.Fatalf("MinRound=1 时到期轮应为第4轮：due=%v hasDue=%v", due, hasDue)
	}
	for _, n := range failed {
		if n == due {
			t.Fatalf("重叠轮 %v 不应同时进入倒2/倒3 列表：%v", n, failed)
		}
	}
	if len(failed) != 1 || failed[0] != 3 {
		t.Fatalf("MinRound=1 时倒3 仍应为第3轮，实际 %v", failed)
	}
}
