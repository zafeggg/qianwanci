package main

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core/impl"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils/evm"
	"com.fibonacci.crowd/utils/kto"
	"errors"
	"flag"
	"fmt"
	"github.com/robfig/cron/v3"
	log "github.com/sirupsen/logrus"
	"math/big"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
)

// ============================== burn 手续费销毁程序（N次方新增） ==============================
// 功能：汇总 fee_burn 表中 status=0（待销毁）的手续费，从资金池钱包链上销毁，
//      直至当前手续费币种（impl.FeeSymbol，默认 BOFI）流通量仅剩 BurnTarget 枚（默认 7777）。
//
// 两条销毁通道（Burn.Chain 选择）：
//   - "kto"（默认，历史行为）：KTO gRPC 链，从 KtoPool 转出到 Burn.Address 黑洞地址；
//   - "evm"（2026-09-19 新增）：EVM(ERC20) 代币，调用合约的 burn(uint256) 真实销毁。
//     千万次的手续费币 TM 是 ERC20（contracts/TMToken.sol），**必须走 evm 通道**；
//     TMToken.burn 在合约层强制「流通量不得低于 7777 枚」，与后端 BurnTarget 构成双保险。
//
// 流程：
//   1. 查询待销毁记录（fee_burn.status=0），按币种汇总
//   2. 链上销毁（见上）
//   3. 更新记录 status=1（已销毁）并记录 tx_hash；失败回 status=2 待重试
// 前置条件：确认链支持 burn；不支持则使用黑洞地址 + 公开审计（技术方案 3.3 流程3）。
//
// 用法：./burn -f ./config/burn.yml  （常驻定时任务，默认每小时执行一次）

// getBurnAddress 取黑洞地址。
// 安全口径（2026-09-18 B 模块加固）：**不再回落到占位地址**。
// 原实现地址留空时返回 "KtoB1ackHole0000..."，会把资金池手续费转到一个人为编造的地址上——
// 那是真实资产损失而非销毁。现在未配置即视为「未就绪」，由调用方跳过本轮销毁并告警。
func getBurnAddress() (string, error) {
	addr := strings.TrimSpace(config.EtcConfig.Burn.Address)
	if addr == "" {
		return "", errors.New("未配置 Burn.Address（黑洞地址），已跳过销毁；请在 yml 填入链上真实黑洞地址")
	}
	if isPlaceholderBurnAddress(addr) {
		return "", fmt.Errorf("Burn.Address(%s) 形似占位值，已跳过销毁；请替换为链上真实黑洞地址", addr)
	}
	return addr, nil
}

// isPlaceholderBurnAddress 识别历史占位地址（如 KtoB1ackHole0000...），避免误当真实地址使用。
func isPlaceholderBurnAddress(addr string) bool {
	low := strings.ToLower(addr)
	return strings.Contains(low, "b1ack") || strings.Contains(low, "blackhole") ||
		strings.Contains(low, "placeholder") || strings.Contains(low, "0000000")
}

func main() {
	path := flag.String("f", "./config/burn.yml", "-f 指定配置文件")
	once := flag.Bool("once", false, "只执行一轮销毁就退出（联调/脚本化用；不加则常驻每小时一次）")
	flag.Parse()
	config.Init(*path)      //mysql, redis, config file
	impl.LoadNpowerParams() //迭代0：加载运行参数（手续费币种/销毁目标）
	impl.WatchNpowerParamsReload()

	if isEvmBurn() {
		//EVM 通道：合约 burn 是真实销毁，不需要黑洞地址；改为校验合约/私钥配置
		log.WithFields(log.Fields{
			"rpc": config.EtcConfig.Chain.FiboRpcUrl, "token": config.EtcConfig.Withdraw.TokenContract,
			"chainId": config.EtcConfig.Chain.FiboChainId,
		}).Infoln("[burn] 销毁通道 = evm（调用 ERC20 burn(uint256)）")
		if strings.TrimSpace(config.EtcConfig.Withdraw.TokenContract) == "" {
			log.Warnln("[burn] 未配置 Withdraw.TokenContract（TM 合约地址），本轮无法销毁；请在 yml 填入部署地址")
		}
		if strings.TrimSpace(burnPrivate()) == "" {
			log.Warnln("[burn] 未配置 Burn.EvmPrivate（销毁出资私钥），本轮无法销毁；生产应由 KMS/环境变量注入")
		}
	} else {
		//KTO 通道：启动即校验黑洞地址（未配置/占位时明确告警，不阻断启动，便于运维运行中补配置）
		if addr, err := getBurnAddress(); err != nil {
			log.Warnln("[burn] " + err.Error())
		} else {
			log.Infoln("[burn] 黑洞地址已配置:", addr)
		}
	}

	//启动前先执行一次（便于上线时立即清零存量待销毁手续费）
	if err := burnOnce(); err != nil {
		log.Errorln("首次销毁失败", err)
	}
	if *once {
		return
	}

	cronNew := cron.New(cron.WithSeconds(), cron.WithLogger(cron.VerbosePrintfLogger(log.StandardLogger())))
	//每小时检查一次待销毁手续费（频率可按需调整）
	if _, err := cronNew.AddFunc("0 0 * * * ?", func() {
		if err := burnOnce(); err != nil {
			log.Errorln("手续费销毁任务失败", err)
		}
	}); err != nil {
		panic(err)
	}

	cronNew.Start()
	defer cronNew.Stop()

	stopSignal := make(chan os.Signal, 1) //buffered：防止信号发送时无人接收导致丢失
	signal.Notify(stopSignal, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGQUIT)
	<-stopSignal
}

// burnOnce 执行一轮销毁：汇总待销毁手续费 → 链上销毁（KTO 转黑洞 / EVM burn） → 标记已销毁
func burnOnce() error {
	evmChain := isEvmBurn()

	//0. 通缩目标判定（BurnTarget = 手续费币种**剩余流通量**下限，默认 7777 枚）
	//
	//⚠ 2026-09-30 修复：原实现把「已销毁手续费累计量 ≥ BurnTarget」当成"流通量已到下限"，
	//两个量级完全不同（BurnTarget 语义见 core/impl/config.go#BurnTarget：流通量仅剩该数量）。
	//TM 初始发行量未定且通常远大于 7777，于是累计刚烧到 7777 枚就永久停烧，通缩目标永不可达；
	//合约层判的是 `totalSupply - amount >= MIN_CIRCULATING`，后端必须与之同口径。
	//现在：
	//  - EVM 通道（TM）：以**链上 totalSupply**（随 burn 递减，即剩余流通量）为准，不足下限即停；
	//  - KTO 黑洞通道：链上无法查询黑洞后流通量，退化为"累计销毁量"近似判据并明确提示。
	var burnedTotal uint64
	if err := config.MysqlDBPool.Table(model.FeeBurnTable).Select("COALESCE(SUM(`amount`), 0)").
		Where("`symbol` = ? and `status` = ?", impl.FeeSymbol, 1).Scan(&burnedTotal).Error; err != nil {
		log.Errorln("查询已销毁总量失败", err)
	}
	burnedFloat := float64(burnedTotal) / config.SymbolDictionary[impl.FeeSymbol]

	if evmChain {
		if stop, supply, minCirc := evmBurnTargetReached(); stop {
			log.WithFields(log.Fields{"symbol": impl.FeeSymbol, "target": impl.BurnTarget, "onChainSupply": supply.String()}).
				Infoln("链上剩余流通量已不足下限，停止销毁（与合约 MIN_CIRCULATING 同口径）")
			return nil
		} else if supply != nil {
			log.WithFields(log.Fields{"symbol": impl.FeeSymbol, "onChainSupply": supply.String(), "minCirc": minCirc.String(), "burned": burnedFloat}).
				Infoln("手续费销毁进度（链上剩余流通量口径）")
		} else {
			log.WithFields(log.Fields{"symbol": impl.FeeSymbol, "burned": burnedFloat}).
				Infoln("手续费销毁进度（链上流通量不可读，仅显示累计销毁量）")
		}
	} else if burnedFloat >= impl.BurnTarget {
		log.WithFields(log.Fields{"symbol": impl.FeeSymbol, "target": impl.BurnTarget, "burned": burnedFloat}).
			Warnln("KTO 黑洞通道无法查询链上剩余流通量，按累计销毁量达到 BurnTarget 近似停止（建议改用 EVM 通道）")
		return nil
	} else {
		log.WithFields(log.Fields{"symbol": impl.FeeSymbol, "burned": burnedFloat, "target": impl.BurnTarget}).Infoln("手续费销毁进度")
	}

	//1. 查询待销毁记录（status IN (0,2)：0 待销毁，2 上次上链失败待重试），按币种汇总
	var pending []model.FeeBurn
	if err := config.MysqlDBPool.Table(model.FeeBurnTable).Find(&pending, "`status` IN ?", []uint{0, 2}).Error; err != nil {
		return err
	}
	if len(pending) == 0 {
		log.Infoln("无待销毁手续费")
		return nil
	}

	//按币种汇总
	summary := make(map[string]uint64)
	for _, fb := range pending {
		summary[fb.Symbol] += fb.Amount
	}

	var burnAddr string
	if !evmChain {
		var err error
		if burnAddr, err = getBurnAddress(); err != nil {
			log.Warnln("[burn] " + err.Error())
			return nil //地址未就绪不算失败，不重试也不上链
		}
	}
	pool := config.EtcConfig.KtoPool
	//本轮已抢占（status=3）的币种：失败时统一回滚，避免残留"销毁中"永久滞留
	var claimedSymbols []string
	for symbol, amount := range summary {
		if amount <= 0 {
			continue
		}
		//2. 原子抢占本币种待销毁记录为"销毁中(status=3)"，防重复销毁。
		//   若链上销毁成功但 DB 更新失败，记录滞留 status=3，不会再次上链销毁（绝不重复销毁），
		//   由人工核查该笔链上交易后处理（宁可少销毁，不可重复销毁）。
		res := config.MysqlDBPool.Table(model.FeeBurnTable).
			Where("`symbol` = ? and `status` IN ?", symbol, []uint{0, 2}).
			Update("status", 3)
		if res.Error != nil {
			log.Errorf("标记销毁中(%v)失败: %v", symbol, res.Error)
			continue
		}
		if res.RowsAffected == 0 {
			continue // 无待处理记录（已被并发抢占）
		}
		claimedSymbols = append(claimedSymbols, symbol)

		//3. 决定本次**精确**销毁量，再上链
		//   ⚠ 2026-09-30 二次修正：只按"链上可销毁余量(allowance)"截断还不够 ——
		//   allowance 通常不等于任何几条记录的和（例：allowance=79600001，记录是 3 条 30000000），
		//   若照 allowance 烧，链上烧了 79600001 而账本只能标 60000000 条记录，
		//   差值就成了"链上烧了但账本没记"的新账实不符。
		//   因此：**先算出能整条覆盖的 FIFO 前缀和**，再按该值精确销毁，保证
		//   「链上销毁量 == 被标记为已销毁的记录之和」这一不变量恒成立。
		var hash string
		var err error
		var done, left []model.FeeBurn // 本次已销毁 / 留待下次 的记录
		if evmChain {
			if symbol != impl.FeeSymbol {
				//EVM 通道只配置了手续费币种的合约（Withdraw.TokenContract）：
				//其它币种若从这里下去会把"数量"烧到 TM 合约上（资金级错配），直接跳过并回滚状态。
				log.WithFields(log.Fields{"symbol": symbol, "feeSymbol": impl.FeeSymbol}).
					Warnln("[burn] EVM 通道未配置该币种的销毁合约，跳过待配置后处理")
				if uerr := config.MysqlDBPool.Table(model.FeeBurnTable).Where("`symbol` = ? and `status` = ?", symbol, 3).Update("status", 2).Error; uerr != nil {
					log.Errorln("标记销毁失败记录出错", uerr)
				}
				continue
			}
			allowance, aerr := evmBurnAllowance(symbol, amount)
			if aerr != nil {
				log.Errorf("销毁 %v %v 前预检失败: %v", amount, symbol, aerr)
				if uerr := config.MysqlDBPool.Table(model.FeeBurnTable).
					Where("`symbol` IN ? and `status` = ?", claimedSymbols, 3).Update("status", 2).Error; uerr != nil {
					log.Errorln("标记销毁失败记录出错", uerr)
				}
				return errors.New("销毁上链失败")
			}
			done, left = splitBurnRows(pending, symbol, allowance)
			if len(done) == 0 {
				//余量为 0 或不足以覆盖任何一条记录：本轮不销毁（不烧"无账可对"的数量）
				if uerr := config.MysqlDBPool.Table(model.FeeBurnTable).
					Where("`symbol` IN ? and `status` = ?", claimedSymbols, 3).Update("status", 2).Error; uerr != nil {
					log.Errorln("标记销毁失败记录出错", uerr)
				}
				log.WithFields(log.Fields{
					"symbol": symbol, "allowance": allowance, "pendingRows": len(left),
					"minRow": minRowAmount(pending, symbol),
				}).Warnln("[burn] 链上可销毁余量不足以覆盖任何一条待销毁记录，本轮跳过；" +
					"记录已退回待销毁(status=2)，请调整 BurnTarget 或等待新的手续费累积（避免销毁量与账本对不上）")
				continue
			}
			hash, err = evmBurnExact(symbol, sumAmount(done))
		} else {
			done = nil
			_, hash, err = kto.KTOonChainSync(symbol, pool.Address, config.KtoPoolPrivateKey(), burnAddr, amount)
			done, left = splitBurnRows(pending, symbol, amount) //黑洞转账无额度限制，整批生效
		}
		if err != nil {
			log.Errorf("销毁 %v %v 上链失败: %v", amount, symbol, err)
			//⚠ 2026-09-30 修复：原实现只回滚"当前币种"的 status=3，本轮先前已抢占的其它币种会
			//永久滞留"销毁中"（burnOnce 只选 status IN (0,2)），只能靠 recon 的 WARN 发现。
			//现在回滚本轮**已抢占的全部**币种记录（只覆盖 status=3，已成功的 status=1 不受影响）。
			if uerr := config.MysqlDBPool.Table(model.FeeBurnTable).
				Where("`symbol` IN ? and `status` = ?", claimedSymbols, 3).Update("status", 2).Error; uerr != nil {
				log.Errorln("标记销毁失败记录出错", uerr)
			}
			return errors.New("销毁上链失败")
		}
		burnedAmount := sumAmount(done) //链上销毁量 == 待标记记录之和（不变量）
		if evmChain {
			log.Infof("销毁成功: %v %v, tx_hash: %v, 方式: ERC20 burn(uint256)", burnedAmount, symbol, hash)
		} else {
			log.Infof("销毁成功: %v %v, tx_hash: %v, 目标地址: %v", burnedAmount, symbol, hash, burnAddr)
		}

		//4. 按**实际销毁量**精确记账（2026-09-30 修复账实不符）：
		//   已销毁的记录标 status=1，其余回滚为待销毁(status=2)。
		//   原实现无条件把整批标记为已销毁 —— 一旦链上因 7777 下限被截断，DB 就说全烧了，
		//   而剩余部分既不会重试也无法对账（注释只写了"需人工决定"，等于长期账实不符）。
		if len(done) > 0 {
			if err = config.MysqlDBPool.Table(model.FeeBurnTable).
				Where("`id` IN ? and `status` = ?", idsOf(done), 3).
				Updates(map[string]interface{}{"status": 1, "tx_hash": hash}).Error; err != nil {
				log.Errorln("更新销毁记录失败(记录滞留 status=3，需人工核查该笔链上交易是否已成功)", err)
				return err
			}
		}
		if len(left) > 0 {
			if err = config.MysqlDBPool.Table(model.FeeBurnTable).
				Where("`id` IN ? and `status` = ?", idsOf(left), 3).
				Update("status", 2).Error; err != nil {
				log.Errorln("回滚未销毁记录失败(记录滞留 status=3，需人工核查)", err)
				return err
			}
			log.WithFields(log.Fields{
				"symbol": symbol, "burned": burnedAmount, "pendingRows": len(left),
			}).Warnln("[burn] 链上可销毁余量不足（受 7777 下限约束），本次只销毁了部分手续费；" +
				"未销毁部分已保留为待销毁(status=2)，将随下次运行重试 —— 若要继续销毁请提高 TM 流通量或调整 BurnTarget")
		}
	}
	return nil
}

// sumAmount 记录金额合计（校验"链上销毁量 == 已标记记录之和"这一不变量）
func sumAmount(rows []model.FeeBurn) uint64 {
	var sum uint64
	for _, r := range rows {
		sum += r.Amount
	}
	return sum
}

// minRowAmount 某币种待销毁记录中的最小单条金额（0 表示没有记录）；用于告警说明"余量连最小一条都不够"
func minRowAmount(rows []model.FeeBurn, symbol string) uint64 {
	var min uint64
	for _, r := range rows {
		if r.Symbol != symbol {
			continue
		}
		if min == 0 || r.Amount < min {
			min = r.Amount
		}
	}
	return min
}

// splitBurnRows 按 FIFO 把某币种的待销毁记录拆成「本次已销毁」与「留待下次」两组。
//
// 规则：按 id 升序累加（先入先销），前缀和 ≤ burned 的记录算已销毁；其余留待下次。
// burned >= 总量时全部归入已销毁（正常情况下就是整批）。
//
// 抽成纯函数便于单测：这是"DB 与链上必须一致"的关键点，原实现（整批标记）在链上截断时会账实不符。
func splitBurnRows(rows []model.FeeBurn, symbol string, burned uint64) (done, left []model.FeeBurn) {
	cand := make([]model.FeeBurn, 0, len(rows))
	for _, r := range rows {
		if r.Symbol == symbol {
			cand = append(cand, r)
		}
	}
	sort.Slice(cand, func(i, j int) bool { return cand[i].ID < cand[j].ID })

	var acc uint64
	for _, r := range cand {
		if acc+r.Amount <= burned {
			acc += r.Amount
			done = append(done, r)
		} else {
			//该条会导致累计超过实际销毁量：本条与其后都留待下次（保持 FIFO 语义，不做拆分）
			left = append(left, r)
		}
	}
	return done, left
}

// idsOf 取出记录 id 列表（用于按 id 精确更新，避免把并发/同批其它记录一起改掉）
func idsOf(rows []model.FeeBurn) []uint {
	ids := make([]uint, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

/* ==================== EVM(ERC20) 销毁通道（千万次 TM 手续费使用） ==================== */

// isEvmBurn 是否走 EVM 销毁通道（Burn.Chain = "evm"，大小写不敏感）
func isEvmBurn() bool {
	return strings.EqualFold(strings.TrimSpace(config.EtcConfig.Burn.Chain), "evm")
}

// burnPrivate 销毁出资私钥：环境变量（Burn.PrivateKeyEnv）优先，yml 的 Burn.EvmPrivate 兜底。
// yml 里写私钥仅限本地私链；生产必须由 KMS 或环境变量注入（yml 已入库）。
func burnPrivate() string {
	return config.BurnPrivateKey()
}

// decimalsOfScale 把 SymbolDictionary 里的 10 的幂（1e8 这种）换算成 decimals 位数。
// 返回 ok=false 表示该值不是 10 的整数次幂（登记有误），调用方据此告警而不是硬算。
func decimalsOfScale(scale float64) (uint8, bool) {
	d := 0
	for scale > 1.5 && d < 77 {
		scale /= 10
		d++
	}
	if scale > 1.5 {
		return 0, false
	}
	if scale < 1.0000001 && scale > 0.9999999 {
		return uint8(d), true
	}
	//允许 1e8 之类的浮点表示误差，但仍要求非常接近 1
	if scale-1 < 1e-6 && 1-scale < 1e-6 {
		return uint8(d), true
	}
	return 0, false
}

// evmBurnTargetReached 判断 EVM 通道是否已到通缩目标。
//
// BurnTarget 的语义是「手续费币种**剩余流通量**下限」，合约层用 totalSupply 判定
// （TMToken._burnWithFloor：totalSupply - amount >= MIN_CIRCULATING），
// 因此后端也用链上 totalSupply 作为判据 —— ERC20 的 totalSupply 随 burn 递减，即剩余流通量。
//
// 返回 stop=true 表示已到下限；supply/minCirc 为 nil 表示链上不可读（此时返回 stop=false，
// 继续走 evmBurn 的预检兜底，避免因节点抖动让销毁永久停摆）。
func evmBurnTargetReached() (stop bool, supply *big.Int, minCirc *big.Int) {
	cfg := config.EtcConfig
	token := strings.TrimSpace(cfg.Withdraw.TokenContract)
	if token == "" {
		return false, nil, nil
	}
	scale := config.SymbolDictionary[impl.FeeSymbol]
	if scale <= 0 {
		return false, nil, nil
	}
	supply, err := evm.ReadERC20TotalSupply(cfg.Chain.FiboRpcUrl, token)
	if err != nil {
		log.WithFields(log.Fields{"err": err}).Warnln("[burn] 读取链上 totalSupply 失败，按未达标继续处理")
		return false, nil, nil
	}
	minCirc = new(big.Int).SetUint64(uint64(impl.BurnTarget * scale))
	return supply.Cmp(minCirc) <= 0, supply, minCirc
}

// evmBurnAllowance 销毁前的**预检**：返回链上允许本次销毁的最大量（最小单位）。
//
// 双保险设计（与 contracts/TMToken.sol 对齐）：
//   - 链上：TMToken._burnWithFloor 要求 totalSupply - amount >= 7777 枚，否则 revert；
//   - 链下：这里先读 totalSupply 做预检，把请求量收敛到"可销毁余量"以内，
//     避免整批销毁因一笔超限而 revert 回滚（回滚后 status 回 2，下一轮仍然超限 → 死循环）。
//
// ⚠ 返回值不等于调用方的最终销毁量：调用方还要用 splitBurnRows 把它收敛到"能整条覆盖的
//   记录金额之和"（见 burnOnce），保证「链上销毁量 == 被标记已销毁的记录之和」。
func evmBurnAllowance(symbol string, amount uint64) (uint64, error) {
	cfg := config.EtcConfig
	token := strings.TrimSpace(cfg.Withdraw.TokenContract)
	if token == "" {
		return 0, errors.New("未配置 Withdraw.TokenContract（TM 合约地址）")
	}
	priv := burnPrivate()
	if priv == "" {
		return 0, errors.New("未配置 Burn.EvmPrivate（销毁出资私钥）")
	}
	from := cfg.Burn.EvmFrom
	if from == "" {
		from = cfg.TronPool.Address
	}
	if from == "" {
		return 0, errors.New("未配置 Burn.EvmFrom（销毁出资地址，须为 TM 持有者）")
	}

	decimals := config.SymbolDictionary[symbol]
	if decimals <= 0 {
		return 0, fmt.Errorf("币种 %s 未在 config.SymbolDictionary 中登记精度", symbol)
	}

	//预检 1：链上 decimals 与记账精度必须一致（写错会导致销毁数量差几个数量级）
	onChainDecimals, err := evm.ReadERC20Decimals(cfg.Chain.FiboRpcUrl, token)
	if err != nil {
		return 0, fmt.Errorf("读取链上 decimals 失败: %w", err)
	}
	if want, ok := decimalsOfScale(decimals); !ok {
		log.WithFields(log.Fields{"symbol": symbol, "scale": decimals}).
			Warnln("[burn] config.SymbolDictionary 的精度不是 10 的幂，无法与链上 decimals 比对，请人工核对")
	} else if want != onChainDecimals {
		log.WithFields(log.Fields{"symbol": symbol, "dbDecimals": want, "chainDecimals": onChainDecimals}).
			Warnln("[burn] 链上 decimals 与后端精度登记值不一致，请核对 config.SymbolDictionary")
	}

	//预检 2：可销毁余量（totalSupply - 7777 枚），超限则截断
	burnable := amount
	supply, err := evm.ReadERC20TotalSupply(cfg.Chain.FiboRpcUrl, token)
	if err != nil {
		return 0, fmt.Errorf("读取链上 totalSupply 失败: %w", err)
	}
	minCirc := new(big.Float).SetPrec(256).SetFloat64(impl.BurnTarget)
	minCirc.Mul(minCirc, big.NewFloat(decimals))
	minCircInt, _ := minCirc.Int(nil)
	if supply.Cmp(minCircInt) <= 0 {
		return 0, fmt.Errorf("链上流通量 %s 已不高于销毁下限 %s，拒绝销毁", supply.String(), minCircInt.String())
	}
	allowance := new(big.Int).Sub(supply, minCircInt) //可销毁上限
	if allowance.Cmp(new(big.Int).SetUint64(amount)) < 0 {
		burnable = allowance.Uint64()
		log.WithFields(log.Fields{"requested": amount, "allowance": burnable, "supply": supply.String()}).
			Warnln("[burn] 本次请求的销毁量超过链上可销毁余量（7777 下限），已收敛到余量以内；剩余记录留待下次")
	}
	if burnable == 0 {
		return 0, errors.New("截断后可销毁量为 0，跳过")
	}

	//预检 3：出资地址余额是否够（不足时合约会 revert，提前发现更省事）
	bal, err := evm.ReadERC20BalanceOf(cfg.Chain.FiboRpcUrl, token, from)
	if err != nil {
		log.WithFields(log.Fields{"err": err}).Warnln("[burn] 读取销毁出资地址余额失败，继续执行")
	} else if bal.Cmp(new(big.Int).SetUint64(burnable)) < 0 {
		return 0, fmt.Errorf("销毁出资地址 %s 的 TM 余额不足：有 %s，需 %d", from, bal.String(), burnable)
	}
	return burnable, nil
}

// evmBurnExact 按**调用方给定的精确数量**执行 ERC20 burn（最小单位），返回交易哈希。
//
// 调用方必须先用 evmBurnAllowance 校验额度与余额，这里只负责广播。
// 拆成两步的目的：让"链上销毁量"能精确等于"账本标记为已销毁的记录金额之和" ——
// allowance 通常不等于任何几条记录的和（例如 allowance=79600001，记录是若干条 30000000），
// 若直接按 allowance 烧，账本就会出现"销毁了但没记账"的差额。
func evmBurnExact(symbol string, amount uint64) (hash string, err error) {
	cfg := config.EtcConfig
	token := strings.TrimSpace(cfg.Withdraw.TokenContract)
	if token == "" {
		return "", errors.New("未配置 Withdraw.TokenContract（TM 合约地址）")
	}
	priv := burnPrivate()
	if priv == "" {
		return "", errors.New("未配置 Burn.EvmPrivate（销毁出资私钥）")
	}
	if amount == 0 {
		return "", errors.New("销毁数量为 0，拒绝上链")
	}
	_ = symbol //币种仅用于日志/追溯，合约由 Withdraw.TokenContract 决定
	return evm.SendERC20Burn(
		cfg.Chain.FiboRpcUrl, token, priv,
		new(big.Int).SetUint64(amount), cfg.Chain.FiboChainId,
	)
}
