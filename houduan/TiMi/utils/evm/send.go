package evm

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// ============================== B 模块：ERC20 出金广播 / 合约调用 ==============================
// 只做「签名 + 广播」这一件事，不持有热钱包状态、不查数据库。
//
// 设计取舍：
//   - 用 legacy 交易（types.NewTransaction）而非 EIP-1559：FIBO 主网按 GYT/ZYT 既有口径走 type:0，
//     发 1559 交易可能被节点拒收。
//   - 不用 accounts/abi/bind：那会把 keystore/usbwallet 等一大票依赖拉进来；
//     transfer(address,uint256) 的 calldata 只有固定 68 字节，手写更可控。
//   - gasLimit 先 EstimateGas，失败则回落 100000（ERC20 转账典型 5~6 万，留足余量）。
//   - **任何失败都必须原样返回错误**，绝不能在链上结果未知时当作成功。

// erc20TransferSelector = keccak256("transfer(address,uint256)") 的前 4 字节
var erc20TransferSelector = []byte{0xa9, 0x05, 0x9c, 0xbb}

// erc20BurnSelector = keccak256("burn(uint256)") 的前 4 字节 = 0x42966c68
// TMToken 覆写了 burn：销毁调用者自身持仓，并在合约层强制「流通量不低于 7777 枚」。
var erc20BurnSelector = []byte{0x42, 0x96, 0x6c, 0x68}

// erc20BalanceOfSelector = keccak256("balanceOf(address)") 的前 4 字节 = 0x70a08231
var erc20BalanceOfSelector = []byte{0x70, 0xa0, 0x82, 0x31}

// erc20DecimalsSelector = keccak256("decimals()") 的前 4 字节 = 0x313ce567
var erc20DecimalsSelector = []byte{0x31, 0x3c, 0xe5, 0x67}

// erc20TotalSupplySelector = keccak256("totalSupply()") 的前 4 字节 = 0x18160ddd
// 手续费销毁用：TMToken 的 circulatingSupply() 就是 totalSupply()，
// 直接读标准 ERC20 的 totalSupply 即可，无需依赖项目自定义 ABI。
var erc20TotalSupplySelector = []byte{0x18, 0x16, 0x0d, 0xdd}

// burnWaitTimeout 广播后等待回执的总时长。私链秒级出块，主网 12s/块，
// 90s 足够覆盖多数情况；超时不算「失败已确认」，而是「状态未知」，由调用方按需人工核查。
const burnWaitTimeout = 90 * time.Second

var (
	ErrInvalidPrivateKey = errors.New("evm: 私钥非法")
	// ErrNonPositiveAmount 出金金额必须为正（0 或负数不允许打到链上）
	ErrNonPositiveAmount = errors.New("evm: 出金金额必须为正数")
	// ErrTxReverted 交易已上链但执行失败（receipt.status = 0），通常意味着合约 require 未通过
	ErrTxReverted = errors.New("evm: 交易已上链但执行失败（revert）")
	// ErrTxStatusUnknown 广播成功但等待回执超时：链上结果未知，**不能当作失败重发**
	ErrTxStatusUnknown = errors.New("evm: 等待交易回执超时，链上结果未知")
)

// EncodeERC20Transfer 生成 transfer(to, amount) 的 calldata（68 字节）
func EncodeERC20Transfer(to common.Address, amount *big.Int) []byte {
	data := make([]byte, 0, 4+32+32)
	data = append(data, erc20TransferSelector...)
	data = append(data, common.LeftPadBytes(to.Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(amount.Bytes(), 32)...)
	return data
}

// EncodeERC20Burn 生成 burn(uint256) 的 calldata（36 字节）。
// amount 为 0 也会正常编码（由调用方在发起前拦截，避免白付 gas）。
func EncodeERC20Burn(amount *big.Int) []byte {
	if amount == nil {
		amount = new(big.Int)
	}
	data := make([]byte, 0, 4+32)
	data = append(data, erc20BurnSelector...)
	data = append(data, common.LeftPadBytes(amount.Bytes(), 32)...)
	return data
}

// SendERC20 从热钱包把 amount（最小单位）转给 to，返回 txHash。
// rpcUrl 为空、私钥非法、金额非正、地址非法都会立即返回错误（不发起广播）。
//
// 语义变更（2026-09-19 私链联调）：**广播后会等待回执**，
// 交易 revert（receipt.status=0）返回 ErrTxReverted，等待超时返回 ErrTxStatusUnknown。
// 原实现只要 SendTransaction 不报错就算成功，revert 会被当成出金成功记账（链上其实没转）。
func SendERC20(rpcUrl, tokenAddr, privateKeyHex, toAddr string, amount *big.Int, chainID int64) (string, error) {
	token, to, priv, err := prepareERC20(rpcUrl, tokenAddr, privateKeyHex, toAddr, amount)
	if err != nil {
		return "", err
	}
	return sendERC20Call(rpcUrl, token, priv, EncodeERC20Transfer(to, amount), chainID)
}

// SendERC20Burn 调用代币合约的 burn(uint256)，销毁 from（私钥持有者）自身持仓。
// 用于千万次手续费通缩：TMToken.burn 在合约层强制「流通量不得低于 7777 枚」。
func SendERC20Burn(rpcUrl, tokenAddr, privateKeyHex string, amount *big.Int, chainID int64) (string, error) {
	token, _, priv, err := prepareERC20(rpcUrl, tokenAddr, privateKeyHex, "", amount)
	if err != nil {
		return "", err
	}
	return sendERC20Call(rpcUrl, token, priv, EncodeERC20Burn(amount), chainID)
}

// prepareERC20 统一入参校验：空 RPC / 非法合约地址 / 非法收款地址 / 非正金额 / 非法私钥
// 全部在**拨号之前**失败（不能出现「连不上所以算成功」这类静默通过）。
// toAddr 传空表示「不需要收款地址」（burn 场景）。
func prepareERC20(rpcUrl, tokenAddr, privateKeyHex, toAddr string, amount *big.Int) (token common.Address, to common.Address, priv *ecdsa.PrivateKey, err error) {
	if strings.TrimSpace(rpcUrl) == "" {
		err = errors.New("evm: 未配置 RPC 地址")
		return
	}
	if token, err = NormalizeAddress(tokenAddr); err != nil {
		err = fmt.Errorf("evm: 代币合约地址非法: %w", err)
		return
	}
	if toAddr != "" {
		if to, err = NormalizeAddress(toAddr); err != nil {
			err = fmt.Errorf("evm: 收款地址非法: %w", err)
			return
		}
	}
	if amount == nil || amount.Sign() <= 0 {
		err = ErrNonPositiveAmount
		return
	}
	key := strings.TrimPrefix(strings.TrimSpace(privateKeyHex), "0x")
	if priv, err = crypto.HexToECDSA(key); err != nil {
		err = ErrInvalidPrivateKey
		return
	}
	return
}

// keyLocks 按「发送方地址」串行化 nonce 获取+广播。
// 同一热钱包并发出金时，两笔都可能用 PendingNonceAt 拿到同一个 nonce，
// 结果是后一笔被节点拒收（或相互覆盖）——记账已发生、链上只出去一笔，属于资金错账。
// 串行化是这里最简单且足够安全的处置（出金不是高频路径）。
var (
	keyLocksMu sync.Mutex
	keyLocks   = map[string]*sync.Mutex{}
)

func lockForKey(from common.Address) func() {
	keyLocksMu.Lock()
	l, ok := keyLocks[from.Hex()]
	if !ok {
		l = &sync.Mutex{}
		keyLocks[from.Hex()] = l
	}
	keyLocksMu.Unlock()

	l.Lock()
	return l.Unlock
}

// sendERC20Call 广播一笔对代币合约的调用并等待回执。
func sendERC20Call(rpcUrl string, token common.Address, priv *ecdsa.PrivateKey, data []byte, chainID int64) (string, error) {
	from := crypto.PubkeyToAddress(priv.PublicKey)

	cli, err := ethclient.Dial(rpcUrl)
	if err != nil {
		return "", fmt.Errorf("evm: 连接 RPC 失败: %w", err)
	}
	defer cli.Close()

	reply := lockForKey(from)
	defer reply()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	//链 ID：以配置为准；为 0 时向节点问一次（避免签名链 ID 错导致交易被全网拒收）
	if chainID == 0 {
		id, cerr := cli.ChainID(ctx)
		if cerr != nil {
			return "", fmt.Errorf("evm: 获取 chainId 失败: %w", cerr)
		}
		chainID = id.Int64()
	}

	nonce, err := cli.PendingNonceAt(ctx, from)
	if err != nil {
		return "", fmt.Errorf("evm: 获取 nonce 失败: %w", err)
	}
	gasPrice, err := cli.SuggestGasPrice(ctx)
	if err != nil {
		return "", fmt.Errorf("evm: 获取 gasPrice 失败: %w", err)
	}

	gasLimit := uint64(100000)
	if est, eerr := cli.EstimateGas(ctx, ethereum.CallMsg{
		From: from, To: &token, Value: big.NewInt(0), Data: data,
	}); eerr == nil && est > 0 {
		gasLimit = est * 12 / 10 //留 20% 余量，防止链上状态微变导致 out of gas
	}

	tx := types.NewTransaction(nonce, token, big.NewInt(0), gasLimit, gasPrice, data)
	signed, err := types.SignTx(tx, types.NewEIP155Signer(big.NewInt(chainID)), priv)
	if err != nil {
		return "", fmt.Errorf("evm: 交易签名失败: %w", err)
	}
	if err := cli.SendTransaction(ctx, signed); err != nil {
		return "", fmt.Errorf("evm: 广播失败: %w", err)
	}

	hash := signed.Hash().Hex()
	if err := waitReceipt(cli, signed.Hash()); err != nil {
		//失败也把 hash 带出去：回执超时/失败时调用方需要它去链上人工核查
		return hash, err
	}
	return hash, nil
}

// waitReceipt 轮询等待交易回执并判定执行结果。
// 只有 status=1 才算成功；超时返回 ErrTxStatusUnknown（链上结果未知，不可当作失败重发）。
func waitReceipt(cli *ethclient.Client, hash common.Hash) error {
	deadline := time.Now().Add(burnWaitTimeout)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		receipt, err := cli.TransactionReceipt(ctx, hash)
		cancel()

		if err == nil && receipt != nil {
			if receipt.Status == types.ReceiptStatusSuccessful {
				return nil
			}
			return fmt.Errorf("%w (tx=%s, block=%d)", ErrTxReverted, hash.Hex(), receipt.BlockNumber)
		}
		//ethereum.NotFound = 还没被打包，继续等；其它错误也继续等到超时
		if time.Now().After(deadline) {
			return fmt.Errorf("%w (tx=%s)", ErrTxStatusUnknown, hash.Hex())
		}
		time.Sleep(2 * time.Second)
	}
}

// ReadERC20Decimals 读取代币精度（配置里的精度写错会导致记账差几个数量级，出金前应校验一次）
func ReadERC20Decimals(rpcUrl, tokenAddr string) (uint8, error) {
	out, err := CallContract(rpcUrl, tokenAddr, erc20DecimalsSelector)
	if err != nil {
		return 0, err
	}
	if len(out) < 32 {
		return 0, errors.New("evm: decimals() 返回数据过短")
	}
	v := new(big.Int).SetBytes(out)
	if !v.IsUint64() || v.Uint64() > 255 {
		return 0, errors.New("evm: decimals() 返回值超出 uint8")
	}
	return uint8(v.Uint64()), nil
}

// ReadERC20BalanceOf 读取某地址的代币余额（最小单位）
func ReadERC20BalanceOf(rpcUrl, tokenAddr, holder string) (*big.Int, error) {
	addr, err := NormalizeAddress(holder)
	if err != nil {
		return nil, fmt.Errorf("evm: 查询地址非法: %w", err)
	}
	data := append(append([]byte{}, erc20BalanceOfSelector...), common.LeftPadBytes(addr.Bytes(), 32)...)
	out, err := CallContract(rpcUrl, tokenAddr, data)
	if err != nil {
		return nil, err
	}
	if len(out) < 32 {
		return nil, errors.New("evm: balanceOf() 返回数据过短")
	}
	return new(big.Int).SetBytes(out), nil
}

// ReadERC20TotalSupply 读取代币总供应量（最小单位）。
// 对 TMToken 而言 totalSupply 即流通量（销毁真实减量），用于计算还可销毁多少（7777 下限）。
func ReadERC20TotalSupply(rpcUrl, tokenAddr string) (*big.Int, error) {
	out, err := CallContract(rpcUrl, tokenAddr, erc20TotalSupplySelector)
	if err != nil {
		return nil, err
	}
	if len(out) < 32 {
		return nil, errors.New("evm: totalSupply() 返回数据过短")
	}
	return new(big.Int).SetBytes(out), nil
}

// PrivateKeyToAddress 从私钥（带或不带 0x）推导地址。
// 用于配置体检：出金热钱包与充值收款池是否同址（同址时监听必须过滤 pool→用户 的出金交易）。
func PrivateKeyToAddress(privateKeyHex string) (common.Address, error) {
	key := strings.TrimPrefix(strings.TrimSpace(privateKeyHex), "0x")
	priv, err := crypto.HexToECDSA(key)
	if err != nil {
		return common.Address{}, ErrInvalidPrivateKey
	}
	return crypto.PubkeyToAddress(priv.PublicKey), nil
}

// CallContract 只读调用合约（eth_call），返回原始返回数据。
// 用于出金/销毁前的链上事实核查——配置写错时靠它提前发现，而不是发完交易才知道。
func CallContract(rpcUrl, toAddr string, data []byte) ([]byte, error) {
	if strings.TrimSpace(rpcUrl) == "" {
		return nil, errors.New("evm: 未配置 RPC 地址")
	}
	to, err := NormalizeAddress(toAddr)
	if err != nil {
		return nil, fmt.Errorf("evm: 合约地址非法: %w", err)
	}
	cli, err := ethclient.Dial(rpcUrl)
	if err != nil {
		return nil, fmt.Errorf("evm: 连接 RPC 失败: %w", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	return cli.CallContract(ctx, ethereum.CallMsg{
		To:   &to,
		Data: data,
	}, nil)
}
