package evm

import (
	"bytes"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// 出金 calldata 编码单测（纯函数，不连链）。
// 运行：go test -p 1 -count=1 -run TestEncodeERC20Transfer ./utils/evm

func TestEncodeERC20Transfer(t *testing.T) {
	to := common.HexToAddress("0x41250bbf374c40378ceef7ac08a5d86058c1b076")

	//25.0（decimals=8）
	data := EncodeERC20Transfer(to, big.NewInt(250000000))

	//transfer(address,uint256) = 4 + 32 + 32 = 68 字节
	if len(data) != 68 {
		t.Fatalf("calldata 长度应为 68 字节，实际 %d", len(data))
	}
	if got := common.Bytes2Hex(data[:4]); got != "a9059cbb" {
		t.Fatalf("selector 应为 a9059cbb，实际 %s", got)
	}
	//地址左填充到 32 字节：前 12 字节为 0，后 20 字节是地址
	if got := common.Bytes2Hex(data[4:36]); got != "000000000000000000000000"+strings.ToLower(to.Hex()[2:]) {
		t.Fatalf("地址填充错误：%s", got)
	}
	//金额左填充到 32 字节
	want := "000000000000000000000000000000000000000000000000000000000ee6b280" //250000000
	if got := common.Bytes2Hex(data[36:68]); got != want {
		t.Fatalf("金额填充错误：期望 %s，实际 %s", want, got)
	}
}

func TestEncodeERC20TransferEdgeAmounts(t *testing.T) {
	to := common.HexToAddress("0x1111111111111111111111111111111111111111")

	//0 值也应编码为 32 字节全 0（不由本函数拦截，拦截在 SendERC20）
	data := EncodeERC20Transfer(to, big.NewInt(0))
	if got := common.Bytes2Hex(data[36:68]); got != strings.Repeat("0", 64) {
		t.Fatalf("0 值编码错误：%s", got)
	}

	//超大金额（超过 uint64）必须完整编码，不能被截断
	huge, _ := new(big.Int).SetString("1606938044258990275541962092341162602522202993782792835301376", 10)
	data = EncodeERC20Transfer(to, huge)
	if got := new(big.Int).SetBytes(data[36:68]); got.Cmp(huge) != 0 {
		t.Fatalf("超大金额编码丢失精度：期望 %s，实际 %s", huge, got)
	}
}

func TestSendERC20RejectsBadInputWithoutNetwork(t *testing.T) {
	to := common.HexToAddress("0x1111111111111111111111111111111111111111")

	//空 RPC 必须在拨号前就失败（不能出现「连不上所以算成功」）
	if _, err := SendERC20("", "0x2222222222222222222222222222222222222222", "aa", to.Hex(), big.NewInt(1), 12306); err == nil {
		t.Fatal("空 RPC 应报错")
	}
	//非法代币地址
	if _, err := SendERC20("http://127.0.0.1:1", "not-an-address", "aa", to.Hex(), big.NewInt(1), 12306); err == nil {
		t.Fatal("非法代币地址应报错")
	}
	//非法收款地址
	if _, err := SendERC20("http://127.0.0.1:1", "0x2222222222222222222222222222222222222222", "aa", "0x123", big.NewInt(1), 12306); err == nil {
		t.Fatal("非法收款地址应报错")
	}
	//金额为 0 / 负数：不能把 0 或负数打到链上
	if _, err := SendERC20("http://127.0.0.1:1", "0x2222222222222222222222222222222222222222", "aa", to.Hex(), big.NewInt(0), 12306); err == nil {
		t.Fatal("0 金额应报错")
	}
	if _, err := SendERC20("http://127.0.0.1:1", "0x2222222222222222222222222222222222222222", "aa", to.Hex(), big.NewInt(-1), 12306); err == nil {
		t.Fatal("负金额应报错")
	}
	//非法私钥
	if _, err := SendERC20("http://127.0.0.1:1", "0x2222222222222222222222222222222222222222", "zzzz", to.Hex(), big.NewInt(1), 12306); err == nil {
		t.Fatal("非法私钥应报错")
	}
}

// ============================== 手续费销毁（burn）相关 ==============================

// selector 常量必须与 keccak 计算结果一致——手抄的十六进制一旦写错，
// 链上会调用到一个不存在的函数（交易 revert 或直接成功但不做任何事），必须由测试钉死。
func TestSelectorConstants(t *testing.T) {
	cases := []struct {
		name string
		sig  string
		got  []byte
	}{
		{"transfer(address,uint256)", "transfer(address,uint256)", erc20TransferSelector},
		{"burn(uint256)", "burn(uint256)", erc20BurnSelector},
		{"balanceOf(address)", "balanceOf(address)", erc20BalanceOfSelector},
		{"decimals()", "decimals()", erc20DecimalsSelector},
		{"totalSupply()", "totalSupply()", erc20TotalSupplySelector},
	}
	for _, c := range cases {
		want := crypto.Keccak256([]byte(c.sig))[:4]
		if !bytes.Equal(c.got, want) {
			t.Fatalf("%s selector 错误：期望 %x，实际 %x", c.name, want, c.got)
		}
		if len(c.got) != 4 {
			t.Fatalf("%s selector 长度应为 4，实际 %d", c.name, len(c.got))
		}
	}
}

func TestEncodeERC20Burn(t *testing.T) {
	//25.0（decimals=8）
	data := EncodeERC20Burn(big.NewInt(250000000))

	if len(data) != 36 {
		t.Fatalf("burn calldata 长度应为 36 字节（4+32），实际 %d", len(data))
	}
	if got := common.Bytes2Hex(data[:4]); got != "42966c68" {
		t.Fatalf("selector 应为 42966c68，实际 %s", got)
	}
	want := "000000000000000000000000000000000000000000000000000000000ee6b280"
	if got := common.Bytes2Hex(data[4:36]); got != want {
		t.Fatalf("金额填充错误：期望 %s，实际 %s", want, got)
	}

	//nil 金额按 0 编码（不 panic；调用方负责拦截 0）
	if got := common.Bytes2Hex(EncodeERC20Burn(nil)[4:36]); got != strings.Repeat("0", 64) {
		t.Fatalf("nil 金额应编码为全 0，实际 %s", got)
	}

	//超大金额（>uint64）必须完整编码，不能被截断（7777 枚下限计算会用到 big.Int 余量）
	huge, _ := new(big.Int).SetString("1606938044258990275541962092341162602522202993782792835301376", 10)
	data = EncodeERC20Burn(huge)
	if got := new(big.Int).SetBytes(data[4:36]); got.Cmp(huge) != 0 {
		t.Fatalf("超大金额编码丢失精度：期望 %s，实际 %s", huge, got)
	}
}

func TestSendERC20BurnRejectsBadInputWithoutNetwork(t *testing.T) {
	//burn 没有收款地址，但空 RPC / 非法合约地址 / 非正金额 / 非法私钥同样必须在拨号前失败
	if _, err := SendERC20Burn("", "0x2222222222222222222222222222222222222222", "aa", big.NewInt(1), 31337); err == nil {
		t.Fatal("空 RPC 应报错")
	}
	if _, err := SendERC20Burn("http://127.0.0.1:1", "not-an-address", "aa", big.NewInt(1), 31337); err == nil {
		t.Fatal("非法合约地址应报错")
	}
	if _, err := SendERC20Burn("http://127.0.0.1:1", "0x2222222222222222222222222222222222222222", "aa", big.NewInt(0), 31337); err == nil {
		t.Fatal("0 金额应报错（销毁 0 毫无意义且白付 gas）")
	}
	if _, err := SendERC20Burn("http://127.0.0.1:1", "0x2222222222222222222222222222222222222222", "aa", big.NewInt(-5), 31337); err == nil {
		t.Fatal("负金额应报错")
	}
	if _, err := SendERC20Burn("http://127.0.0.1:1", "0x2222222222222222222222222222222222222222", "zzzz", big.NewInt(1), 31337); err == nil {
		t.Fatal("非法私钥应报错")
	}
}

func TestCallContractRejectsBadInput(t *testing.T) {
	if _, err := CallContract("", "0x2222222222222222222222222222222222222222", erc20TotalSupplySelector); err == nil {
		t.Fatal("空 RPC 应报错")
	}
	if _, err := CallContract("http://127.0.0.1:1", "0x123", erc20TotalSupplySelector); err == nil {
		t.Fatal("非法合约地址应报错")
	}
	if _, err := ReadERC20BalanceOf("http://127.0.0.1:1", "0x2222222222222222222222222222222222222222", "0x123"); err == nil {
		t.Fatal("非法查询地址应报错")
	}
}
