package req

//Registration 创建钱包请求
type Registration struct {
	Code     string `json:"code"`     //邀请码
	Name     string `json:"name"`     //钱包名称
	Password string `json:"password"` //钱包密码
}

//Recover 导入钱包请求
type Recover struct {
	Sign string `json:"sign"` //私钥
	Pwd  string `json:"pwd"`  //密码
}

//ModifyPwd 修改密码请求
type ModifyPwd struct {
	OldPwd string `json:"oldPwd"` //旧密码
	NewPwd string `json:"newPwd"` //新密码
}

//ExportPri 导出私钥请求
type ExportPri struct {
	Pwd string `json:"pwd"` //密码
}

//Vote 投资项目轮请求
type Vote struct {
	RoundId uint    `json:"roundId"` //轮ID
	Pwd     string  `json:"pwd"`     //密码
	Amount  uint `json:"amount"`  //数量
	CaptchaId string `json:"captchaId"` //验证ID
	CaptchaSolution string `json:"captchaSolution"` //图片解决
}


//Withdraw 提币
type Withdraw struct {
	Address string `json:"address"` //提现地址
	Symbol string `json:"symbol"` //代币符号
	Amount float64 `json:"amount"` //金额
	Pwd     string  `json:"pwd"`     //密码
}

//MiningExchange 矿机兑换请求
type MiningExchange struct {
	MineMachineId  uint `json:"mineMachineId"` //矿机ID
}

//MiningWithdraw 矿机提取请求
type MiningWithdraw struct {
	Amount float64 `json:"amount"` //金额
	Pwd     string  `json:"pwd"`     //密码
}