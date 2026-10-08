package impl

import (
	"com.fibonacci.crowd/config"
	"reflect"
	"testing"
)

// ============================== 运行参数「复位 + 覆盖」语义单测（2026-09-30 新增）==============================
//
// 背景：ApplyNpowerParams 的约定是「0/空 = 不覆盖」，所以它只会把参数**改成**某个值，
// 不会改回默认值 —— 一旦运维设错参数，删掉配置键也回不到默认（只能在线上用错值硬顶，
// 或者重启进程）。修复方式是每次 LoadNpowerParams 之前先用 init 期快照复位。
// 这些断言锁死"复位"这条路（纯函数，不需要数据库）。

func TestResetToCodeDefaults_RestoresOverriddenParams(t *testing.T) {
	//保存并在用例结束后恢复，避免污染同包其它用例
	origStatic, origFee, origMin, origWs := StaticRewardRate, FeeSymbol, MinRound, WithdrawSingleMax
	defer func() {
		StaticRewardRate, FeeSymbol, MinRound, WithdrawSingleMax = origStatic, origFee, origMin, origWs
	}()

	ApplyNpowerParams(config.NpowerParams{
		StaticRewardRate:  0.99,
		FeeSymbol:         "BOFI",
		MinRound:          9,
		WithdrawSingleMax: 5,
	})
	if StaticRewardRate != 0.99 || MinRound != 9 || WithdrawSingleMax != 5 {
		t.Fatalf("参数覆盖应生效：static=%v minRound=%v singleMax=%v", StaticRewardRate, MinRound, WithdrawSingleMax)
	}

	resetToCodeDefaults()

	if StaticRewardRate != codeDefaults.staticRewardRate {
		t.Fatalf("复位后静态收益率应回到默认 %v，实际 %v", codeDefaults.staticRewardRate, StaticRewardRate)
	}
	if MinRound != codeDefaults.minRound {
		t.Fatalf("复位后 MinRound 应回到默认 %v，实际 %v", codeDefaults.minRound, MinRound)
	}
	if WithdrawSingleMax != codeDefaults.withdrawSingleMax {
		t.Fatalf("复位后提现单笔上限应回到默认 %v，实际 %v", codeDefaults.withdrawSingleMax, WithdrawSingleMax)
	}
	if FeeSymbol != codeDefaults.feeSymbol {
		t.Fatalf("复位后手续费币种应回到默认 %v，实际 %v", codeDefaults.feeSymbol, FeeSymbol)
	}
}

func TestResetToCodeDefaults_RestoresStageSymbols(t *testing.T) {
	//手续费币种切换会就地改写 StageSymbols（阶段可参与币种列表）——复位必须能把它整体还原，
	//否则"切到 KTO 再删参数"会留下一个被改坏的阶段列表（旧实现正是这个坑）。
	orig := cloneStageSymbols(StageSymbols)
	defer func() {
		StageSymbols = orig
		FeeSymbol = codeDefaults.feeSymbol
	}()

	setFeeSymbol("KTO")
	if reflect.DeepEqual(StageSymbols, codeDefaults.stageSymbols) {
		t.Fatal("切换手续费币种后阶段列表应已变化（否则该用例没有验证到东西）")
	}

	resetToCodeDefaults()

	if !reflect.DeepEqual(StageSymbols, codeDefaults.stageSymbols) {
		t.Fatalf("复位后阶段币种列表应完全还原：\n期望 %v\n实际 %v", codeDefaults.stageSymbols, StageSymbols)
	}
	if FeeSymbol != codeDefaults.feeSymbol {
		t.Fatalf("复位后手续费币种应还原为 %v，实际 %v", codeDefaults.feeSymbol, FeeSymbol)
	}
}

func TestResetToCodeDefaults_IsIdempotent(t *testing.T) {
	orig := cloneStageSymbols(StageSymbols)
	defer func() { StageSymbols = orig }()

	resetToCodeDefaults()
	first := cloneStageSymbols(StageSymbols)
	resetToCodeDefaults()
	if !reflect.DeepEqual(first, StageSymbols) {
		t.Fatal("连续复位结果应完全一致（不能每次复位都把默认快照改坏）")
	}
}

func TestApplyNpowerParams_ZeroMeansNoOverride(t *testing.T) {
	orig := StaticRewardRate
	defer func() { StaticRewardRate = orig }()

	StaticRewardRate = 0.13
	ApplyNpowerParams(config.NpowerParams{StaticRewardRate: 0, LossRate: 0, MinRound: 0})
	if StaticRewardRate != 0.13 {
		t.Fatalf("0 值不应覆盖既有参数，实际 %v", StaticRewardRate)
	}
}

func TestResetThenApply_DeleteMeansBackToDefault(t *testing.T) {
	//这是修复的核心语义：先复位（= 配置键不存在），再应用一个**不含该键**的参数集，
	//结果必须等于代码默认值（修复前会保留上一次设过的值）。
	orig := StaticRewardRate
	defer func() { StaticRewardRate = orig }()

	ApplyNpowerParams(config.NpowerParams{StaticRewardRate: 0.42})
	resetToCodeDefaults()
	ApplyNpowerParams(config.NpowerParams{}) //等价于"DB 与 yml 里都没有这个键"
	if StaticRewardRate != codeDefaults.staticRewardRate {
		t.Fatalf("删掉参数键后应回到默认值 %v，实际 %v", codeDefaults.staticRewardRate, StaticRewardRate)
	}
}
