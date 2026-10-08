package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core/impl"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils/evm"
	"com.fibonacci.crowd/utils/metrics"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	glogger "gorm.io/gorm/logger"
)

// ============================== B 模块：FIBO(EVM) 链充值监听 ==============================
// 定位与既有 cmd/trshash 相同（充值到账监听），但走 EVM JSON-RPC + ERC20 Transfer 日志，
// 替代/并行于旧的 KTO gRPC 与 TRON TronGrid 通道。
//
// 与 trshash 的关键差异：
//   1. trshash 靠 kortho.io / trongrid 的**代币行情接口**反查转账，EVM 侧直接按日志过滤，语义更准；
//   2. **入账幂等由 wallet_hash_charge.hash 的唯一索引兜底**（uk_wallet_hash_charge_hash），
//      不是「先查再插」——后者在重启补扫/并发时必丢单或多单；
//   3. 进度存 Redis，重启后从断点续扫；确认数取 config.Chain.Confirmations（默认 12）。
//
// 用法：
//   evmwatch.exe -f config/etc.local.yml -token 0x... -pool 0x... -symbol FIBO -decimals 8
//   evmwatch.exe ... -once          只扫一轮就退出（便于脚本化/联调）
//   evmwatch.exe ... -fromBlock 123  指定起始块（Redis 无进度时生效）
//
// ⚠ 本机无 FIBO 节点，**链上端到端未验证**；离线可验证部分见 utils/evm 的单测。

var (
	errAlreadyCredited = errors.New("该笔充值已入账（唯一索引命中）")
	// errNotDeposit 该笔日志不是「用户 → 收款池」的充值（出金 / 自转 / mint），跳过且不计入任何计数
	errNotDeposit = errors.New("非充值转账")

	// 入账进程内的并发保护：AddPointAmount 内部虽用 CAS 重试，
	// 但同一地址同一 symbol 的多次入账仍应串行，避免大量重试。
	pointSafe = impl.NewWalletPointSafe()
)

const (
	// maxLogSpan FIBO RPC 对 eth_getLogs 的硬限制：单次区间必须 ≤2000 块。
	// 实测超限报错：the span between fromBlock and toBlock must be less than or equal to 2000。
	maxLogSpan = 2000
	// maxChunksPerTick 单轮最多拉多少片（防止落后几十万块时一次把 RPC 打爆；
	// 未追完的部分下一轮继续，进度已落 Redis 不会丢）。20 片 = 单轮最多 4 万块。
	maxChunksPerTick = 20
)

func main() {
	path := flag.String("f", "./config/etc.yml", "-f 指定配置文件")
	tokenFlag := flag.String("token", "", "ERC20 代币合约地址（留空则取 config.Chain.WatchToken）")
	poolFlag := flag.String("pool", "", "收款地址（充值目标，留空则取 config.Chain.WatchPool）")
	symbol := flag.String("symbol", "FIBO", "入账币种符号（需存在于 config.SymbolDictionary）")
	decimals := flag.Uint("decimals", 8, "代币精度（FIBO/TM 为 8）")
	fromBlock := flag.Uint64("fromBlock", 0, "起始块高（仅当 Redis 无进度时生效；0 = 从最新块开始）")
	interval := flag.Duration("interval", 15*time.Second, "轮询间隔")
	once := flag.Bool("once", false, "只扫一轮就退出")
	flag.Parse()

	config.Init(*path)
	//监听程序是常驻批处理，SQL 日志会把业务日志淹掉
	config.MysqlDBPool.Logger = glogger.Default.LogMode(glogger.Silent)
	impl.LoadNpowerParams()
	impl.WatchNpowerParamsReload()
	model.WatchRiskChange()

	//命令行优先，未给则取配置（便于用 systemd/env 部署，不必把地址写进命令行）
	tokenArg := *tokenFlag
	if tokenArg == "" {
		tokenArg = config.EtcConfig.Chain.WatchToken
	}
	poolArg := *poolFlag
	if poolArg == "" {
		poolArg = config.EtcConfig.Chain.WatchPool
	}

	token, err := evm.NormalizeAddress(tokenArg)
	if err != nil {
		log.Fatalln("[evmwatch] -token 非法或未提供（也可配置 Chain.WatchToken）：", err)
	}
	pool, err := evm.NormalizeAddress(poolArg)
	if err != nil {
		log.Fatalln("[evmwatch] -pool 非法或未提供（也可配置 Chain.WatchPool）：", err)
	}
	if _, ok := config.SymbolDictionary[*symbol]; !ok {
		log.Fatalf("[evmwatch] 币种 %s 不在 config.SymbolDictionary 中，无法换算精度", *symbol)
	}
	cli, err := ethclient.Dial(config.EtcConfig.Chain.FiboRpcUrl)
	if err != nil {
		log.Fatalln("[evmwatch] 连接 FIBO RPC 失败:", config.EtcConfig.Chain.FiboRpcUrl, err)
	}
	defer cli.Close()

	log.WithFields(log.Fields{
		"rpc": config.EtcConfig.Chain.FiboRpcUrl, "token": token.Hex(), "pool": pool.Hex(),
		"symbol": *symbol, "decimals": *decimals,
		"confirmations": config.EtcConfig.Chain.Confirmations,
	}).Infoln("[evmwatch] 启动")

	//配置体检：出金热钱包与收款池同址时，链上每笔出金都是 pool→用户，
	//必须靠 handleLog 里的 from==pool 过滤挡住（否则出金会被重复当成充值入账）。
	if hot := config.HotWalletPrivateKey(); hot != "" {
		if hotAddr, herr := evm.PrivateKeyToAddress(hot); herr == nil {
			if hotAddr == pool {
				log.WithFields(log.Fields{"pool": pool.Hex()}).
					Warnln("[evmwatch] 出金热钱包与收款池为同一地址：已启用 from==pool 过滤，出金不会被误当充值")
			} else {
				log.WithFields(log.Fields{"pool": pool.Hex(), "hotWallet": hotAddr.Hex()}).
					Infoln("[evmwatch] 收款池与出金热钱包不同址（生产推荐：充值地址与出金地址分离）")
			}
		}
	}

	for {
		if err := watchOnce(cli, token, pool, *symbol, uint8(*decimals), *fromBlock); err != nil {
			//配置类错误（例如"无起点块"）：**必须 fail-fast 且非 0 退出**。
			//否则 `-once` 模式下进程照样 exit 0，systemd（Restart=on-failure）与验证脚本
			//都会把它当成功 —— 上线时表现成"监听在跑但一笔充值都不入账"，比直接起不来更危险。
			if errors.Is(err, errNoStartBlock) {
				log.Fatalln("[evmwatch] 配置错误，拒绝启动:", err)
			}
			log.Errorln("[evmwatch] 本轮扫描失败:", err)
		}
		if *once {
			return
		}
		time.Sleep(*interval)
	}
}

// progressKey 扫块进度（按 币种+收款地址 隔离，允许多个监听实例并存）
func progressKey(symbol string, pool common.Address) string {
	return "npower:evmwatch:lastblock:" + symbol + ":" + pool.Hex()
}

func getLastBlock(symbol string, pool common.Address) (uint64, bool) {
	if config.RedisClient == nil {
		return 0, false
	}
	v, err := config.RedisClient.Get(context.Background(), progressKey(symbol, pool)).Result()
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func setLastBlock(symbol string, pool common.Address, block uint64) {
	if config.RedisClient == nil {
		return
	}
	//进度不设过期：这是续扫依据，丢了会导致重复扫描（虽然幂等，但白跑）
	config.RedisClient.Set(context.Background(), progressKey(symbol, pool), strconv.FormatUint(block, 10), 0)
	metrics.Set("npower_evmwatch_last_block", float64(block), map[string]string{"symbol": symbol})
}

// errNoStartBlock 配置类失败：既无 Redis 进度、也没给 -fromBlock / Chain.WatchStartBlock，
// 且未显式声明"接受不回溯"（AllowStartFromLatest）。这类错误不会自愈，必须 fail-fast。
var errNoStartBlock = errors.New(
	"无扫块进度且未指定起始块：请设置 Chain.WatchStartBlock（建议=TM 合约部署高度），" +
		"或命令行 -fromBlock <块高>；若确认不需要补扫历史充值，可显式设置 Chain.AllowStartFromLatest=true")

// watchOnce 扫描一轮：[last+1, latest-confirmations]
func watchOnce(cli *ethclient.Client, token, pool common.Address, symbol string, decimals uint8, fromBlockFlag uint64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	head, err := cli.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("获取最新块高失败: %w", err)
	}
	conf := config.EtcConfig.Chain.Confirmations
	if head <= conf {
		return nil //链太新/刚启动，还没有达到确认数的块
	}
	safeHead := head - conf

	last, ok := getLastBlock(symbol, pool)
	if !ok {
		//无进度：按优先级确定起扫块；**全都不可用时 fail-closed 拒绝启动**。
		//⚠ 2026-09-30 收紧：原实现兜底"从当前安全块开始"（只打一条 WARN），等于静默放弃补扫历史充值 ——
		//   上线时很容易变成"上线前的充值永远不会入账"，而要等对账才发现。现在要求显式二选一：
		//   给出起始块（-fromBlock / Chain.WatchStartBlock），或显式声明"接受不回溯"（AllowStartFromLatest）。
		switch {
		case fromBlockFlag > 0:
			last = fromBlockFlag - 1
			log.WithFields(log.Fields{"fromBlock": fromBlockFlag}).
				Infoln("[evmwatch] 无扫块进度，按 -fromBlock 起扫")
		case config.EtcConfig.Chain.WatchStartBlock > 0:
			last = config.EtcConfig.Chain.WatchStartBlock - 1
			log.WithFields(log.Fields{"watchStartBlock": config.EtcConfig.Chain.WatchStartBlock}).
				Infoln("[evmwatch] 无扫块进度，按 Chain.WatchStartBlock 起扫")
		case config.EtcConfig.Chain.AllowStartFromLatest:
			log.WithFields(log.Fields{"head": head, "safeHead": safeHead}).
				Warnln("[evmwatch] 无扫块进度且未指定起始块，按 Chain.AllowStartFromLatest=true 从当前安全块开始（此前的历史充值不会被补扫）")
			last = safeHead
		default:
			return errNoStartBlock
		}
	}
	if last >= safeHead {
		log.WithFields(log.Fields{"head": head, "safeHead": safeHead, "last": last, "fromBlockFlag": fromBlockFlag}).
			Debugln("[evmwatch] 无新块可扫（last >= safeHead），本轮跳过；如需强制重扫请清 Redis 进度键或用 -fromBlock")
		return nil
	}

	from := last + 1
	log.WithFields(log.Fields{"head": head, "safeHead": safeHead, "from": from, "chunks": maxChunksPerTick}).
		Debugln("[evmwatch] 开始扫描区间")

	//分片拉取：FIBO RPC 对 eth_getLogs 有硬限制「区间必须 ≤2000 块」（实测报错原文：
	//the span between fromBlock and toBlock must be less than or equal to 2000）。
	//不分片的话，监听一旦落后超过 2000 块就永远追不上（每次整段请求都被拒）。
	chunks := 0
	credited, skipped, notDeposit := 0, 0, 0
	for start := from; start <= safeHead && chunks < maxChunksPerTick; start += maxLogSpan {
		end := start + maxLogSpan - 1
		if end > safeHead {
			end = safeHead
		}
		chunks++

		query := ethereum.FilterQuery{
			FromBlock: new(big.Int).SetUint64(start),
			ToBlock:   new(big.Int).SetUint64(end),
			Addresses: []common.Address{token},
			//topics[0]=Transfer 签名，topics[1]=from（不限），topics[2]=to（必须是收款地址）
			Topics: [][]common.Hash{
				{common.HexToHash(evm.TransferTopic)},
				nil,
				{common.BytesToHash(pool.Bytes())},
			},
		}
		logs, err := cli.FilterLogs(ctx, query)
		if err != nil {
			return fmt.Errorf("拉取日志失败 [%d,%d]: %w", start, end, err)
		}

		for _, lg := range logs {
			switch err := handleLog(lg, token, pool, symbol, decimals); {
			case err == nil:
				credited++
			case errors.Is(err, errAlreadyCredited):
				skipped++
			case errors.Is(err, errNotDeposit):
				notDeposit++
			default:
				//单笔失败不阻断整片推进：否则该笔会卡住进度，导致后面所有块反复重扫。
				//代价是该笔需人工补账，因此打 ERROR 便于告警。
				log.WithFields(log.Fields{"tx": lg.TxHash.Hex(), "err": err}).Errorln("[evmwatch] 单笔入账失败（需人工核查）")
			}
		}

		//每片成功即落进度：中途失败也不会丢掉已经处理完的块
		setLastBlock(symbol, pool, end)
	}

	if behind := safeHead - from + 1; behind > uint64(maxLogSpan*maxChunksPerTick) {
		log.WithFields(log.Fields{"behind": behind, "chunks": chunks}).
			Warnln("[evmwatch] 落后较多，本轮只推进了部分区间，下一轮继续追赶")
	}
	if credited+skipped+notDeposit > 0 {
		log.WithFields(log.Fields{
			"from": from, "to": safeHead, "chunks": chunks,
			"credited": credited, "skipped": skipped, "notDeposit": notDeposit,
		}).Infoln("[evmwatch] 扫描完成")
	}
	return nil
}

// handleLog 解码 + 入账（幂等）
func handleLog(lg types.Log, token, pool common.Address, symbol string, decimals uint8) error {
	from, to, value, err := evm.DecodeERC20Transfer(lg)
	if err != nil {
		log.WithFields(log.Fields{"tx": lg.TxHash.Hex(), "err": err}).Debugln("[evmwatch] 日志解码失败，跳过")
		return errNotDeposit
	}
	//逐笔调试日志：排查「充值没入账」时，先看这里有没有这笔、to 是否等于 pool。
	//命中率极高的问题都出在「过滤条件没匹配上」而不是入账逻辑本身（实测踩过多次）。
	log.WithFields(log.Fields{
		"block": lg.BlockNumber, "tx": lg.TxHash.Hex(),
		"from": from.Hex(), "to": to.Hex(), "pool": pool.Hex(), "value": value.String(),
	}).Debugln("[evmwatch] 收到 Transfer 日志")
	if to != pool {
		log.WithFields(log.Fields{"tx": lg.TxHash.Hex(), "to": to.Hex(), "pool": pool.Hex()}).
			Debugln("[evmwatch] 收款方不是监听池，跳过")
		return errNotDeposit //理论上被 topics 过滤掉了，双保险
	}
	//⚠ 必须排除「自己转给自己」：出金热钱包与收款池是同一个地址时，
	//   每笔出金（热钱包 → 用户）在链上都是 pool → 用户，**不是充值**；
	//   若不过滤，出金会被再次当成充值入账（余额凭空增加，实测已复现）。
	//   同理排除 from == to 的自转账，以及 from 为 0x0 的 mint。
	if from == pool || from == to || from == (common.Address{}) {
		log.WithFields(log.Fields{
			"tx": lg.TxHash.Hex(), "from": from.Hex(), "to": to.Hex(), "value": value.String(),
		}).Debugln("[evmwatch] 非充值转账（出金/自转/mint），跳过")
		//用哨兵错误而非 nil 返回：调用方按「nil=已入账」计数，
		//返回 nil 会把跳过的交易也算成 credited，让日志/指标失真（实测踩过）。
		return errNotDeposit
	}
	if value.Sign() <= 0 {
		//同样用哨兵错误：返回 nil 会被计成「已入账」而实际什么都没写
		return errNotDeposit
	}
	if !value.IsUint64() {
		return fmt.Errorf("金额超出 uint64 记账范围: %s", value)
	}

	mainAmount := evm.AmountToMainUnit(value, decimals)
	if mainAmount <= 0 {
		return fmt.Errorf("换算后主单位金额为 0（decimals=%d, value=%s）", decimals, value)
	}

	//入账对象：谁的钱记到谁头上。
	//  recipient（默认，兼容既有私链口径）：记到**收款地址**对应的账户——
	//    适合「每个用户一个专属充值地址」的模型（用户从交易所提币到自己地址）。
	//  sender：记到**付款地址**对应的账户——
	//    适合「所有人共用一个收款池」的模型（DApp 正是给所有用户展示同一个地址）。
	//    安全性：上面的 from==pool / 自转 / mint 过滤已经挡住「出金被当成充值」，
	//    故按付款方入账不会让热钱包出去的资金凭空变成一笔充值。
	creditAddr := to.Hex()
	creditRole := "recipient"
	if strings.EqualFold(strings.TrimSpace(config.EtcConfig.Chain.DepositCreditTo), "sender") {
		creditAddr = from.Hex()
		creditRole = "sender"
	}

	//找用户：注册时 Address 与 EvmAddress 写的是同一个 EVM 地址，
	//这里两者都比对以兼容「只填了 evm_address 的历史数据」。
	addr := creditAddr
	var wallet model.Wallet
	if err := config.MysqlDBPool.Table(model.WalletTable).
		Where("`evm_address` = ? or `address` = ?", addr, addr).
		First(&wallet).Error; err != nil {
		log.WithFields(log.Fields{"address": addr, "creditRole": creditRole, "tx": lg.TxHash.Hex(), "err": err}).
			Warnln("[evmwatch] 入账地址无对应用户，跳过（不凭空建钱包）")
		//⚠ 返回哨兵错误而不是 nil：返回 nil 会被调用方计成「已入账」，
		//导致日志/指标显示 credited 但数据库没有任何变化（实测踩过，排查成本很高）。
		return errNotDeposit
	}

	txHash := lg.TxHash.Hex()
	log.WithFields(log.Fields{
		"tx": txHash, "walletId": wallet.ID, "address": wallet.Address,
		"symbol": symbol, "value": value.Uint64(), "mainAmount": mainAmount, "block": lg.BlockNumber,
	}).Debugln("[evmwatch] 开始入账")
	err = config.MysqlDBPool.Transaction(func(tx *gorm.DB) error {
		//① 唯一索引闸门：重复哈希直接撞唯一键，比「先查再插」可靠
		charge := model.WalletHashCharge{
			Address: wallet.Address,
			Hash:    txHash,
			Symbol:  symbol,
			Amount:  value.Uint64(),
		}
		if cerr := tx.Table(model.WalletHashChargeTable).Create(&charge).Error; cerr != nil {
			if isDuplicateKey(cerr) {
				return errAlreadyCredited
			}
			return cerr
		}

		//② 加余额（复用既有实现，保证与 KTO/TRON 通道口径一致）
		if _, aerr := pointSafe.AddPointAmount(tx, wallet.Address, symbol, mainAmount); aerr != nil {
			return aerr
		}

		//③ 记流水（Type=BlockIn 区块链入账）
		wtx := model.WalletTx{
			From:   pool.Hex(),
			To:     wallet.Address,
			Symbol: symbol,
			Amount: value.Uint64(),
			Desc:   enum.BlockInText,
			Hash:   txHash,
			Type:   enum.BlockIn,
		}
		return tx.Table(model.WalletTxTable).Create(&wtx).Error
	})
	if err != nil {
		return err
	}

	log.WithFields(log.Fields{
		"tx": txHash, "address": wallet.Address, "symbol": symbol,
		"amount": mainAmount, "block": lg.BlockNumber,
	}).Infoln("[evmwatch] 充值入账成功")
	return nil
}

// isDuplicateKey 判断是否唯一键冲突。
//
// 只做字符串匹配，**不能用 `gorm.ErrDuplicatedKey`**：该错误值（配合 TranslateError）
// 是 gorm v1.25 才引入的，本仓库锁在 v1.21.16，引用它直接编译不过。
// MySQL 1062 = ERANGE_DUP_ENTRY；驱动把原始错误包在 err.Error() 里。
func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "1062") || strings.Contains(msg, "Duplicate entry")
}
