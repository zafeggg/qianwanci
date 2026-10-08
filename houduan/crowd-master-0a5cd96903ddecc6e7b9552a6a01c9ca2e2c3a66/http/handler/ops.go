package handler

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core"
	"com.fibonacci.crowd/core/impl"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils"
	"com.fibonacci.crowd/utils/kto"
	"com.fibonacci.crowd/utils/tron"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"math/rand"
	"strconv"
	"time"
)

type OpsHandler struct {
	ProjectManager   core.Projecting
	RoundManager     core.Rounding
	WalletManager    core.OnBoarding
	RewardingManager core.Rewarding
}

func (h OpsHandler) Sign(c *fiber.Ctx) error {
	var request fiber.Map
	if err := c.BodyParser(&request); err != nil {
		return err
	}

	account := request["account"]
	pwd := request["pwd"]
	if account == "blx" && pwd == "blxup2022city" {
		token, err := GenerateToken(0, "超级钱包", 24*time.Hour)
		if err != nil {
			return errors.New("生成签名错误")
		}

		return c.Send(utils.JsonOk(token))
	}

	return c.Send(utils.JsonFail("账号密码错误"))
}

func (h OpsHandler) StartProject(c *fiber.Ctx) error {
	var request fiber.Map
	if err := c.BodyParser(&request); err != nil {
		return err
	}

	symbol := request["symbol"]
	period := request["period"].(float64)
	if symbol == "" {
		return errors.New("请输入代币符号")
	}

	if period == 0 {
		return errors.New("无效的期数")
	}

	project, err := h.ProjectManager.Init(symbol.(string), uint(period))
	if err != nil {
		return c.Send(utils.JsonFail(err.Error()))
	}

	err = h.ProjectManager.Start(project)
	if err != nil {
		return c.Send(utils.JsonFail(err.Error()))
	}

	return c.Send(utils.JsonOk(project))
}

func (h OpsHandler) SetRound(c *fiber.Ctx) error {
	var request fiber.Map
	if err := c.BodyParser(&request); err != nil {
		return err
	}

	projectId := request["projectId"].(float64)

	var target float64
	if request["target"] != nil {
		target = request["target"].(float64)
	}

	min := request["min"].(float64)
	max := request["max"].(float64)
	startTimeStr := request["startTime"].(string)
	endTimeStr := request["endTime"].(string)

	startTime, err := time.ParseInLocation("2006-01-02 15:04:05", startTimeStr, time.Local)
	if err != nil {
		return errors.New("开始时间错误" + err.Error())
	}
	endTime, err := time.ParseInLocation("2006-01-02 15:04:05", endTimeStr, time.Local)
	if err != nil {
		return errors.New("结束时间错误" + err.Error())
	}

	round, err := h.RoundManager.Init(uint(projectId), target, min, max, startTime, endTime)
	if err != nil {
		return errors.New("新建项目轮错误" + err.Error())
	}

	return c.Send(utils.JsonOk(round))
}

func (h OpsHandler) BatchCreateWallet(c *fiber.Ctx) error {
	return nil
}

func (h OpsHandler) SetLevel(c *fiber.Ctx) error {
	var request fiber.Map
	if err := c.BodyParser(&request); err != nil {
		return err
	}

	address := request["address"].(string)
	level := request["level"].(float64)

	var wallet model.Wallet
	//修复存量bug：wallet 表无 pub_key 列，按地址查询应使用 address 列（原写法该接口稳定失败）
	if err := config.MysqlDBPool.Table(model.WalletTable).First(&wallet, "address = ?", address).Error; err != nil {
		return errors.New("钱包不存在")
	}
	err := h.WalletManager.SetLevel(wallet, int(level))
	if err != nil {
		return errors.New("设置错误: " + err.Error())
	}

	return c.Send(utils.JsonOk("成功"))
}

func (h OpsHandler) FullVote(c *fiber.Ctx) error {
	return nil
}

func (h OpsHandler) EndRound(c *fiber.Ctx) error {
	var request fiber.Map
	if err := c.BodyParser(&request); err != nil {
		return err
	}

	roundId := request["roundId"].(float64)
	act := request["act"].(string)

	if act == "" {
		return errors.New("请输入结束行为Success/FailedNormal/Failed")
	}

	var round model.ProjectRound
	err := config.MysqlDBPool.Table(model.ProjectRoundTable).First(&round, "`id` = ?", roundId).Error
	if err != nil {
		return errors.New("轮不存在")
	}

	//	//Success 结算项目轮成功
	//	Success(round model.ProjectRound)
	//
	//	//FailedNormal 正常结算退款
	//	FailedNormal(round model.ProjectRound)
	//
	//	//Failed 结算项目轮失败
	//	Failed(rounds []model.ProjectRound)
	switch act {
	case "Success":
		h.RewardingManager.Success(round)
	case "FailedNormal":
		h.RewardingManager.FailedNormal(round)

	case "Failed":
		h.RewardingManager.Failed([]model.ProjectRound{round})
	default:
		return errors.New("错误结束指令")
	}

	return c.Send(utils.JsonOk("结束成功"))
}

func (h OpsHandler) TestAccounts(c *fiber.Ctx) error {
	var request fiber.Map
	if err := c.BodyParser(&request); err != nil {
		return err
	}

	name := request["name"].(string)
	code := request["code"].(string)
	count := request["count"].(float64)
	symbol := request["symbol"].(string)
	amount := request["amount"].(float64)

	wallets := make([]string, 0)
	for i := 0; i < int(count); i++ {
		wallet, err := h.WalletManager.Registration(name+"_"+strconv.Itoa(i), code, "123456")
		if err != nil {
			return errors.New("钱包错误")
		}
		wallets = append(wallets, wallet.Name)

		h.WalletManager.ChargePoint(wallet.Address, symbol, uint64(amount))
	}

	return c.Send(utils.JsonOk(wallets))
}

func (h OpsHandler) Charge(c *fiber.Ctx) error {
	var request fiber.Map
	if err := c.BodyParser(&request); err != nil {
		return err
	}

	address := request["address"].(string)
	symbol := request["symbol"].(string)
	amount := request["amount"].(float64)

	var wallet model.Wallet
	err := config.MysqlDBPool.Table(model.WalletTable).First(&wallet, "address = ?", address).Error
	if err != nil {
		return err
	}

	return h.WalletManager.ChargePoint(wallet.Address, symbol, uint64(amount))
}

func (h OpsHandler) SupperWallet(c *fiber.Ctx) error {
	var err error
	var ktoPub string
	var ktoPri string
	var tronPub string
	var tronPri string
	//1.生成波场钱包
	if tronPub, tronPri, err = tron.CreateAccount(); err != nil {
		return errors.New("创建钱包失败" + err.Error())
	}

	//2.生成kto钱包
	if ktoPub, ktoPri, err = kto.CreateWallet(); err != nil {
		return errors.New("创建钱包失败" + err.Error())
	}

	//3.用kto作为主钱包
	wallet := new(model.Wallet)
	wallet.Address = ktoPub
	wallet.Private = ktoPri
	wallet.TronAddress = tronPub
	wallet.TronPrivate = tronPri
	wallet.Name = "管理员" + ktoPub
	wallet.Admin = true //diff

	initPwd := "bk2021-"
	var encryptPwd string
	if encryptPwd = utils.HashAndSalt([]byte(initPwd)); err != nil {
		return err
	}
	//4.加密私钥 需要密码解密
	var sign []byte
	if sign, err = utils.AesEncrypt([]byte(ktoPri), []byte(utils.SECRET_KEY)); err != nil {
		return err
	}
	wallet.Password = encryptPwd
	wallet.Sign = hex.EncodeToString(sign)

	rand.Seed(time.Now().UnixNano())
	//fromCode := utils.GetString(8)
	err = config.MysqlDBPool.Transaction(func(tx *gorm.DB) error {
		//wallet.Code = fromCode
		if err = tx.Model(model.Wallet{}).Create(&wallet).Error; err != nil {
			return err
		}
		return nil
	})

	return c.Send(utils.JsonOk(wallet))
}

type F3VoteCount struct {
	Amount float64
	Count  uint
}

func (h OpsHandler) GetDataZmy(c *fiber.Ctx) error {
	//1.获取当天开的币种列表
	now := time.Now()
	minNowDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	maxNowDate := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.Local)

	var rounds []model.ProjectRound
	err := config.MysqlDBPool.Table(model.ProjectRoundTable).Where("`start_time` > ? and `start_time` < ?", minNowDate, maxNowDate).Find(&rounds).Error
	if err != nil {
		return errors.New("查询轮失败")
	}
	var f3s []model.Wallet
	err = config.MysqlDBPool.Table(model.WalletTable).Where("`level` = ?", enum.Level3).Find(&f3s).Error
	if err != nil {
		return errors.New("查F3失败")
	}
	f3sMap := make(map[uint]model.Wallet)
	for _, wallet := range f3s {
		f3sMap[wallet.ID] = wallet
	}

	f3CountVoteMap := make(map[uint]F3VoteCount)
	symbolRoundCountMap := make(map[string]map[uint]uint)
	symbolLockAmountMap := make(map[string]float64)
	symbols := make([]string, 0)
	for _, round := range rounds {
		symbol := round.Symbol
		id := round.ID
		var votes []model.Vote
		err = config.MysqlDBPool.Table(model.VoteTable).Where("`round_id` = ?", id).Find(&votes).Error
		if err != nil {
			logrus.Error("查询round的投票信息失败", id, err)
			continue
		}

		var addrCount uint
		for _, _ = range votes {
			addrCount += 1
		}

		//2.f3业绩
		for _, wallet := range f3s {
			walletId := wallet.ID

			//1.找到所有子代
			var descendants []uint
			err = config.MysqlDBPool.Table(model.WalletTreeTable).Select("`descendant`").Find(&descendants, "`ancestor` = ? and `distance` >= 0", walletId).Error
			if err != nil {
				logrus.Error("查询所有descendants失败", err)
				continue
			}
			//to map
			descendantsMap := make(map[uint]uint)
			for _, ds := range descendants {
				descendantsMap[ds] = 1
			}

			var count uint
			var amount float64
			for _, vote := range votes {
				//self
				if vote.WalletId == walletId {
					count += 1
					amount += vote.Amount
				}

				//descendants
				if _, ok := descendantsMap[vote.WalletId]; ok {
					count += 1
					amount += vote.Amount
				}
			}

			if _, ok := f3CountVoteMap[walletId]; !ok {
				f3CountVoteMap[walletId] = F3VoteCount{Count: count, Amount: amount}
			} else {
				f3vc := f3CountVoteMap[walletId]
				f3vc.Amount += amount
				f3vc.Count += count
				f3CountVoteMap[walletId] = f3vc
			}
		}

		if _, ok := symbolRoundCountMap[symbol]; !ok {
			symbols = append(symbols, symbol)

			symbolRoundCountMap[symbol] = make(map[uint]uint)
			symbolRoundCountMap[symbol][1] = addrCount

			var in uint64
			var out uint64
			err = config.MysqlDBPool.Table(model.WalletTxTable).Select("SUM(`amount`)").Find(&in, "`desc` = ? and `symbol` = ?", enum.BlockInText, symbol).Error
			if err != nil {
				logrus.Error("统计区块入帐错误", err)
				continue
			}
			err = config.MysqlDBPool.Table(model.WalletTxTable).Select("SUM(`amount`)").Find(&out, "`desc` = ? and `symbol` = ?", enum.BlockOutText, symbol).Error
			if err != nil {
				logrus.Error("统计区块入帐错误", err)
				continue
			}

			left := in - out
			symbolLockAmountMap[symbol] = float64(left) / config.SymbolDictionary[symbol]
		} else {
			srm := symbolRoundCountMap[symbol]
			i := len(srm)
			srm[uint(i+1)] = addrCount
			symbolRoundCountMap[symbol] = srm
		}
	}

	result := make(fiber.Map)
	symbolResult := make(fiber.Map)
	for _, symbol := range symbols {
		rcm := symbolRoundCountMap[symbol]
		slm := symbolLockAmountMap[symbol]

		var total uint
		for i := 0; i < len(rcm); i++ {
			total += rcm[uint(i)]
		}
		symbolResult[symbol] = map[string]interface{}{
			"目前为止持仓总量": slm,
			"轮次":       len(rcm),
			"轮次信息":     rcm,
			"总成功人次":    total,
		}
	}
	result["币种"] = symbolResult

	f3Result := make(fiber.Map)
	for walletId, count := range f3CountVoteMap {
		wallet := f3sMap[walletId]

		f3Result[wallet.Name] = map[string]interface{}{
			"伞下参与人数": count.Count,
			"投资额":    count.Amount,
		}
	}

	result["F3业绩"] = f3Result
	return c.Send(utils.JsonOk(result))
}

type SymbolCoinInfo struct {
	YesAmount        uint64
	CurrentAddAmount uint64
}

func (h OpsHandler) GetDataZmy2(c *fiber.Ctx) error {
	queryTimeStr := c.Query("queryTime")
	if queryTimeStr == "" {
		return errors.New("查询时间必传")
	}

	queryTime, err := time.ParseInLocation("2006-01-02 15:04:05", queryTimeStr, time.Local)
	if err != nil {
		return errors.New(fmt.Sprintf("解析时间错误:" + err.Error()))
	}

	now := queryTime
	minNowDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	maxNowDate := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.Local)

	yes := now.AddDate(0, 0, -1)
	minYesDate := time.Date(yes.Year(), yes.Month(), yes.Day(), 0, 0, 0, 0, time.Local)
	maxYesDate := time.Date(yes.Year(), yes.Month(), yes.Day(), 23, 59, 59, 0, time.Local)

	//1.获取币种列表
	symbolMap := make(map[string]map[string]interface{})
	for symbol, _ := range config.SymbolDictionary {
		//1.截至昨日充币量
		var yesAmount uint64
		err := config.MysqlDBPool.Table(model.WalletTxTable).Select("SUM(`amount`)").Where("`symbol` = ? and `desc` = ? and `created_at` <= ?", symbol, enum.BlockInText, maxYesDate).Find(&yesAmount).Error
		if err != nil {
			logrus.Error("查询截至昨天充币量失败", err)
			continue
		}

		//2.今日新增
		var todayAmount uint64
		err = config.MysqlDBPool.Table(model.WalletTxTable).Select("SUM(`amount`)").Where("`symbol` = ? and `desc` = ? and `created_at` >= ? and `created_at` <= ?", symbol, enum.BlockInText, minNowDate, maxNowDate).Find(&todayAmount).Error
		if err != nil {
			logrus.Error("查询截至昨天充币量失败", err)
		}

		//3.今日提币量
		var todayWithdrawAmount uint64
		err = config.MysqlDBPool.Table(model.WalletTxTable).Select("SUM(`amount`)").Where("`symbol` = ? and `desc` = ? and `created_at` >= ? and `created_at` <= ?", symbol, enum.BlockOutText, minNowDate, maxNowDate).Find(&todayWithdrawAmount).Error
		if err != nil {
			logrus.Error("查询截至昨天充币量失败", err)
		}

		//4.目前资金池总量
		var currentPoolAmount uint64
		err = config.MysqlDBPool.Table(model.WalletPointTable).Select("SUM(`amount`)").Where("`symbol` = ?", symbol).Find(&currentPoolAmount).Error
		if err != nil {
			logrus.Error("查询资金池总量失败", err)
		}

		//5.昨日参与投票地址
		var yesAddress []string
		err = config.MysqlDBPool.Table(model.VoteTable).Select("DISTINCT `address`").Where("`symbol` = ? and `created_at` >= ? and `created_at` <= ?", symbol, minYesDate, maxYesDate).Find(&yesAddress).Error
		if err != nil {
			logrus.Error("查询昨日参与投票地址失败", err)
		}

		//6.今日参与投票地址
		var todayAddress []string
		err = config.MysqlDBPool.Table(model.VoteTable).Select("DISTINCT `address`").Where("`symbol` = ? and `created_at` >= ? and `created_at` <= ?", symbol, minNowDate, maxNowDate).Find(&todayAddress).Error
		if err != nil {
			logrus.Error("查询昨日参与投票地址失败", err)
		}

		//6.1 获取提币信息
		var withdrawTxs []model.WalletTx
		err = config.MysqlDBPool.Table(model.WalletTxTable).Where("`symbol` = ? and `desc` = ? and `created_at` >= ? and `created_at` <= ?", symbol, enum.BlockOutText, minNowDate, maxNowDate).Find(&withdrawTxs).Error
		if err != nil {
			logrus.Error("查询提币信息失败", err)
		}

		//7.f3信息
		var f3s []model.Wallet
		err = config.MysqlDBPool.Table(model.WalletTable).Where("`level` = ?", enum.Level3).Find(&f3s).Error
		if err != nil {
			return errors.New("查F3失败")
		}

		f3sMap := make(map[uint]model.Wallet)
		for _, wallet := range f3s {
			f3sMap[wallet.ID] = wallet
		}

		f3CountVoteMap := make(map[uint]F3VoteCount)
		for _, wallet := range f3s {
			walletId := wallet.ID

			//1.找到所有子代
			var descendants []uint
			err = config.MysqlDBPool.Table(model.WalletTreeTable).Select("`descendant`").Find(&descendants, "`ancestor` = ? and `distance` >= 0", walletId).Error
			if err != nil {
				logrus.Error("查询所有descendants失败", err)
				continue
			}

			//2.self
			descendants = append(descendants, walletId)

			//3.find active wallets
			var activeTeamWallets []model.Wallet
			err = config.MysqlDBPool.Table(model.WalletTable).Find(&activeTeamWallets, "`id` IN ? and `active` = ?", descendants, true).Error
			if err != nil {
				logrus.Error("查询所有激活钱包数量失败", err)
				continue
			}

			addresses := make([]string, len(activeTeamWallets))
			for _, w := range activeTeamWallets {
				addresses = append(addresses, w.Address)
			}

			//4.find points
			var amount uint64
			err = config.MysqlDBPool.Table(model.WalletPointTable).Select("SUM(`amount`)").Find(&amount, "`address` IN ? and `symbol` = ?", addresses, symbol).Error
			if err != nil {
				logrus.Error("查询所有激活钱包数量失败", err)
			}

			if _, ok := f3CountVoteMap[walletId]; !ok {
				f3CountVoteMap[walletId] = F3VoteCount{Count: uint(len(addresses)), Amount: float64(amount)}
			} else {
				f3vc := f3CountVoteMap[walletId]
				f3vc.Amount += float64(amount)
				f3vc.Count += uint(len(addresses))
				f3CountVoteMap[walletId] = f3vc
			}
		}

		//8.组合信息
		symbolD := config.SymbolDictionary[symbol]
		if _, ok := symbolMap[symbol]; ok {
			logrus.Error("重复代币信息")
		} else {
			news := make(map[string]interface{})
			news["截至昨日充币量"] = float64(yesAmount) / symbolD
			news["今日新增"] = float64(todayAmount) / symbolD
			news["今日提币量"] = float64(todayWithdrawAmount) / symbolD
			news["目前资金池币总量"] = float64(currentPoolAmount) / symbolD
			news["昨日参与投票地址数量"] = float64(len(yesAddress))
			news["今日参与投票地址数量"] = float64(len(todayAddress))

			txs := make([]fiber.Map, 0)
			for _, tx := range withdrawTxs {
				var name string
				err = config.MysqlDBPool.Table(model.WalletTable).Select("name").Find(&name, "`address` = ? ", tx.From).Error
				if err != nil {
					logrus.Error("根据from获取钱包地址失败", err)
				}
				txs = append(txs, fiber.Map{
					"提现账号": name,
					"地址":   tx.From,
					"提现数量": float64(tx.Amount) / symbolD,
				})
			}
			news["今日提币信息"] = txs

			//1.readable
			f3Info := make(fiber.Map)
			for walletId, vm := range f3CountVoteMap {
				wallet := f3sMap[walletId]
				name := wallet.Name

				f3Info[name] = fiber.Map{
					"团队持币量":    vm.Amount / symbolD,
					"团队激活地址数量": vm.Count,
				}
			}
			news["F3信息"] = f3Info
			symbolMap[symbol] = news
		}
	}

	return c.Send(utils.JsonOk(symbolMap))
}

func NewOpsHandler() *OpsHandler {

	return &OpsHandler{
		WalletManager:    impl.NewWalletManager(),
		ProjectManager:   impl.NewProjectManager(),
		RoundManager:     impl.NewRoundManager(),
		RewardingManager: impl.NewRewardManager(),
	}
}
