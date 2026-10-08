package main

import (
	"com.fibonacci.crowd/model"
	"gorm.io/gorm"
	"testing"
)

// ============================== 手续费销毁精确记账单测（2026-09-30 新增）==============================
//
// 背景：EVM 通道受合约 MIN_CIRCULATING(7777) 约束，链上可销毁余量可能小于"待销毁总量"，
// 此时 evmBurn 会按上限截断。原实现**无条件把整批记录标成已销毁** —— DB 与链上不一致，
// 剩余部分也永远不会重试（注释只写"需人工决定"）。修复后按实际销毁量 FIFO 拆分。
//
// 这些断言锁死拆分规则本身（纯函数，不需要链/DB）。

// fb 构造一条待销毁记录。注意 FeeBurn 内嵌 gorm.Model，ID 是提升字段，
// 组合字面量里必须写成 Model: gorm.Model{ID: ...}（不能直接写 ID:）。
func fb(id uint, symbol string, amount uint64) model.FeeBurn {
	return model.FeeBurn{Model: gorm.Model{ID: id}, Symbol: symbol, Amount: amount}
}

func TestSplitBurnRows_FullBurn(t *testing.T) {
	rows := []model.FeeBurn{fb(1, "TM", 100), fb(2, "TM", 200), fb(3, "TM", 300)}
	done, left := splitBurnRows(rows, "TM", 600)
	if len(done) != 3 || len(left) != 0 {
		t.Fatalf("足额销毁应全部标记：done=%d left=%d", len(done), len(left))
	}
}

func TestSplitBurnRows_TruncatedBurnKeepsRemainderPending(t *testing.T) {
	//待销毁 100+200+300=600，链上只允许 300 → FIFO：100+200 已销毁，300 留待下次
	rows := []model.FeeBurn{fb(1, "TM", 100), fb(2, "TM", 200), fb(3, "TM", 300)}
	done, left := splitBurnRows(rows, "TM", 300)
	if len(done) != 2 || done[0].ID != 1 || done[1].ID != 2 {
		t.Fatalf("应按 FIFO 标记前两条：%+v", done)
	}
	if len(left) != 1 || left[0].ID != 3 || left[0].Amount != 300 {
		t.Fatalf("剩余一条应留待下次：%+v", left)
	}
	//关键不变量：已销毁金额 == 链上实际销毁量（不允许"多标"）
	var sum uint64
	for _, r := range done {
		sum += r.Amount
	}
	if sum != 300 {
		t.Fatalf("已销毁合计应等于链上实际销毁量 300，实际 %d", sum)
	}
}

func TestSplitBurnRows_UnsplittableRowGoesPending(t *testing.T) {
	//单条金额就超过可销毁量：不做拆分（保持一条记录一个金额的账本语义），整条留待下次
	rows := []model.FeeBurn{fb(1, "TM", 100), fb(2, "TM", 500)}
	done, left := splitBurnRows(rows, "TM", 300)
	if len(done) != 1 || done[0].ID != 1 {
		t.Fatalf("只有第一条能被销毁：%+v", done)
	}
	if len(left) != 1 || left[0].ID != 2 {
		t.Fatalf("超量那条应留待下次：%+v", left)
	}
}

func TestSplitBurnRows_FiltersOtherSymbols(t *testing.T) {
	//只处理指定币种：别的币种记录既不算已销毁也不算"留待"（由各自的币种循环处理）
	rows := []model.FeeBurn{fb(1, "TM", 100), fb(2, "BOFI", 999), fb(3, "TM", 100)}
	done, left := splitBurnRows(rows, "TM", 200)
	if len(done) != 2 || len(left) != 0 {
		t.Fatalf("应只处理 TM：done=%d left=%d", len(done), len(left))
	}
	for _, r := range done {
		if r.Symbol != "TM" {
			t.Fatalf("混入了其它币种：%+v", r)
		}
	}
}

func TestSplitBurnRows_SortsByID(t *testing.T) {
	//乱序输入也必须按 id 先入先销（否则"实际销毁的是哪几条"不可复现）
	rows := []model.FeeBurn{fb(3, "TM", 300), fb(1, "TM", 100), fb(2, "TM", 200)}
	done, left := splitBurnRows(rows, "TM", 300)
	if len(done) != 2 || done[0].ID != 1 || done[1].ID != 2 {
		t.Fatalf("应按 id 升序取前缀：%+v", done)
	}
	if len(left) != 1 || left[0].ID != 3 {
		t.Fatalf("id=3 应留待下次：%+v", left)
	}
}

func TestSplitBurnRows_ZeroBurned(t *testing.T) {
	//链上一点都不能销毁（已到 7777 下限）：全部留待下次，且不得标记任何已销毁
	rows := []model.FeeBurn{fb(1, "TM", 100)}
	done, left := splitBurnRows(rows, "TM", 0)
	if len(done) != 0 || len(left) != 1 {
		t.Fatalf("可销毁量为 0 时不应标记已销毁：done=%d left=%d", len(done), len(left))
	}
}
