package impl

import "com.fibonacci.crowd/enum"

const RewardAddress = "Kto7A1FCdp2c3endgUsTLNQzGtHjGNmytueQaJCsX5FKjmi" //奖励转出地址
const VoteAddress = "Kto7A1FCdp2c3endgUsTLNQzGtHjGNmytueQaJCsX5FKjmi"   //投入转入地址

// ============================== 迭代0：参数化（见 config/params.go + core/impl/params.go）==============================
// 以下运行参数以 var 形式给出默认值（= 改造前常量值），启动时由 LoadNpowerParams()
// 按「代码默认值 < config 表 params 列(JSON) < yml Params 段」三级覆盖。
// 新增可调参数请同步：config/params.go 字段、下方默认值、core/impl/params.go ApplyNpowerParams 分支。

var StaticRewardRate float64 = 0.13
var DynamicShardReward1GenerateRate float64 = 0.015
var DynamicShardReward3GenerateRate float64 = 0.02
var DynamicShardReward5GenerateRate float64 = 0.025


var LossRate float64 = 0.5 //倒2/倒3 扣除比例：被扣部分折算算力，**剩余部分退还用户**（需求#5/#7）
// MinRound 结算窗口：第 N 轮仓位在「第 N+MinRound 轮」End 时结算（round.go: rewardRoundNum=currentRound-MinRound）。
// 取值 3 = N次方"三进一出"（N次方-技术方案.md 1.2：第 N 轮投入在第 N+3 轮结束时统一结算）。
// 规则来源裁定（owner 2026-09-08）：千万次 TiMi(1).docx 与 N次方 均有规定的按千万次；千万次未明确结算轮次
// （仅有第5条倒1全退/倒2倒3扣半的失败处理），故沿用 N次方三进一出 = N+3。
// ⚠ 2026-09-30 起前端不再硬编码该值：`GET /api/rules` 的 settleOffset 直接下发本值，改这里前端自动跟随。
var MinRound uint = 3

var FeeRate float64 = 0.03 //提币手续费提取比例
// FeeSymbol 提币手续费币种。TiMi（千万次）基准 = TM（前端 config.js 裁决① 2026-09-04：
// 提币 3% 手续费使用 TM 支付，TM 销毁至仅剩 7777 枚）。默认直接写 TM；
// 历史 N次方默认 BOFI 见 enum.FEE_SYMBOL，如需切回由 config 表 params 列 / yml Params 段覆盖。
var FeeSymbol = "TM"

// EnforceVoteRoundCap 每轮每用户累计投入 ≤ round.MaxVote（TiMi需求#2：每轮用户有最高限额）。
// 默认开启；参数 MaxVoteIsPerUserRound：1=保持开启，-1=显式关闭（0/空=不覆盖）。
var EnforceVoteRoundCap = true

// RoundMaxVoteGrowRate 每轮最高投入限额递增率（TiMi需求#2 + N次方介绍：「最低限额不变，最高限额每轮增加一点点」）。
// 口径来源：N次方举例 10-100 → 10-110 → 10-130 → 10-150，绝对增量为 +10/+20/+20，非固定比例；
// 取等比更稳（不会随基数放大而失控），默认 10%：100→110→121→133，与举例同量级。
// 逐轮复合（next = cur × (1+rate)），最低限额 min 保持不变（由 AutoCreateNextRound 沿用）。
var RoundMaxVoteGrowRate float64 = 0.1

var MiningRewardBuff float64 = 3
var MiningRewardSymbol = enum.FIBO
var MiningWithdrawMinCount float64 = 50

var F1DirectCount uint = 10
var F1VoteCount int = 30 //伞下30人参与游戏（int：兼容 len()/int 比较）
var F1ToF2Count uint = 3 //成为F2的F1个数
var F2ToF3Count uint = 3 //成为F3的F2个数

// ============================== N次方新增参数 ==============================

// ---- 算力矿机（爆仓补偿，见 core/impl/hashpower.go）----
var HashPowerBuff float64 = 3             //三倍出局倍数（金本位：累计产出价值 ≥ 算力×3）
var HashPowerPoolDailyRatio float64 = 0.3 //矿池日产出占 FIBO 每日区块总产出的比例
var HashPowerMaxDays float64 = 300        //最长 300 天三倍出局（模式1/2 保底日产出分母）
// HashPowerPoolDailyOutputDefault FIBO 每日区块总产出（枚）默认值。
// 取自原 cmd/hashpower 的 -daily 默认 3498542274052×0.425（最小单位换算后的历史口径），
// 现作为参数默认值；实际取值由 config 表 params 列 / yml Params 段覆盖（见 config/params.go）。
const HashPowerPoolDailyOutputDefault = 3498542274052 * 0.425

var HashPowerPoolDailyOutput float64 = HashPowerPoolDailyOutputDefault

// FiboStaticPrice FIBO 静态兜底价（U）；0 = 关闭（默认）。
// 行情源不可达时 FIBO 无价会让算力矿机三项语义同时失效：模式1/2 保底被跳过、
// 金本位出局永不触发（账户无限挖矿）、失败轮爆仓折算报错并回滚整笔失败结算。
// 与 USDT/DOX(=1)、TM(=3.4) 的静态兜底口径一致，只是可配且默认关闭（线上行为不变）。
var FiboStaticPrice float64 = 0

// 算力矿机模式枚举（写入 hash_power.mode，语义固化不可配）
const HashPowerModeDefault uint = 0 //模式0：默认三倍出局（按算力占比分配，无保底）
const HashPowerMode300 uint = 1     //模式1：最长 300 天三倍出局（保底日产出）
const HashPowerModeTM uint = 2      //模式2：TM 加速（暂定 3.4U/枚，300 天三倍出局）

// ---- 手续费销毁（见 cmd/burn/burn.go）----
var BurnTarget float64 = 7777 //通缩目标：手续费币种流通量仅剩该数量即停止销毁

// ---- 团队收益（伞下该期总投资额比例制，见技术方案 1.5）----
var TeamRewardRate1 float64 = 0.005 //F1：伞下该期总投资额 × 0.5%
var TeamRewardRate2 float64 = 0.01  //F2：伞下该期总投资额 × 1%
var TeamRewardRate3 float64 = 0.015 //F3：伞下该期总投资额 × 1.5%

// ---- 多阶段币种开关（见技术方案 1.8）----
// 第一阶段：FIBO + USDT；第二阶段：+ 手续费币种 + KTO；第三阶段：更多 Token。
// CurrentStage 可经参数化切换（阶段 2/3 列表中的手续费币种随 FeeSymbol 联动）。
var CurrentStage uint = 1 //当前阶段（默认第一阶段）

// StageSymbols 各阶段允许参与众筹的币种
var StageSymbols = map[uint][]string{
	1: {enum.FIBO, enum.USDT},
	2: {enum.FIBO, enum.USDT, FeeSymbol, enum.KTO}, //第二阶段：+ 手续费币种 + KTO
	3: {enum.FIBO, enum.USDT, FeeSymbol, enum.KTO}, //第三阶段：更多 Token 待定
}

// ---- 轮次时限动态调整（见技术方案 1.1/2.3 改造项6）----
var MinTimeLimitSeconds float64 = 600 //最小时限下限：10 分钟（防时限过短导致系统来不及结算）

// ============================== 风控（D 模块，2026-09-18 新增）==============================
// 背景：全仓此前**没有任何限流、黑名单、白名单**（Limiter/Blacklist/RateLimit 检索零命中），
// 任何人只要知道地址就能高频调用 /api 写接口（参投/提现/领取），且提现无任何额度约束。
//
// ⚠ 待 owner 拍板的数值（现值 0 = 不限制，仅提供机制，不擅自设业务上限）：
//
//	WithdrawSingleMax / WithdrawDailyMax / WithdrawDailyCountMax
//	限流阈值给出的是保守默认值（可经 params 覆盖），提现额度必须由 owner 给定后才生效。
var (
	// RequestRatePerMinute 单 IP 每分钟**全部**请求上限（0 = 不限）
	RequestRatePerMinute = 240
	// WriteRatePerMinute 单 IP 每分钟**写请求**（POST/PUT/PATCH/DELETE）上限（0 = 不限）
	WriteRatePerMinute = 60
	// BlacklistEnabled 黑名单总开关（命中黑名单的地址禁止参投/提现/领取）
	BlacklistEnabled = true
	// WhitelistEnabled 白名单总开关（开启后仅白名单地址可参投/提现）
	WhitelistEnabled = false
	// WithdrawMinAmount 最低起提额（按被提币种的**个数**计，不是 U）。
	// owner 2026-09-30 敲定：**100 个起提**（防粉尘提现把链上手续费亏穿）。
	// 0 = 关闭校验（私链联调配置用 0，便于小额测试）；生产建议保持 100。
	WithdrawMinAmount float64 = 100
	// WithdrawSingleMax 单笔提现上限（主币计价，0 = 不限）
	WithdrawSingleMax float64 = 0
	// WithdrawDailyMax 单日提现累计上限（主币计价，0 = 不限）—— 待 owner 定值
	WithdrawDailyMax float64 = 0
	// WithdrawDailyCountMax 单日提现笔数上限（0 = 不限）—— 待 owner 定值
	WithdrawDailyCountMax = 0
)

// isStageSymbol 判断币种是否在当前阶段允许参与列表（N次方多阶段币种开关，见技术方案 1.8）
// 未配置当前阶段时放行（兼容旧部署）；超级账号（运营托底）不受币种限制。
func isStageSymbol(symbol string) bool {
	allowed, ok := StageSymbols[CurrentStage]
	if !ok {
		return true
	}
	for _, s := range allowed {
		if s == symbol {
			return true
		}
	}
	return false
}
