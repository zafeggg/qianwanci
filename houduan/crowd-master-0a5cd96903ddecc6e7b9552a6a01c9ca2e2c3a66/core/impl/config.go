package impl

import "com.fibonacci.crowd/enum"

const RewardAddress = "Kto7A1FCdp2c3endgUsTLNQzGtHjGNmytueQaJCsX5FKjmi" //奖励转出地址
const VoteAddress = "Kto7A1FCdp2c3endgUsTLNQzGtHjGNmytueQaJCsX5FKjmi" //投入转入地址

const StaticRewardRate = 0.13
const DynamicShardReward1GenerateRate = 0.015
const DynamicShardReward3GenerateRate = 0.02
const DynamicShardReward5GenerateRate = 0.025

const DynamicTeamRewardDiffRate = 0.005 //动态收益-团队奖励-极差
const LossRate = 0.5 //倒二倒三损失比例
const MinRound = 2 //最低玩法轮数
const MaxVoteCount = 5

const FeeRate = 0.03   //手续费提取比例

const MiningRewardBuff = 3
const MiningRewardSymbol  = "FIBO"
const MiningWithdrawMinCount = 50

const F1DirectCount = 10
const F1VoteCount = 30 //伞下30人参与游戏
const F1ToF2Count = 3 //成为F2的F1个数
const F2ToF3Count = 3 //成为F3的F2个数

// ============================== N次方新增常量 ==============================
// 说明：所有参数化配置（费率/递增率/币种/阶段等）后续迭代0 将迁移至 config 表 + YAML，
//       当前先以常量形式补齐编译缺口，保证新增模块（hashpower / burn）可编译运行。

// ---- 算力矿机（爆仓补偿，见 core/impl/hashpower.go）----
const HashPowerBuff = 3          //三倍出局倍数（金本位：累计产出价值 ≥ 算力×3）
const HashPowerPoolDailyRatio = 0.3 //矿池日产出占 FIBO 每日区块总产出的比例
const HashPowerMaxDays = 300     //最长 300 天三倍出局（模式1/2 保底日产出分母）
const HashPowerModeDefault = 0   //模式0：默认三倍出局（按算力占比分配，无保底）
const HashPowerMode300 = 1       //模式1：最长 300 天三倍出局（保底日产出）
const HashPowerModeTM = 2        //模式2：TM 加速（暂定 3.4U/枚，300 天三倍出局）

// ---- 手续费销毁（见 cmd/burn/burn.go）----
// 通缩目标：手续费币（enum.FeeSymbol，当前为 TM）流通量仅剩 7777 枚即停止销毁。
// 注：FeeSymbol 与参与币 FEE_SYMBOL(BOFI) 语义独立，勿混淆。
const BurnTarget = 7777

// ---- 团队收益（伞下该期总投资额比例制，见技术方案 1.5）----
const TeamRewardRate1 = 0.005 //F1：伞下该期总投资额 × 0.5%
const TeamRewardRate2 = 0.01  //F2：伞下该期总投资额 × 1%
const TeamRewardRate3 = 0.015 //F3：伞下该期总投资额 × 1.5%

// ---- 多阶段币种开关（见技术方案 1.8）----
// 第一阶段：FIBO + USDT；第二阶段：+ BOFI + KTO；第三阶段：更多 Token。
// 切换阶段只需修改 CurrentStage（迭代0 参数化时将迁移至 config 表/yml 由运营配置）。
const CurrentStage = 1 //当前阶段（默认第一阶段）

// StageSymbols 各阶段允许参与众筹的币种
var StageSymbols = map[uint][]string{
	1: {enum.FIBO, enum.USDT}, //第一阶段：FIBO + USDT
	2: {enum.FIBO, enum.USDT, enum.FEE_SYMBOL, enum.KTO}, //第二阶段：+ BOFI + KTO
	3: {enum.FIBO, enum.USDT, enum.FEE_SYMBOL, enum.KTO}, //第三阶段：更多 Token 待定
}

// ---- 轮次时限动态调整（见技术方案 1.1/2.3 改造项6）----
const MinTimeLimitSeconds = 600 //最小时限下限：10 分钟（防时限过短导致系统来不及结算，见技术方案风险项）

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
