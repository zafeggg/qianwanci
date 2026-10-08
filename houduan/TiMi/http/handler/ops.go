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
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"math/rand"
	"strconv"
	"strings"
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
	//安全口径（2026-09-18 E 模块加固）：**移除硬编码默认口令回退**（原为 blx/blxup2022city）。
	//理由：默认口令是公开已知值，一旦部署时漏配 yml 就等于运维后台裸奔（17 条高危写路由）。
	//现在未配置即拒绝登录，并在启动/首次登录时告警，强制部署方显式配置。
	opsAccount := config.EtcConfig.Ops.Account
	opsPwd := config.EtcConfig.Ops.Password
	if opsAccount == "" || opsPwd == "" {
		logrus.Warnln("[ops] 未配置 Ops.Account / Ops.Password，运维后台登录已禁用；请在 yml 显式配置强口令")
		return c.Send(utils.JsonFail("运维后台未配置登录凭据，已禁用（请在 yml 配置 Ops.Account/Ops.Password）"))
	}

	accStr, _ := account.(string)
	pwdStr, _ := pwd.(string)
	//常量时间比较，避免按字符比对泄漏口令长度/前缀（与 ApiKeyMiddleware 同口径）
	accOK := subtle.ConstantTimeCompare([]byte(accStr), []byte(opsAccount)) == 1
	pwdOK := subtle.ConstantTimeCompare([]byte(pwdStr), []byte(opsPwd)) == 1
	if accOK && pwdOK {
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

	//修复：原用不存在的 `pub_key` 字段查询导致接口稳定失败（见 crowd-master 解析报告 §9.2），
	//改为 KTO 主链地址（model.Wallet.Address）查询；兼容按波场地址兜底一次。
	var wallet model.Wallet
	if err := config.MysqlDBPool.Table(model.WalletTable).First(&wallet, "`address` = ?", address).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			if err = config.MysqlDBPool.Table(model.WalletTable).First(&wallet, "`tron_address` = ?", address).Error; err != nil {
				return errors.New("钱包不存在")
			}
		} else {
			return errors.New("钱包不存在")
		}
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
	if sign, err = utils.AesEncrypt([]byte(ktoPri), utils.AesSecretKey()); err != nil {
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

// ============================== 迭代0：运行参数（config 表 params 列）维护 ==============================

// GetParams 读取 config 表 params（当前生效的 JSON 参数对象；未配置返回空对象）
func (h OpsHandler) GetParams(c *fiber.Ctx) error {
	var cfg model.Config
	err := config.MysqlDBPool.Table(model.ConfigTable).Order("`id` asc").First(&cfg).Error
	if err == gorm.ErrRecordNotFound {
		return c.Send(utils.JsonOk(fiber.Map{"params": fiber.Map{}}))
	}
	if err != nil {
		return errors.New("读取配置失败")
	}
	var m map[string]interface{}
	if cfg.Params != "" {
		if jerr := json.Unmarshal([]byte(cfg.Params), &m); jerr != nil {
			return errors.New("params JSON 解析失败")
		}
	}
	if m == nil {
		m = make(map[string]interface{})
	}
	return c.Send(utils.JsonOk(fiber.Map{"params": m}))
}

// SetParams 写入 config 表 params 并热加载。默认**合并/补丁语义**，`?mode=replace` 才整体替换。
//
// 请求体为 NpowerParams 键值 JSON（键名见 config/params.go），如 {"FeeSymbol":"TM","StaticRewardRate":0.13}。
//
// ⚠ 2026-09-30 修复：原实现是**整体覆盖** —— `json.Unmarshal` body 后直接写回 params 列，
// 而注释却写"0/空串字段会被忽略"。后果：只想调一个限流阈值，调
// `{"WriteRatePerMinute":120}` 会把库里既有的 FeeSymbol / MinRound / CurrentStage 等
// **一并抹掉**，热重载后静默回落到代码默认值，属于"改一个参数、悄悄改坏一串参数"的运维事故源。
//
// 两种语义：
//   - 默认（合并/patch）：只覆盖 body 里出现的键，其余保持不动；值为 `null` 表示**删除该键**
//     （JSON Merge Patch 惯例），用于"把某参数恢复成不存在"。
//   - `?mode=replace`（整体替换）：只保留 body 里的键，其余移除。
//     ⚠ 联调脚本（privchain-rounds/hashpower/multiteam/withdraw-limits/f2f3）**依赖这一语义**：
//     它们 GET 全量 params 备份、改一个字段再整体写回，并把"少一个键"当作"删掉该参数"
//     （例如 hashpower 脚本要移除 FiboStaticPrice 复现"行情不可达"）。这些脚本已显式传
//     `?mode=replace`；默认合并是为了保护运维手改单个参数的场景。
//
// 注意：0/空值仍按既有约定在 ApplyNpowerParams 层视为"不覆盖默认值"；要显式取消限制请用 -1
// （见 core/impl/params.go 的提现额度/限流阈值约定）。
func (h OpsHandler) SetParams(c *fiber.Ctx) error {
	var patch map[string]interface{}
	if err := json.Unmarshal(c.Body(), &patch); err != nil {
		return errors.New("参数必须为 JSON 对象")
	}
	if len(patch) == 0 {
		return errors.New("参数为空")
	}
	replaceAll := strings.EqualFold(strings.TrimSpace(c.Query("mode")), "replace")

	var cfg model.Config
	err := config.MysqlDBPool.Table(model.ConfigTable).Order("`id` asc").First(&cfg).Error
	switch {
	case err == gorm.ErrRecordNotFound:
		bs, merr := json.Marshal(patch)
		if merr != nil {
			return errors.New("参数序列化失败")
		}
		cfg = model.Config{Params: string(bs)}
		if err = config.MysqlDBPool.Table(model.ConfigTable).Create(&cfg).Error; err != nil {
			return errors.New("创建配置记录失败: " + err.Error())
		}
	case err != nil:
		return errors.New("读取配置失败: " + err.Error())
	default:
		// 以既有 JSON 为基线（replace 模式则从空基线开始，实现"整体替换/按键移除"）
		merged := map[string]interface{}{}
		if !replaceAll && strings.TrimSpace(cfg.Params) != "" {
			if uerr := json.Unmarshal([]byte(cfg.Params), &merged); uerr != nil {
				// 库里是坏 JSON：不能拿它当基线（否则会把坏数据"合并"下去），
				// 明确报错让运维先修，而不是静默丢弃既有配置
				return errors.New("库内 params 不是合法 JSON，请先修复后再写入：" + uerr.Error())
			}
		}
		for k, v := range patch {
			if v == nil {
				delete(merged, k) //null = 删除该键（合并模式下的显式移除）
				continue
			}
			merged[k] = v
		}
		bs, merr := json.Marshal(merged)
		if merr != nil {
			return errors.New("参数序列化失败")
		}
		if err = config.MysqlDBPool.Table(model.ConfigTable).Where("`id` = ?", cfg.ID).
			Update("params", string(bs)).Error; err != nil {
			return errors.New("更新 params 失败: " + err.Error())
		}
		patch = merged // 回显合并后的完整配置，便于调用方确认没被抹掉
	}

	//热应用：以 config 表为新基线重载（yml 覆盖优先级更高仍生效）
	impl.LoadNpowerParams()
	//复盘 Wave-4：广播参数变更，订阅该频道的程序（npower/round/hashpower/burn/mining/demo）免重启生效
	if config.RedisClient != nil {
		config.RedisClient.Publish(context.Background(), impl.NpowerParamsChannel, "1")
	}
	modeName := "merge"
	if replaceAll {
		modeName = "replace"
	}
	return c.Send(utils.JsonOk(fiber.Map{"params": patch, "mode": modeName}))
}

// SetHashPowerMode 运营设置算力矿机账户模式：mode = 0 默认三倍出局 / 1 最长300天三倍出局 / 2 TM加速。
// 按 id（或 address）定位账户；已出局(status=1)账户也可改（仅影响后续产币/保底判定）。
func (h OpsHandler) SetHashPowerMode(c *fiber.Ctx) error {
	var request struct {
		Id      uint   `json:"id"`
		Address string `json:"address"`
		Mode    uint   `json:"mode"`
	}
	if err := json.Unmarshal(c.Body(), &request); err != nil {
		return errors.New("参数错误")
	}
	if request.Mode != impl.HashPowerModeDefault && request.Mode != impl.HashPowerMode300 && request.Mode != impl.HashPowerModeTM {
		return errors.New("mode 必须为 0/1/2")
	}
	if request.Id == 0 && request.Address == "" {
		return errors.New("id 与 address 至少传一个")
	}

	q := config.MysqlDBPool.Table(model.HashPowerTable)
	if request.Id != 0 {
		q = q.Where("`id` = ?", request.Id)
	} else {
		q = q.Where("`address` = ?", request.Address)
	}
	res := q.Update("mode", request.Mode)
	if res.Error != nil {
		return errors.New("更新失败: " + res.Error.Error())
	}
	if res.RowsAffected == 0 {
		return errors.New("未找到匹配的算力矿机账户")
	}
	return c.Send(utils.JsonOk("成功"))
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
