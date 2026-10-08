package core

import (
	"com.fibonacci.crowd/model"
	"gorm.io/gorm"
	"time"
)

//OnBoarding wallet lifetime
type OnBoarding interface {
	//Registration 注册钱包
	Registration(name string, code string, password string) (wallet model.Wallet, err error)

	//Recover 恢复钱包
	Recover(sign string, password string) (wallet model.Wallet, err error)

	//Invite 邀请
	Invite(fromCode string, toCode string) error

	//InviteWithTx 邀请伴随tx
	InviteWithTx(tx *gorm.DB, fromWalletId uint, toWalletId uint) error

	//Charge 充值
	Charge(wallet model.Wallet, from string, symbol string, amount uint64, hash string) error

	//ChargePoint 充值
	ChargePoint(address string, symbol string, amount uint64) error

	//Promote 提升等级
	Promote(walletId uint, projectId uint) error

	//SetLevel 设置级别
	SetLevel(wallet model.Wallet, level int) error
}

//Projecting 项目
type Projecting interface {
	//Init 初始化一个项目
	Init(symbol string, period uint) (project model.Project, err error)

	//Start 开始一个项目
	Start(project model.Project) error

	//Stop 停止一个项目
	Stop(project model.Project) error

	//Current 获取当前进行轮项目
	Current(project model.Project) (round model.ProjectRound, err error)

	//Next 获取下一轮项目
	Next(project model.Project) (round model.ProjectRound, err error)
}

//Rounding 项目轮
type Rounding interface {
	//Init 初始化项目轮
	Init(projectId uint, target, min, max float64, st, et time.Time) (round model.ProjectRound, err error)

	//Begin 开始项目轮
	Begin(round model.ProjectRound) error

	//Active 激活参与项目轮用户
	Active(round model.ProjectRound) error

	//End 结束项目轮
	End(round model.ProjectRound) error
}

//Voting 投入
type Voting interface {

	//Vote 投入项目
	Vote(wallet model.Wallet, round model.ProjectRound, amount float64) (vote model.Vote, err error)
}

//Rewarding 收益
type Rewarding interface {
	//Success 结算项目轮成功
	Success(round model.ProjectRound)

	//FailedNormal 正常结算退款
	FailedNormal(round model.ProjectRound)

	//Failed 结算项目轮失败
	Failed(rounds []model.ProjectRound)


	//GetNoAdminVotes 获取非超级账号投资信息
	GetNoAdminVotes(round model.ProjectRound) (votes []model.Vote)

	//GetAdminVotes 获取非超级账号投资信息
	GetAdminVotes(round model.ProjectRound) (votes []model.Vote)
}

//Mining 挖矿
type Mining interface {
	//Exchange 交换 fusd 换算力参与挖矿
	Exchange(wallet model.Wallet, fusd float64, pcb float64) error

	//Reward 眉笔兑换收益入账
	Reward(exchangeId uint, reward float64) error

	//Withdraw 提取收益
	Withdraw(wallet model.Wallet, reward float64) error
}

type WalletPointing interface {

	//GetOrInitWalletPoint 获取并且创建积分
	GetOrInitWalletPoint(tx *gorm.DB, address string, symbol string) model.WalletPoint

	//AddPointAmount 添加积分
	AddPointAmount(tx *gorm.DB, address string, symbol string, amount float64) (amountWithDecimal uint64, err error)

}

// HashPowering 算力矿机（N次方新增）
// 爆仓补偿：倒2/倒3 轮失败时扣 50% 币不退，按爆仓价折算为算力进入矿机挖矿，直到三倍（金本位）出局。
type HashPowering interface {
	//BookPower 爆仓折算入账：amount 为被扣币数量（= 投入额 × 50%），price 为爆仓时币价(U)
	BookPower(tx *gorm.DB, address string, symbol string, amount float64, price float64) error

	//DailyReward 每日产币：按算力占比分配矿池日产出（由 cmd/hashpower 定时任务调用）
	DailyReward(rewardDailyFibo float64) error

	//Withdraw 提取矿机产出（FIBO），最低起提 MiningWithdrawMinCount 个
	Withdraw(wallet model.Wallet, amount float64) error

	//SwitchMode 用户自选算力出局模式（需求#8）；只允许调整未产出、未出局的账户
	SwitchMode(address string, symbol string, mode uint) (updated int64, err error)

	//Home 我的算力矿机首页数据：账户列表 + 汇总
	Home(address string) (HashPowerHome, error)
}

// HashPowerHome 算力矿机首页聚合数据
type HashPowerHome struct {
	AccountList  []model.HashPower //算力账户列表
	TotalPower   float64           //总算力(U)
	TotalReward  float64           //累计产出(币本位)
	RemainReward float64           //剩余可提(币本位)
}
