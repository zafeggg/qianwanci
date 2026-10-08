package enum

const (
	//ProjectWaiting 0未开始 1进行中 2停止
	ProjectWaiting = iota
	ProjectStarting
	ProjectEnding
)

const (
	//RoundWaiting 0未开始 1进行中 2待结算 3已结束
	RoundWaiting = iota
	RoundStarting
	RoundCompute
	RoundEnding
)

const (
	//Level0 等级 F1 F2 F3
	Level0 = iota
	Level1
	Level2
	Level3
)

const (
	Vote              = 1
	BlockOut          = 2
	BlockIn           = 3
	DynamicShard      = 4
	DynamicTeam       = 5
	StaticReward      = 6
	SuccessWithdraw   = 7
	Failed100Withdraw = 8
	Failed50Withdraw  = 9
	Fee               = 10
	FusdPoint = 11
	MiningReward = 12
	SuperAdmin100Withdraw = 13
	HashPowerIn = 14 //N次方新增：爆仓扣币折算算力入账（币未返还，记录折算流水）
)

const VoteText = "参与众筹"
const BlockOutText = "区块链出账"
const BlockInText = "区块链入账"
const FeeText = "手续费"
const FusdPointText = "积分返还"

const DynamicShardText = "动态分享收益"
const DynamicTeamText = "动态团队收益"
const StaticRewardText = "静态收益"
const SuccessWithdrawText = "成功本金100%退还"
const Failed100WithdrawText = "失败100%退还"
const Failed50WithdrawText = "失败50%退还"
const MiningRewardText = "挖矿收益提取"
const SuperAdmin100WithdrawText = "100%返还"
const HashPowerInText = "爆仓算力折算" //N次方新增：倒2/倒3 轮失败扣50%币折算算力入账


