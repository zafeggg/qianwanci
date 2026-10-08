package config

// ============================== 迭代0：玩法/经济参数化 ==============================
// 说明（N次方-技术方案 迭代0）：
//   原 core/impl/config.go 中硬编码的费率/递增率/等级门槛/算力/销毁等常量，
//   统一收敛为 NpowerParams 结构，支持两级覆盖：
//     代码默认值（var 初始化） < config 表 params 列（JSON，ops/params 接口维护） < yml Params 段
//   加载入口见 core/impl/params.go LoadNpowerParams（各程序 main 在 config.Init 后调用）。
// 编码约定：数值字段 0 / 字符串字段 "" / 映射为空 = “未设置/不覆盖”，沿用默认值。
// 注意：若将手续费币种切到 SymbolDictionary 之外的币种（如 TM），需先在本包
//   SymbolDictionary / SymbolIconDictionary 补齐精度与图标，并在 price.go 提供价格源。

// NpowerParams 运行参数（同时兼容 yml Params 段与 config 表 params 列的 JSON 编码）
type NpowerParams struct {
	// ---- 众筹静态/动态收益 ----
	StaticRewardRate                float64 `yaml:"StaticRewardRate" json:"StaticRewardRate"`
	DynamicShardReward1GenerateRate float64 `yaml:"DynamicShardReward1GenerateRate" json:"DynamicShardReward1GenerateRate"`
	DynamicShardReward3GenerateRate float64 `yaml:"DynamicShardReward3GenerateRate" json:"DynamicShardReward3GenerateRate"`
	DynamicShardReward5GenerateRate float64 `yaml:"DynamicShardReward5GenerateRate" json:"DynamicShardReward5GenerateRate"`
	TeamRewardRate1                 float64 `yaml:"TeamRewardRate1" json:"TeamRewardRate1"`
	TeamRewardRate2                 float64 `yaml:"TeamRewardRate2" json:"TeamRewardRate2"`
	TeamRewardRate3                 float64 `yaml:"TeamRewardRate3" json:"TeamRewardRate3"`
	LossRate                        float64 `yaml:"LossRate" json:"LossRate"` //倒2/倒3 轮失败扣除比例（0.5 = 扣50%折算算力）

	// ---- 轮次 ----
	MinRound            uint    `yaml:"MinRound" json:"MinRound"`                       //三进一出：第 N 轮结算第 N-MinRound 轮
	MinTimeLimitSeconds float64 `yaml:"MinTimeLimitSeconds" json:"MinTimeLimitSeconds"` //轮次动态缩短的最小时限下限（秒）

	// ---- 提币手续费 ----
	FeeRate   float64 `yaml:"FeeRate" json:"FeeRate"`
	FeeSymbol string  `yaml:"FeeSymbol" json:"FeeSymbol"` //手续费币种（默认 BOFI；切 TM 需配套价格/精度）

	// ---- 投入限额口径 ----
	// MaxVoteIsPerUserRound：=1 保持"每轮每用户累计投入 ≤ round.MaxVote"（默认开启，TiMi需求#2）；
	// = -1 显式关闭（退回"MaxVote 仅限单笔、可多次累计超出"的存量口径）；0/空 = 不覆盖。
	MaxVoteIsPerUserRound float64 `yaml:"MaxVoteIsPerUserRound" json:"MaxVoteIsPerUserRound"`
	// RoundMaxVoteGrowRate 每轮最高投入限额的递增率（TiMi需求#2/N次方介绍：「最低限额不变，最高限额每轮增加一点点」）。
	// 例：10-100 → 10-110 → 10-130 → 10-150。默认 0.1；0/空 = 不覆盖（用代码默认值）。
	RoundMaxVoteGrowRate float64 `yaml:"RoundMaxVoteGrowRate" json:"RoundMaxVoteGrowRate"`

	// ---- 团队等级晋升门槛 ----
	F1DirectCount uint `yaml:"F1DirectCount" json:"F1DirectCount"`
	F1VoteCount   int  `yaml:"F1VoteCount" json:"F1VoteCount"`
	F1ToF2Count   uint `yaml:"F1ToF2Count" json:"F1ToF2Count"`
	F2ToF3Count   uint `yaml:"F2ToF3Count" json:"F2ToF3Count"`

	// ---- 矿机（FUSD→PCB 挖矿）----
	MiningRewardBuff       float64 `yaml:"MiningRewardBuff" json:"MiningRewardBuff"`
	MiningRewardSymbol     string  `yaml:"MiningRewardSymbol" json:"MiningRewardSymbol"`
	MiningWithdrawMinCount float64 `yaml:"MiningWithdrawMinCount" json:"MiningWithdrawMinCount"`

	// ---- 算力矿机（爆仓补偿）----
	HashPowerBuff           float64 `yaml:"HashPowerBuff" json:"HashPowerBuff"`
	HashPowerPoolDailyRatio float64 `yaml:"HashPowerPoolDailyRatio" json:"HashPowerPoolDailyRatio"`
	HashPowerMaxDays        float64 `yaml:"HashPowerMaxDays" json:"HashPowerMaxDays"`
	// HashPowerPoolDailyOutput FIBO 每日区块总产出（枚），TiMi需求#8：
	//   「默认币量为每日区块总产出 × 0.3」。池日产出 = 本值 × HashPowerPoolDailyRatio。
	// 原实现硬编码在 cmd/hashpower 的 -daily 默认值里（3498542274052×0.425），现收敛为可配参数。
	HashPowerPoolDailyOutput float64 `yaml:"HashPowerPoolDailyOutput" json:"HashPowerPoolDailyOutput"`

	// ---- 手续费销毁 ----
	BurnTarget float64 `yaml:"BurnTarget" json:"BurnTarget"`

	// ---- 多阶段币种开关 ----
	CurrentStage uint `yaml:"CurrentStage" json:"CurrentStage"` //1 第一阶段(FIBO+USDT) 2 第二阶段 3 第三阶段

	// ---- 行情兜底 ----
	// FiboStaticPrice FIBO 静态兜底价（U）。0 = 不兜底（默认，线上行情正常时行为完全不变）。
	// 用途：行情源不可达（离线/私链/被墙）时 FIBO 无价，会让算力矿机三项语义同时失效：
	//   模式1/2 的保底产出被跳过、金本位出局判定永不触发（无上限增发）、
	//   失败轮爆仓折算直接报错回滚（整笔失败结算都不落地）。
	// 与 USDT/DOX(=1)、TM(=3.4) 的静态兜底口径一致，只是改成可配、且默认关闭。
	FiboStaticPrice float64 `yaml:"FiboStaticPrice" json:"FiboStaticPrice"`

	// ---- 风控（D 模块）----
	// ⚠ 这组参数原本只存在于 core/impl/config.go 的硬编码 var 里，**没有接进 NpowerParams**，
	//   导致 /ops/params 写入它们时被静默忽略（2026-09-22 实测：写入 WriteRatePerMinute=1200 后
	//   响应头 X-RateLimit-Limit 仍是 60）。而「提现额度阈值待 owner 定值」这条待办的前提
	//   就是「数值可以运营配置」——不接进来，owner 定完值也只能改代码重新编译。
	// 编码约定同上：数值 0 = 不覆盖；开关类用 1/-1/0（1 开、-1 关、0 不覆盖），
	//   因为 bool 的零值 false 无法区分「未设置」与「显式关闭」。
	//
	// ⚠ 下面这几个数值是**例外**：它们的 0 本身就是有意义的取值（= 不限制），
	//   若沿用"0 = 不覆盖"，一旦经运维接口设过上限就再也无法取消（只能改代码或重启进程）。
	//   故约定：>0 设定该上限；-1 显式取消限制（置 0）；0 不覆盖。
	RequestRatePerMinute  int     `yaml:"RequestRatePerMinute" json:"RequestRatePerMinute"`   //单 IP 每分钟全部请求上限（>0 设定，-1 取消限制，0 不覆盖）
	WriteRatePerMinute    int     `yaml:"WriteRatePerMinute" json:"WriteRatePerMinute"`       //单 IP 每分钟写请求上限（>0 设定，-1 取消限制，0 不覆盖）
	// WithdrawMinAmount 最低起提额（按币种个数计）；owner 2026-09-30 敲定 100 个起提；-1 = 关闭
	WithdrawMinAmount float64 `yaml:"WithdrawMinAmount" json:"WithdrawMinAmount"`
	WithdrawSingleMax     float64 `yaml:"WithdrawSingleMax" json:"WithdrawSingleMax"`         //单笔提现上限（主币计价；>0 设定，-1 取消限制，0 不覆盖）
	WithdrawDailyMax      float64 `yaml:"WithdrawDailyMax" json:"WithdrawDailyMax"`           //单日提现累计上限（>0 设定，-1 取消限制，0 不覆盖）
	WithdrawDailyCountMax int     `yaml:"WithdrawDailyCountMax" json:"WithdrawDailyCountMax"` //单日提现笔数上限（>0 设定，-1 取消限制，0 不覆盖）
	BlacklistEnabled      int     `yaml:"BlacklistEnabled" json:"BlacklistEnabled"`           //黑名单总开关：1 开 / -1 关 / 0 不覆盖
	WhitelistEnabled      int     `yaml:"WhitelistEnabled" json:"WhitelistEnabled"`           //白名单总开关：1 开 / -1 关 / 0 不覆盖
}
