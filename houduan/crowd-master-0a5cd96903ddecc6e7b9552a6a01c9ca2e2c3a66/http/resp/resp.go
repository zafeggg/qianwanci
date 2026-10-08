package resp

import (
	"time"
)

//RoundDetail 轮详情 我要参与
type RoundDetail struct {
	Balance     float64 `json:"balance"`     //我的余额
	Vote        float64 `json:"vote"`        //我参与筹集
	TargetVote  float64 `json:"targetVote"`  //筹集目标
	CurrentVote float64 `json:"currentVote"` //已筹集
	Status      uint    `json:"status"`      //轮状态: 0未开始 1进行中 2待结算 3已结束
	Symbol      string  `json:"symbol"`      //代币
	Icon        string  `json:"icon"`
	Usdt        float64 `json:"usdt"`
}

//ProjectDetail 项目详情
type ProjectDetail struct {
	Vote      float64 `json:"vote"`      //本期众筹数量
	Reward    float64 `json:"reward"`    //本期盈利
	RoundList []Round `json:"roundList"` //轮列表
}

type Home struct {
	ProjectList []Project `json:"projectList"` //期列表
	RoundList   []Round   `json:"roundList"`   //轮列表
	NotifyList  []Notify  `json:"notifyList"`  //通知列表
}

type Project struct {
	Id     uint   `json:"id"`
	Period uint   `json:"period"` //期
	Symbol string `json:"symbol"` //代币
	Status uint   `json:"status"` //状态： 0未开始 1进行中 2停止
}

type Round struct {
	Id          uint      `json:"id"`
	ProjectId   uint      `json:"projectId"`
	Period      uint      `json:"period"`      //期
	Round       uint      `json:"round"`       //轮
	TargetVote  float64   `json:"targetVote"`  //目标众筹数量
	MinVote     float64   `json:"minVote"`     //最小投入数量
	MaxVote     float64   `json:"maxVote"`     //最大投入数量
	CurrentVote float64   `json:"currentVote"` //众筹数量
	StartTime   time.Time `json:"startTime"`   //开始时间
	EndTime     time.Time `json:"endTime"`     //结束时间
	Status      uint      `json:"status"`      //0未开始 1进行中 2待结算 3已结束
	RewardRate  float64   `json:"rewardRate"`  //收益率
	Symbol      string    `json:"symbol"`      //代币
}

type Notify struct {
	Id         uint      `json:"id"`
	Content    string    `json:"content"` //通知内容
	CreateTime time.Time `json:"createTime"`
}

//VoteRecord 众筹记录
type VoteRecord struct {
	Id         uint      `json:"id"`
	Period     uint      `json:"period"` //期
	Round      uint      `json:"round"`  //轮
	Amount     float64   `json:"amount"` //金额
	CreateTime time.Time `json:"createTime"`
	Symbol     string    `json:"symbol"`
}

//Asset 资产代币
type Asset struct {
	Address string  `json:"address"` //充币地址
	Symbol  string  `json:"symbol"`  //代币符号
	Price   float64 `json:"price"`   //价格
	Icon    string  `json:"icon"`    //图片地址
	Amount  float64 `json:"amount"`  //数量
}

//Wallet 钱包信息
type Wallet struct {
	Name        string  `json:"name"`        //钱包名称
	USDT        float64 `json:"usdt"`        //USDT金额
	Address     string  `json:"address"`     //地址
	UsdtAddress string  `json:"usdtAddress"` //usdt地址
	AssetList   []Asset `json:"assetList"`   //资产
}

//WalletTx 钱包交易记录
type WalletTx struct {
	In         bool      `json:"in"`   //0入账或者1出账
	Desc       string    `json:"desc"` //备注信息
	From       string    `json:"from"`
	To         string    `json:"to"`
	Hash       string    `json:"hash"`
	Amount     float64   `json:"amount"`     //数量
	Success    uint      `json:"success"`    //0成功 1失败
	CreateTime time.Time `json:"createTime"` //时间
}

//Community 我的社区
type Community struct {
	Total       float64   `json:"total"`       //总数量
	F1          float64   `json:"f1"`          //F1数量
	F2          float64   `json:"f2"`          //F2数量
	F3          float64   `json:"f3"`          //F3数量
	InvitedList []Invited `json:"invitedList"` //邀请信息
}

//Invited 邀请信息
type Invited struct {
	Name    string `json:"name"`    //名称
	Address string `json:"address"` //地址
	Active  bool   `json:"active"`  //是否激活
}

//MiningHome 矿机首页
type MiningHome struct {
	Fusd        float64         `json:"fusd"`        //用户fusd
	Buff        float64         `json:"buff"`        //算力提升
	MachineList []MiningMachine `json:"machineList"` //矿机列表
	LossList    []MiningLoss    `json:"lossList"`    //亏损列表
}

//MiningMachine 矿机
type MiningMachine struct {
	Id   uint    `json:"id"`   //矿机ID
	Fusd float64 `json:"fusd"` //矿机所需FUSD
	Name string  `json:"name"` //矿机名称
	Pcb  float64 `json:"pcb"`  //PCB量
}

//MiningLoss 损失记录
type MiningLoss struct {
	Id         uint      `json:"id"`
	Period     uint      `json:"period"` //期
	Round      uint      `json:"round"`  //轮
	Address    string    `gorm:"index"`  //钱包地址
	Loss       float64   `json:"loss"`   //亏损收益
	Symbol     string    `json:"symbol"` //代币
	CreateTime time.Time `json:"createTime"`
}

//MiningReward 收益记录
type MiningReward struct {
	Id         uint      `json:"id"`
	Reward     float64   `json:"reward"`
	Hash       string    `json:"hash"`
	CreateTime time.Time `json:"createTime"`
}

//MiningPower 我的算力
type MiningPower struct {
	TotalPcb    float64 `json:"totalPcb"`    //总算力
	Pcb         float64 `json:"pcb"`         //个人算力
	TotalReward float64 `json:"totalReward"` //总收益
	Reward      float64 `json:"reward"`      //剩余收益
}

type CaptchaResponse struct {
	CaptchaId string `json:"captchaId"`
	ImageUrl  string `json:"imageUrl"`
}
