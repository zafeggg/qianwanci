// Package evm 提供 FIBO(EVM) 链的最小必要工具：ERC20 Transfer 事件解码、地址归一化、金额单位换算，
// 以及（send.go）ERC20 出金广播。定位与 utils/tron 一致：只做纯工具，不持有状态、不连数据库。
//
// 为什么单独建包而不是塞进 utils/tron：tron 包依赖 TronGrid HTTP 与 tron 专有编码，
// EVM 侧用 go-ethereum 的 types/common/abi，两者依赖与错误语义完全不同，混在一起会互相牵连。
package evm

import (
	"encoding/hex"
	"errors"
	"math"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// TransferTopic = keccak256("Transfer(address,address,uint256)")
// = 0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef
// ERC20 标准的 Transfer 事件签名，也是 ERC721 的同名事件（后者 topics 为 4 个，会被下面的
// len(topics)==3 校验挡掉——这正是我们想要的行为：本模块只处理 ERC20）。
const TransferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

// 解码/换算相关的哨兵错误。用 errors.Is 判断，便于上层区分「数据不合法」与「网络/DB 错误」。
var (
	// ErrInvalidTopicCount topic 数量不是 3（非标准 ERC20 Transfer，例如 ERC721 有 4 个 topic）
	ErrInvalidTopicCount = errors.New("evm: transfer 日志 topic 数量必须为 3")
	// ErrTopicMismatch topic0 不是 Transfer 事件签名
	ErrTopicMismatch = errors.New("evm: topic0 不是 ERC20 Transfer 事件签名")
	// ErrInvalidDataLength Data 长度既不是 0 也不是 32 字节
	ErrInvalidDataLength = errors.New("evm: transfer 日志 data 长度必须为 0 或 32 字节")
	// ErrInvalidAddress 地址字符串非法（非 hex / 长度不对）
	ErrInvalidAddress = errors.New("evm: 非法地址")
	// ErrInvalidAmount 金额非法（NaN/Inf/负数）
	ErrInvalidAmount = errors.New("evm: 非法金额")
)

// DecodeERC20Transfer 解码一笔 ERC20 Transfer 日志。
//
// 校验：len(log.Topics) == 3 且 log.Topics[0] == TransferTopic，否则返回明确错误。
// from/to 取 topics[1]/topics[2] 的末 20 字节（topic 是 32 字节左填充地址，
// 前 12 字节是零填充；不校验这 12 字节是否为零——有些非标准合约会塞脏数据，
// 强制校验会把合法转账拒之门外，末 20 字节才是权威口径）。
//
// value 口径（规格允许自行选定，这里选定为「Data 为空 → 0；长度非 32 → 报错」）：
//   - len(Data) == 0：视为 0（某些链上裁剪/代理合约会省略零值 data，硬报错会漏单）
//   - len(Data) == 32：按大端 uint256 解析
//   - 其它长度：报错。ERC20 规范规定 value 就是 32 字节 ABI 编码，
//     长度不对说明这不是一笔可安全入账的转账，宁可不入账也不能猜。
func DecodeERC20Transfer(log types.Log) (from, to common.Address, value *big.Int, err error) {
	if len(log.Topics) != 3 {
		return from, to, nil, ErrInvalidTopicCount
	}
	if !strings.EqualFold(log.Topics[0].Hex(), TransferTopic) {
		return from, to, nil, ErrTopicMismatch
	}

	// topic 本身是 32 字节定长，common.BytesToAddress 会自动取末 20 字节；
	// 这里显式切片，避免以后有人误传短切片时行为不明确。
	from = common.BytesToAddress(log.Topics[1].Bytes()[common.HashLength-common.AddressLength:])
	to = common.BytesToAddress(log.Topics[2].Bytes()[common.HashLength-common.AddressLength:])

	switch len(log.Data) {
	case 0:
		value = new(big.Int) // 0
	case common.HashLength: // 32
		value = new(big.Int).SetBytes(log.Data)
	default:
		return from, to, nil, ErrInvalidDataLength
	}
	return from, to, value, nil
}

// NormalizeAddress 把用户/配置里写的地址字符串统一成 common.Address。
//
// 兼容：带或不带 0x 前缀、全小写、全大写、EIP-55 混合大小写。
// 统一返回小写形式（common.Address.Hex() 本身就是小写），与库内 wallet.address 的存储口径一致。
// 不做 EIP-55 校验和校验：本地私链/测试地址常常不带校验和，强制校验会误伤；
// 真正的合法性由「40 位 hex」这一条保证。
func NormalizeAddress(s string) (common.Address, error) {
	var zero common.Address
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "0x") || strings.HasPrefix(t, "0X") {
		t = t[2:]
	}
	if len(t) != common.AddressLength*2 { // 40
		return zero, ErrInvalidAddress
	}
	bs, err := hex.DecodeString(t)
	if err != nil {
		return zero, ErrInvalidAddress
	}
	return common.BytesToAddress(bs), nil
}

// convPrec 换算用的 big.Float 精度（bit）。
// 默认 53 bit（float64）在 decimals=18 且金额较大时会丢精度：
// 例如 123456.789 * 1e18 需要约 77 bit 有效位，53 bit 下绝对误差可达 1e7 个最小单位（≈0.14 枚）。
// 用 256 bit 后，float64 入参本身可被精确表示，乘积也精确，往返换算不再额外引入误差。
const convPrec = 256

// AmountToMainUnit 最小单位（wei）转主单位展示值。
// 例如 decimals=8 时 123456789 → 1.23456789。
//
// ⚠ 精度边界（2026-09-18 用 FIBO 主网真实日志实测）：返回值是 float64，只有 53 位整数精度
// （约 9.007e15），**超过该量级就会丢低位**。实测一笔 2.49e20 的转账往返后低位发生变化。
// 因此本函数只适合「展示 / 小额入账」，**不可用于需要精确对账的大额换算**；
// 精确计算一律用 big.Int（如 EncodeERC20Transfer 的入参）。
func AmountToMainUnit(v *big.Int, decimals uint8) float64 {
	if v == nil {
		return 0
	}
	num := new(big.Float).SetPrec(convPrec).SetInt(v)
	den := new(big.Float).SetPrec(convPrec).SetInt(unitFactor(decimals))
	out, _ := new(big.Float).SetPrec(convPrec).Quo(num, den).Float64()
	return out
}

// MainUnitToAmount 主单位转最小单位（用于出金金额打包）。
//
// 为什么用四舍五入而不是截断：float64 无法精确表示 0.1 这类十进制小数
// （0.1 的二进制近似值可能略大于或略小于真实值），截断会把 0.1 * 1e8 变成 9999999，
// 少发 1 个最小单位；四舍五入对「用户输入精度 ≤ decimals」的正常输入能保证往返一致。
//
// ⚠ 往返一致只在 float64 的 53 位整数精度内成立（约 9.007e15 个最小单位）；
// 更大的值本身就不是 float64 能无损表达的，调用方应直接使用 big.Int。
//
// 非法输入（NaN/Inf/负数）返回 nil，避免把脏金额打到链上。
func MainUnitToAmount(amount float64, decimals uint8) *big.Int {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
		return nil
	}
	scaled := new(big.Float).SetPrec(convPrec).SetFloat64(amount)
	scaled.Mul(scaled, new(big.Float).SetPrec(convPrec).SetInt(unitFactor(decimals)))

	// 四舍五入到整数：big.Float 没有直接能力，转成 big.Int 前先判断小数部分是否 >= 0.5。
	intPart, _ := scaled.Int(nil)
	frac := new(big.Float).SetPrec(convPrec).Sub(scaled, new(big.Float).SetPrec(convPrec).SetInt(intPart))
	if frac.Cmp(big.NewFloat(0.5)) >= 0 {
		intPart.Add(intPart, big.NewInt(1))
	}
	return intPart
}

// unitFactor 10^decimals。decimals 上限按 ERC20 常见值给到 18，更大的值在 float64 下也无意义。
func unitFactor(decimals uint8) *big.Int {
	// 用 big.Int.Exp 而不是 math.Pow10：decimals=18 时 1e18 已超出 float64 的整数精确范围，
	// 走浮点再转 big.Int 会丢掉低位（金额精度问题，必须用整数幂）。
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
}
