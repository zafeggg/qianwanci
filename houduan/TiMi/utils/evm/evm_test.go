package evm

import (
	"errors"
	"math"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// B 模块纯函数单测：不连链、不连库。
// 运行：go test -p 1 -count=1 -v ./utils/evm

// transferLog 构造一笔标准 ERC20 Transfer 日志
func transferLog(from, to common.Address, value *big.Int) types.Log {
	return types.Log{
		Topics: []common.Hash{
			common.HexToHash(TransferTopic),
			common.BytesToHash(common.LeftPadBytes(from.Bytes(), common.HashLength)),
			common.BytesToHash(common.LeftPadBytes(to.Bytes(), common.HashLength)),
		},
		Data: common.LeftPadBytes(value.Bytes(), common.HashLength),
	}
}

func TestDecodeERC20TransferOK(t *testing.T) {
	from := common.HexToAddress("0x1111111111111111111111111111111111111111")
	to := common.HexToAddress("0x2222222222222222222222222222222222222222")
	value := big.NewInt(123456789)

	gotFrom, gotTo, gotValue, err := DecodeERC20Transfer(transferLog(from, to, value))
	if err != nil {
		t.Fatalf("正常 Transfer 不应报错：%v", err)
	}
	if gotFrom != from {
		t.Fatalf("from 解析错误：期望 %s，实际 %s", from.Hex(), gotFrom.Hex())
	}
	if gotTo != to {
		t.Fatalf("to 解析错误：期望 %s，实际 %s", to.Hex(), gotTo.Hex())
	}
	if gotValue.Cmp(value) != 0 {
		t.Fatalf("value 解析错误：期望 %s，实际 %s", value, gotValue)
	}
}

func TestDecodeRejectsWrongTopicCount(t *testing.T) {
	from := common.HexToAddress("0x11")
	to := common.HexToAddress("0x22")
	lg := transferLog(from, to, big.NewInt(1))

	//ERC721 的 Transfer 有 4 个 topic，必须被挡掉
	lg.Topics = append(lg.Topics, common.HexToHash("0x01"))
	if _, _, _, err := DecodeERC20Transfer(lg); !errors.Is(err, ErrInvalidTopicCount) {
		t.Fatalf("topic 数量为 4 应返回 ErrInvalidTopicCount，实际 %v", err)
	}

	lg.Topics = lg.Topics[:2]
	if _, _, _, err := DecodeERC20Transfer(lg); !errors.Is(err, ErrInvalidTopicCount) {
		t.Fatalf("topic 数量为 2 应返回 ErrInvalidTopicCount，实际 %v", err)
	}
}

func TestDecodeRejectsWrongTopic0(t *testing.T) {
	from := common.HexToAddress("0x11")
	to := common.HexToAddress("0x22")
	lg := transferLog(from, to, big.NewInt(1))
	lg.Topics[0] = common.HexToHash("0xdeadbeef")

	if _, _, _, err := DecodeERC20Transfer(lg); !errors.Is(err, ErrTopicMismatch) {
		t.Fatalf("topic0 不匹配应返回 ErrTopicMismatch，实际 %v", err)
	}
}

func TestDecodeZeroValueAndEmptyData(t *testing.T) {
	from := common.HexToAddress("0x11")
	to := common.HexToAddress("0x22")

	lg := transferLog(from, to, big.NewInt(0))
	_, _, v, err := DecodeERC20Transfer(lg)
	if err != nil || v.Sign() != 0 {
		t.Fatalf("0 值转账应为 0 且无错，实际 v=%v err=%v", v, err)
	}

	//部分代理合约会省略零值 data：按 0 处理而不是报错（硬报错会漏单）
	lg.Data = nil
	_, _, v, err = DecodeERC20Transfer(lg)
	if err != nil || v.Sign() != 0 {
		t.Fatalf("空 data 应视为 0，实际 v=%v err=%v", v, err)
	}
}

func TestDecodeRejectsBadDataLength(t *testing.T) {
	lg := transferLog(common.HexToAddress("0x11"), common.HexToAddress("0x22"), big.NewInt(1))
	lg.Data = []byte{1, 2, 3}
	if _, _, _, err := DecodeERC20Transfer(lg); !errors.Is(err, ErrInvalidDataLength) {
		t.Fatalf("data 长度非 0/32 应返回 ErrInvalidDataLength，实际 %v", err)
	}
}

func TestDecodeHugeValueBeyondUint64(t *testing.T) {
	//2^200 远超 uint64：必须用 big.Int 全精度解析，不能被截断
	value, _ := new(big.Int).SetString("1606938044258990275541962092341162602522202993782792835301376", 10)
	_, _, got, err := DecodeERC20Transfer(transferLog(
		common.HexToAddress("0x11"), common.HexToAddress("0x22"), value))
	if err != nil {
		t.Fatalf("超大金额不应报错：%v", err)
	}
	if got.Cmp(value) != 0 {
		t.Fatalf("超大金额精度丢失：期望 %s，实际 %s", value, got)
	}
}

func TestNormalizeAddressVariants(t *testing.T) {
	want := common.HexToAddress("0xAbC0000000000000000000000000000000000001")

	cases := []struct {
		name string
		in   string
	}{
		{"带 0x 前缀", "0xAbC0000000000000000000000000000000000001"},
		{"不带前缀", "AbC0000000000000000000000000000000000001"},
		{"全小写", "0xabc0000000000000000000000000000000000001"},
		{"全大写", "0XABC0000000000000000000000000000000000001"},
		{"两端空白", "  0xAbC0000000000000000000000000000000000001  "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NormalizeAddress(c.in)
			if err != nil {
				t.Fatalf("应解析成功，实际报错 %v", err)
			}
			if got != want {
				t.Fatalf("期望 %s，实际 %s", want.Hex(), got.Hex())
			}
		})
	}

	for _, bad := range []string{
		"",      //空
		"0x",    //只有前缀
		"0xabc", //太短
		"0xabc00000000000000000000000000000000000011", //太长
		"0xzzc0000000000000000000000000000000000001",  //非 hex
	} {
		if _, err := NormalizeAddress(bad); !errors.Is(err, ErrInvalidAddress) {
			t.Fatalf("非法地址 %q 应返回 ErrInvalidAddress，实际 %v", bad, err)
		}
	}
}

func TestAmountRoundTrip(t *testing.T) {
	//decimals=8（TM/FIBO 记账精度）与 18（标准 ERC20）都要能往返一致
	for _, dec := range []uint8{8, 18} {
		for _, amount := range []float64{0, 1, 0.1, 3.4, 7777, 123456.789} {
			wei := MainUnitToAmount(amount, dec)
			if wei == nil {
				t.Fatalf("decimals=%d amount=%v 换算返回 nil", dec, amount)
			}
			back := AmountToMainUnit(wei, dec)
			if math.Abs(back-amount) > 1e-6 {
				t.Fatalf("decimals=%d 往返不一致：%v → %s → %v", dec, amount, wei, back)
			}
		}
	}
}

func TestMainUnitToAmountRounding(t *testing.T) {
	//0.1 的 float64 近似值略小于真实 0.1，若用截断会少 1 个最小单位（9999999）；必须四舍五入
	if got := MainUnitToAmount(0.1, 8).String(); got != "10000000" {
		t.Fatalf("0.1 * 1e8 应为 10000000（四舍五入），实际 %s", got)
	}
	//0.30000000000000004（0.1+0.2）也不应多出最小单位
	if got := MainUnitToAmount(0.1+0.2, 8).String(); got != "30000000" {
		t.Fatalf("(0.1+0.2) * 1e8 应为 30000000，实际 %s", got)
	}
	if got := MainUnitToAmount(1.006, 2).String(); got != "101" {
		t.Fatalf("1.006 * 100 应为 101（四舍五入），实际 %s", got)
	}
	//边界：1.005 的 float64 表示实际略小于 1.005，四舍五入后应落在 100（而不是 101）——
	//这不是 bug，是 IEEE754 的客观事实，写进测试是为了固定这个预期行为。
	if got := MainUnitToAmount(1.005, 2).String(); got != "100" {
		t.Fatalf("1.005（float64 实际略小）* 100 应为 100，实际 %s", got)
	}
}

func TestMainUnitToAmountRejectsInvalid(t *testing.T) {
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1, -0.0001} {
		if got := MainUnitToAmount(bad, 8); got != nil {
			t.Fatalf("非法金额 %v 应返回 nil，实际 %s", bad, got)
		}
	}
}

func TestAmountToMainUnitNil(t *testing.T) {
	if got := AmountToMainUnit(nil, 8); got != 0 {
		t.Fatalf("nil 输入应返回 0，实际 %v", got)
	}
}

func TestDecodeThenNormalizeRealisticLog(t *testing.T) {
	//端到端：从「链上日志形态」解出地址与金额，并按 decimals=8 换算成主单位
	pool := common.HexToAddress("0x41250bbf374c40378ceef7ac08a5d86058c1b076")
	user := common.HexToAddress("0x9a1b2c3d4e5f60718293a4b5c6d7e8f901234567")
	value := big.NewInt(2_500_000_000) //25.0（decimals=8 → 25 × 1e8）

	from, to, v, err := DecodeERC20Transfer(transferLog(user, pool, value))
	if err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	if from != user || to != pool {
		t.Fatalf("地址解析错误：from=%s to=%s", from.Hex(), to.Hex())
	}

	norm, err := NormalizeAddress(strings.ToUpper(pool.Hex()))
	if err != nil {
		t.Fatalf("归一化失败：%v", err)
	}
	if norm != pool {
		t.Fatalf("归一化后地址不一致：%s vs %s", norm.Hex(), pool.Hex())
	}
	if got := AmountToMainUnit(v, 8); math.Abs(got-25.0) > 1e-9 {
		t.Fatalf("金额换算错误：期望 25，实际 %v", got)
	}
}

// TestAmountPrecisionBoundary 固定 float64 的精度边界。
// 2026-09-18 用 FIBO 主网真实日志跑探针时发现：一笔 2.49e20 的最小单位金额经
// AmountToMainUnit → MainUnitToAmount 往返后低位发生变化（float64 只有 53 位整数精度）。
// 这不是实现 bug，是 float64 的客观能力上界；写进测试是为了让后人知道「大额别走 float64」。
func TestAmountPrecisionBoundary(t *testing.T) {
	//2^53 = 9007199254740992，float64 能精确表示的最大连续整数
	exact := new(big.Int).SetUint64(1 << 53)
	if got := MainUnitToAmount(AmountToMainUnit(exact, 8), 8); got.Cmp(exact) != 0 {
		t.Fatalf("2^53 以内应能往返一致：%s -> %s", exact, got)
	}

	//真实链上量级（> 2^53）必然丢低位
	huge, _ := new(big.Int).SetString("249176573037905561610", 10)
	got := MainUnitToAmount(AmountToMainUnit(huge, 8), 8)
	if got.Cmp(huge) == 0 {
		t.Fatal("超过 2^53 的值本应丢精度却往返一致——前提变了，需重新评估换算实现")
	}
	t.Logf("预期内的精度损失（float64 上界所致）：%s -> %s", huge, got)
}
