package impl

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/model"
	"context"
	"encoding/json"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// ============================== 迭代0 参数加载（core/impl/config.go 的参数化入口）==============================
// 生效优先级：代码默认值(var) < config 表 params 列(JSON, ops/params 接口维护) < yml Params 段(启动级显式覆盖)。
// 各程序 main 在 config.Init 之后调用一次 LoadNpowerParams()；
// 未命中任何配置时保持 var 默认值（与改造前常量完全一致），因此不影响既有部署。

// ApplyNpowerParams 将一份参数覆盖应用到运行参数（0 / 空字符串 = 不覆盖，见 config/params.go 编码约定）
func ApplyNpowerParams(p config.NpowerParams) {
	if p.StaticRewardRate > 0 {
		StaticRewardRate = p.StaticRewardRate
	}
	if p.DynamicShardReward1GenerateRate > 0 {
		DynamicShardReward1GenerateRate = p.DynamicShardReward1GenerateRate
	}
	if p.DynamicShardReward3GenerateRate > 0 {
		DynamicShardReward3GenerateRate = p.DynamicShardReward3GenerateRate
	}
	if p.DynamicShardReward5GenerateRate > 0 {
		DynamicShardReward5GenerateRate = p.DynamicShardReward5GenerateRate
	}
	if p.TeamRewardRate1 > 0 {
		TeamRewardRate1 = p.TeamRewardRate1
	}
	if p.TeamRewardRate2 > 0 {
		TeamRewardRate2 = p.TeamRewardRate2
	}
	if p.TeamRewardRate3 > 0 {
		TeamRewardRate3 = p.TeamRewardRate3
	}
	if p.LossRate > 0 {
		LossRate = p.LossRate
	}
	if p.MinRound > 0 {
		MinRound = p.MinRound
	}
	if p.MinTimeLimitSeconds > 0 {
		MinTimeLimitSeconds = p.MinTimeLimitSeconds
	}
	if p.FeeRate > 0 {
		FeeRate = p.FeeRate
	}
	if p.FeeSymbol != "" {
		setFeeSymbol(p.FeeSymbol)
	}
	if p.F1DirectCount > 0 {
		F1DirectCount = p.F1DirectCount
	}
	if p.F1VoteCount > 0 {
		F1VoteCount = p.F1VoteCount
	}
	if p.F1ToF2Count > 0 {
		F1ToF2Count = p.F1ToF2Count
	}
	if p.F2ToF3Count > 0 {
		F2ToF3Count = p.F2ToF3Count
	}
	if p.MiningRewardBuff > 0 {
		MiningRewardBuff = p.MiningRewardBuff
	}
	if p.MiningRewardSymbol != "" {
		MiningRewardSymbol = p.MiningRewardSymbol
	}
	if p.MiningWithdrawMinCount > 0 {
		MiningWithdrawMinCount = p.MiningWithdrawMinCount
	}
	if p.HashPowerBuff > 0 {
		HashPowerBuff = p.HashPowerBuff
	}
	if p.HashPowerPoolDailyRatio > 0 {
		HashPowerPoolDailyRatio = p.HashPowerPoolDailyRatio
	}
	if p.HashPowerMaxDays > 0 {
		HashPowerMaxDays = p.HashPowerMaxDays
	}
	if p.HashPowerPoolDailyOutput > 0 {
		HashPowerPoolDailyOutput = p.HashPowerPoolDailyOutput
	}
	if p.RoundMaxVoteGrowRate > 0 {
		RoundMaxVoteGrowRate = p.RoundMaxVoteGrowRate
	}
	if p.BurnTarget > 0 {
		BurnTarget = p.BurnTarget
	}
	if p.CurrentStage > 0 {
		CurrentStage = p.CurrentStage
	}
	if p.FiboStaticPrice > 0 {
		FiboStaticPrice = p.FiboStaticPrice
	}
	//风控（D 模块）：原本这些阈值只有硬编码 var，/ops/params 写了也不生效（见 config/params.go 注释）
	//
	// ⚠ 这里对「数值 0 = 不覆盖」的通用约定做了一个**必要的例外**：
	//   这几个阈值的 0 本身就是有意义的取值（= 不限制），如果沿用"0 不覆盖"，
	//   那么一旦经运维接口设过上限，就再也无法把限制取消（只能改代码重编或重启进程）。
	//   所以约定：>0 设定该上限；-1 = 显式取消限制（置 0）；0 = 不覆盖（沿用当前值）。
	//   与既有 MaxVoteIsPerUserRound 的 1/-1/0 风格一致。
	switch {
	case p.RequestRatePerMinute > 0:
		RequestRatePerMinute = p.RequestRatePerMinute
	case p.RequestRatePerMinute == -1:
		RequestRatePerMinute = 0
	}
	switch {
	case p.WriteRatePerMinute > 0:
		WriteRatePerMinute = p.WriteRatePerMinute
	case p.WriteRatePerMinute == -1:
		WriteRatePerMinute = 0
	}
	switch {
	//最低起提额（owner 2026-09-30 敲定 100 个起提）：>0 设定；-1 = 关闭校验（置 0）；0 = 不覆盖
	case p.WithdrawMinAmount > 0:
		WithdrawMinAmount = p.WithdrawMinAmount
	case p.WithdrawMinAmount == -1:
		WithdrawMinAmount = 0
	}
	switch {
	case p.WithdrawSingleMax > 0:
		WithdrawSingleMax = p.WithdrawSingleMax
	case p.WithdrawSingleMax == -1:
		WithdrawSingleMax = 0
	}
	switch {
	case p.WithdrawDailyMax > 0:
		WithdrawDailyMax = p.WithdrawDailyMax
	case p.WithdrawDailyMax == -1:
		WithdrawDailyMax = 0
	}
	switch {
	case p.WithdrawDailyCountMax > 0:
		WithdrawDailyCountMax = p.WithdrawDailyCountMax
	case p.WithdrawDailyCountMax == -1:
		WithdrawDailyCountMax = 0
	}
	//开关类：1 开 / -1 关 / 0 不覆盖（bool 零值无法区分「未设置」与「显式关闭」）
	switch p.BlacklistEnabled {
	case 1:
		BlacklistEnabled = true
	case -1:
		BlacklistEnabled = false
	}
	switch p.WhitelistEnabled {
	case 1:
		WhitelistEnabled = true
	case -1:
		WhitelistEnabled = false
	}
	if p.MaxVoteIsPerUserRound == 1 {
		EnforceVoteRoundCap = true
	} else if p.MaxVoteIsPerUserRound == -1 {
		EnforceVoteRoundCap = false
	}
}

// setFeeSymbol 切换手续费币种，并同步各阶段允许币种列表中的手续费币种条目。
// 槽位语义（复盘修复）：每阶段列表只替换"首个命中旧手续费币种"的下标（手续费槽位），
// 避免把列表内与手续费币种同名的常量币种位一并覆盖——例如曾把手续费切到 KTO 再切回 BOFI 时，
// 不再把常量位 KTO 一并替换为 BOFI（旧实现按值等匹配会把列表里的 KTO 也覆盖，运行态丢失 KTO 直到重启）。
func setFeeSymbol(sym string) {
	if sym == "" || sym == FeeSymbol {
		return
	}
	old := FeeSymbol
	FeeSymbol = sym
	for stage, list := range StageSymbols {
		replaced := false
		for i, s := range list {
			if replaced {
				break
			}
			if s == old || (old == enum.FEE_SYMBOL && s == enum.FEE_SYMBOL) {
				list[i] = sym
				replaced = true
			}
		}
		if replaced {
			StageSymbols[stage] = list
		}
	}
	log.WithFields(log.Fields{"old": old, "new": sym}).Infoln("手续费币种已切换，阶段币种列表已同步")
}

// ============================== 代码默认值快照 + 重载前复位（2026-09-30 新增）==============================
//
// 为什么需要：ApplyNpowerParams 的约定是「0/空 = 不覆盖」，因此它只会把 var **改成**某个值，
// 永远不会改回默认值 —— 一旦运维通过 /ops/params 设过某个参数，**删掉这个键也回不到默认值**
// （privchain-multiteam.ps1 的注释里就专门记着这个坑："还原不能靠删掉这个键，必须显式写回原数值"）。
// 运营事故场景很实际：设错了一个比例 → 删键 → 发现线上还是错的 → 只能写一个"看起来像默认"的值硬顶，
// 而那个值与代码默认值是否一致全靠人记。
//
// 现在：init 时快照代码默认值，每次 LoadNpowerParams 先用快照复位，再依次应用 config 表 params 与
// yml Params。于是「键不存在 = 用默认值」这一直觉成立，`null` 删除键（/ops/params 合并语义）也能真正还原。
type npowerDefaults struct {
	staticRewardRate         float64
	dynamicShard1            float64
	dynamicShard3            float64
	dynamicShard5            float64
	teamRewardRate1          float64
	teamRewardRate2          float64
	teamRewardRate3          float64
	lossRate                 float64
	minRound                 uint
	minTimeLimitSeconds      float64
	feeRate                  float64
	feeSymbol                string
	f1DirectCount            uint
	f1VoteCount              int
	f1ToF2Count              uint
	f2ToF3Count              uint
	miningRewardBuff         float64
	miningRewardSymbol       string
	miningWithdrawMinCount   float64
	hashPowerBuff            float64
	hashPowerPoolDailyRatio  float64
	hashPowerMaxDays         float64
	hashPowerPoolDailyOutput float64
	roundMaxVoteGrowRate     float64
	burnTarget               float64
	currentStage             uint
	fiboStaticPrice          float64
	requestRatePerMinute     int
	writeRatePerMinute       int
	withdrawMinAmount        float64
	withdrawSingleMax        float64
	withdrawDailyMax         float64
	withdrawDailyCountMax    int
	blacklistEnabled         bool
	whitelistEnabled         bool
	enforceVoteRoundCap      bool
	stageSymbols             map[uint][]string
}

var codeDefaults npowerDefaults

func init() {
	codeDefaults = npowerDefaults{
		staticRewardRate:         StaticRewardRate,
		dynamicShard1:            DynamicShardReward1GenerateRate,
		dynamicShard3:            DynamicShardReward3GenerateRate,
		dynamicShard5:            DynamicShardReward5GenerateRate,
		teamRewardRate1:          TeamRewardRate1,
		teamRewardRate2:          TeamRewardRate2,
		teamRewardRate3:          TeamRewardRate3,
		lossRate:                 LossRate,
		minRound:                 MinRound,
		minTimeLimitSeconds:      MinTimeLimitSeconds,
		feeRate:                  FeeRate,
		feeSymbol:                FeeSymbol,
		f1DirectCount:            F1DirectCount,
		f1VoteCount:              F1VoteCount,
		f1ToF2Count:              F1ToF2Count,
		f2ToF3Count:              F2ToF3Count,
		miningRewardBuff:         MiningRewardBuff,
		miningRewardSymbol:       MiningRewardSymbol,
		miningWithdrawMinCount:   MiningWithdrawMinCount,
		hashPowerBuff:            HashPowerBuff,
		hashPowerPoolDailyRatio:  HashPowerPoolDailyRatio,
		hashPowerMaxDays:         HashPowerMaxDays,
		hashPowerPoolDailyOutput: HashPowerPoolDailyOutput,
		roundMaxVoteGrowRate:     RoundMaxVoteGrowRate,
		burnTarget:               BurnTarget,
		currentStage:             CurrentStage,
		fiboStaticPrice:          FiboStaticPrice,
		requestRatePerMinute:     RequestRatePerMinute,
		writeRatePerMinute:       WriteRatePerMinute,
		withdrawMinAmount:        WithdrawMinAmount,
		withdrawSingleMax:        WithdrawSingleMax,
		withdrawDailyMax:         WithdrawDailyMax,
		withdrawDailyCountMax:    WithdrawDailyCountMax,
		blacklistEnabled:         BlacklistEnabled,
		whitelistEnabled:         WhitelistEnabled,
		enforceVoteRoundCap:      EnforceVoteRoundCap,
		stageSymbols:             cloneStageSymbols(StageSymbols),
	}
}

// cloneStageSymbols 深拷贝阶段币种列表（复位时要用默认快照整体替换，避免 slice 被就地改坏）
func cloneStageSymbols(src map[uint][]string) map[uint][]string {
	dst := make(map[uint][]string, len(src))
	for k, v := range src {
		cp := make([]string, len(v))
		copy(cp, v)
		dst[k] = cp
	}
	return dst
}

// resetToCodeDefaults 把运行参数复位为代码默认值（LoadNpowerParams 每次重载前调用）
func resetToCodeDefaults() {
	d := codeDefaults
	StaticRewardRate = d.staticRewardRate
	DynamicShardReward1GenerateRate = d.dynamicShard1
	DynamicShardReward3GenerateRate = d.dynamicShard3
	DynamicShardReward5GenerateRate = d.dynamicShard5
	TeamRewardRate1 = d.teamRewardRate1
	TeamRewardRate2 = d.teamRewardRate2
	TeamRewardRate3 = d.teamRewardRate3
	LossRate = d.lossRate
	MinRound = d.minRound
	MinTimeLimitSeconds = d.minTimeLimitSeconds
	FeeRate = d.feeRate
	FeeSymbol = d.feeSymbol
	StageSymbols = cloneStageSymbols(d.stageSymbols)
	F1DirectCount = d.f1DirectCount
	F1VoteCount = d.f1VoteCount
	F1ToF2Count = d.f1ToF2Count
	F2ToF3Count = d.f2ToF3Count
	MiningRewardBuff = d.miningRewardBuff
	MiningRewardSymbol = d.miningRewardSymbol
	MiningWithdrawMinCount = d.miningWithdrawMinCount
	HashPowerBuff = d.hashPowerBuff
	HashPowerPoolDailyRatio = d.hashPowerPoolDailyRatio
	HashPowerMaxDays = d.hashPowerMaxDays
	HashPowerPoolDailyOutput = d.hashPowerPoolDailyOutput
	RoundMaxVoteGrowRate = d.roundMaxVoteGrowRate
	BurnTarget = d.burnTarget
	CurrentStage = d.currentStage
	FiboStaticPrice = d.fiboStaticPrice
	RequestRatePerMinute = d.requestRatePerMinute
	WriteRatePerMinute = d.writeRatePerMinute
	WithdrawMinAmount = d.withdrawMinAmount
	WithdrawSingleMax = d.withdrawSingleMax
	WithdrawDailyMax = d.withdrawDailyMax
	WithdrawDailyCountMax = d.withdrawDailyCountMax
	BlacklistEnabled = d.blacklistEnabled
	WhitelistEnabled = d.whitelistEnabled
	EnforceVoteRoundCap = d.enforceVoteRoundCap
}

// LoadNpowerParams 加载运行参数（**先复位默认值**，再 config 表 params 列 < yml Params 段）
// 任何配置缺失/解析失败都不阻断启动，回落到代码默认值。
func LoadNpowerParams() {
	//0.复位为代码默认值：让"参数键不存在"真正等于"用默认值"
	resetToCodeDefaults()

	//1.config 表 params 列（运维运营配置，ops/params 接口维护）
	var cfg model.Config
	if err := config.MysqlDBPool.Table(model.ConfigTable).Order("`id` asc").First(&cfg).Error; err == nil {
		if cfg.Params != "" {
			var dbp config.NpowerParams
			if jerr := json.Unmarshal([]byte(cfg.Params), &dbp); jerr != nil {
				log.WithFields(log.Fields{"err": jerr}).Warnln("config 表 params 解析失败，忽略该级配置")
			} else {
				ApplyNpowerParams(dbp)
			}
		}
	} else if err != gorm.ErrRecordNotFound {
		//config 表缺失 / params 列缺失 / 库异常：参数未生效、回落默认值，需显式告警；
		//未建行(ErrRecordNotFound)属正常（尚无运维写入 config 表），保持静默。
		log.WithFields(log.Fields{"err": err}).Warnln("读取 config 表 params 失败，本轮使用代码默认参数（请先启动 npower 完成 AutoMigrate 补齐 params 列）")
	}

	//2.yml Params 段（显式覆盖，优先级最高）
	ApplyNpowerParams(config.EtcConfig.Params)

	log.WithFields(log.Fields{
		"StaticRewardRate":         StaticRewardRate,
		"DynamicShard1/3/5":        DynamicShardReward1GenerateRate,
		"TeamRate1/2/3":            TeamRewardRate1,
		"LossRate":                 LossRate,
		"FeeRate":                  FeeRate,
		"FeeSymbol":                FeeSymbol,
		"MinRound":                 MinRound,
		"MinTimeLimitSeconds":      MinTimeLimitSeconds,
		"F1Direct/Vote/F2/F3":      F1VoteCount,
		"MiningBuff/Symbol/Min":    MiningRewardBuff,
		"HashPowerBuff/Ratio/Days": HashPowerMaxDays,
		"HashPowerDailyOutput":     HashPowerPoolDailyOutput,
		"FiboStaticPrice":          FiboStaticPrice,
		"RiskRead/WritePerMin":     RequestRatePerMinute,
		"WithdrawMax/Day/Cnt":      WithdrawSingleMax,
		"Black/WhiteList":          BlacklistEnabled,
		"RoundMaxVoteGrowRate":     RoundMaxVoteGrowRate,
		"BurnTarget":               BurnTarget,
		"CurrentStage":             CurrentStage,
		"EnforceVoteRoundCap":      EnforceVoteRoundCap,
	}).Infoln("运行参数加载完成")
}

// NpowerParamsChannel 运行参数变更广播频道（ops/params 写入后发布，各程序订阅即重载）
const NpowerParamsChannel = "npower:params:reload"

// WatchNpowerParamsReload 订阅参数变更广播：收到后重载 config 表 params + yml，
// 使 npower/round/hashpower/burn/mining/demo 无需重启即可同步运维改参（Redis 不可用则跳过）。
func WatchNpowerParamsReload() {
	if config.RedisClient == nil {
		return
	}
	pubsub := config.RedisClient.Subscribe(context.Background(), NpowerParamsChannel)
	ch := pubsub.Channel()
	go func() {
		for range ch {
			log.Infoln("收到运行参数变更广播，重新加载参数")
			LoadNpowerParams()
		}
	}()
}
