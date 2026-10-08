package handler

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core"
	"com.fibonacci.crowd/core/impl"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/http/req"
	"com.fibonacci.crowd/http/resp"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils"
	"com.fibonacci.crowd/utils/kto"
	"com.fibonacci.crowd/utils/tron"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/dchest/captcha"
	"github.com/gofiber/fiber/v2"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"strconv"
	"sync"
	"time"
)

type WalletHandler struct {
	WalletManager core.OnBoarding
	VoteManager   core.Voting
	DB            *gorm.DB
	Lock          sync.Mutex
}

func NewWalletHandler() *WalletHandler {
	return &WalletHandler{WalletManager: impl.NewWalletManager(), DB: config.MysqlDBPool, VoteManager: impl.NewVoteManager()}
}

func (h *WalletHandler) Registration(c *fiber.Ctx) error {
	var err error
	var decryptData []byte
	if decryptData, err = DecryptData(c); err != nil {
		return c.Send(utils.JsonFail("decryptData error"))
	}

	var request req.Registration
	if err = json.Unmarshal(decryptData, &request); err != nil {
		return errors.New("参数错误")
	}

	code := request.Code
	name := request.Name
	password := request.Password

	var wallet model.Wallet
	if wallet, err = h.WalletManager.Registration(name, code, password); err != nil {
		return err
	}

	expr := time.Hour * 30 * 24 * 12 * 20
	token, err := GenerateToken(wallet.ID, wallet.Name, expr)
	if err != nil {
		return errors.New("生成授权码失败")
	}

	err = utils.LoginToken(wallet.ID, token, expr)
	if err != nil {
		log.Errorln("LoginToken err: ", err)
		return errors.New("设置授权码失败")
	}

	return c.Send(utils.JsonOk(fiber.Map{"token": token, "name": wallet.Name}))
}

func (h *WalletHandler) Recover(c *fiber.Ctx) error {
	var err error
	var decryptData []byte
	if decryptData, err = DecryptData(c); err != nil {
		return c.Send(utils.JsonFail("decryptData error"))
	}

	var request req.Recover
	if err = json.Unmarshal(decryptData, &request); err != nil {
		return errors.New("参数错误")
	}

	sign := request.Sign
	pwd := request.Pwd
	var wallet model.Wallet
	if wallet, err = h.WalletManager.Recover(sign, pwd); err != nil {
		return err
	}

	expr := time.Hour * 30 * 24 * 12 * 20
	token, err := GenerateToken(wallet.ID, wallet.Name, expr)
	if err != nil {
		return errors.New("生成授权码失败")
	}

	err = utils.LoginToken(wallet.ID, token, expr)
	if err != nil {
		log.Errorln("LoginToken err: ", err)
		return errors.New("设置授权码失败")
	}

	return c.Send(utils.JsonOk(fiber.Map{"token": token, "name": wallet.Name}))
}

func (h *WalletHandler) ModifyPwd(c *fiber.Ctx) error {
	var err error
	var walletId uint
	if walletId, err = GetWalletId(c); err != nil {
		return err
	}

	var wallet model.Wallet
	if err = h.DB.Table(model.WalletTable).First(&wallet, "`id` = ?", walletId).Error; err != nil {
		return errors.New("钱包不存在")
	}

	var decryptData []byte
	if decryptData, err = DecryptData(c); err != nil {
		return c.Send(utils.JsonFail("decryptData error"))
	}
	var request req.ModifyPwd
	if err = json.Unmarshal(decryptData, &request); err != nil {
		return errors.New("参数错误")
	}

	oldPwd := request.OldPwd
	newPwd := request.NewPwd
	if !utils.ComparePasswords(wallet.Password, []byte(oldPwd)) {
		return errors.New("密码错误")
	}

	//modify password
	wallet.Password = utils.HashAndSalt([]byte(newPwd))
	if err = h.DB.Table(model.WalletTable).Select("password").Updates(&wallet).Error; err != nil {
		return errors.New("更新错误")
	}

	return c.Send(utils.JsonOk(nil))
}

func (h *WalletHandler) ExportPri(c *fiber.Ctx) error {
	var err error
	var walletId uint
	if walletId, err = GetWalletId(c); err != nil {
		return err
	}

	var wallet model.Wallet
	if err = h.DB.Table(model.WalletTable).First(&wallet, "`id` = ?", walletId).Error; err != nil {
		return errors.New("钱包不存在")
	}

	var decryptData []byte
	if decryptData, err = DecryptData(c); err != nil {
		return c.Send(utils.JsonFail("decryptData error"))
	}
	var request req.ExportPri
	if err = json.Unmarshal(decryptData, &request); err != nil {
		return errors.New("参数错误")
	}

	password := request.Pwd
	if !utils.ComparePasswords(wallet.Password, []byte(password)) {
		return errors.New("密码错误")
	}

	return c.Send(utils.JsonOk(fiber.Map{"sign": wallet.Sign}))
}

func (h *WalletHandler) Tx(c *fiber.Ctx) error {
	var err error
	var walletId uint
	if walletId, err = GetWalletId(c); err != nil {

		return err
	}
	var wallet model.Wallet
	if err = h.DB.Table(model.WalletTable).First(&wallet, "`id` = ?", walletId).Error; err != nil {
		return errors.New("钱包不存在")
	}

	symbol := c.Params("symbol", "KTO")
	in, _ := c.ParamsInt("in", 0) //0全部 1转入 2转出

	var txList []model.WalletTx
	if err = h.DB.Table(model.WalletTxTable).Order("`created_at` DESC").Find(&txList, "`symbol` = ? and (`from` = ? or `to` = ?)", symbol, wallet.Address, wallet.Address).Error; err != nil {
		return errors.New("查询记录失败")
	}

	txs := make([]resp.WalletTx, 0)
	for _, tx := range txList {
		inWallet := tx.To == wallet.Address

		switch in {
		case 0:
			if inWallet {
				if tx.Desc != enum.BlockOutText {
					txs = append(txs, resp.WalletTx{
						In:         inWallet,
						Desc:       tx.Desc,
						From:       tx.From,
						Hash:       tx.Hash,
						To:         tx.To,
						Amount:     utils.FixedFloat2(float64(tx.Amount) / config.SymbolDictionary[tx.Symbol]),
						Success:    tx.Success,
						CreateTime: tx.CreatedAt,
					})
				}
			} else {
				if tx.Desc != enum.BlockInText {
					txs = append(txs, resp.WalletTx{
						In:         inWallet,
						Desc:       tx.Desc,
						From:       tx.From,
						Hash:       tx.Hash,
						To:         tx.To,
						Amount:     utils.FixedFloat2(float64(tx.Amount) / config.SymbolDictionary[tx.Symbol]),
						Success:    tx.Success,
						CreateTime: tx.CreatedAt,
					})
				}
			}
		case 1:
			if inWallet {
				if tx.Desc != enum.BlockOutText {
					txs = append(txs, resp.WalletTx{
						In:         inWallet,
						Desc:       tx.Desc,
						From:       tx.From,
						Hash:       tx.Hash,
						To:         tx.To,
						Amount:     utils.FixedFloat2(float64(tx.Amount) / config.SymbolDictionary[tx.Symbol]),
						Success:    tx.Success,
						CreateTime: tx.CreatedAt,
					})
				}
			}
		case 2:
			if !inWallet {
				if tx.Desc != enum.BlockInText {
					txs = append(txs, resp.WalletTx{
						In:         inWallet,
						Desc:       tx.Desc,
						From:       tx.From,
						Hash:       tx.Hash,
						To:         tx.To,
						Amount:     utils.FixedFloat2(float64(tx.Amount) / config.SymbolDictionary[tx.Symbol]),
						Success:    tx.Success,
						CreateTime: tx.CreatedAt,
					})
				}
			}
		}
	}
	return c.Send(utils.JsonOk(txs))
}

func (h *WalletHandler) Vote(c *fiber.Ctx) error {

	var err error
	var walletId uint
	if walletId, err = GetWalletId(c); err != nil {
		return err
	}

	var decryptData []byte
	if decryptData, err = DecryptData(c); err != nil {
		return c.Send(utils.JsonFail("decryptData error"))
	}
	var request req.Vote
	if err = json.Unmarshal(decryptData, &request); err != nil {
		return errors.New("参数错误")
	}

	roundId := request.RoundId
	amount := request.Amount
	pwd := request.Pwd
	captchaId := request.CaptchaId
	captchaSolution := request.CaptchaSolution
	log.WithFields(log.Fields{"Method": "Vote"}).Infoln("投入项目:", request, walletId)

	//2.check is vote and create
	h.Lock.Lock()
	defer h.Lock.Unlock()

	var wallet model.Wallet
	var round model.ProjectRound
	if err = h.DB.Table(model.WalletTable).First(&wallet, "`id` = ?", walletId).Error; err != nil {
		return errors.New("钱包不存在")
	}
	if err = h.DB.Table(model.ProjectRoundTable).First(&round, "`id` = ?", roundId).Error; err != nil {
		return errors.New("该轮不存在")
	}
	if !utils.ComparePasswords(wallet.Password, []byte(pwd)) {
		return errors.New("密码错误")
	}

	if captchaId == "" || captchaSolution == "" {
		return c.Send(utils.JsonFail("参数错误"))
	}
	if !captcha.VerifyString(captchaId, captchaSolution) {
		return c.Send(utils.JsonFailCode(700, "验证码错误"))
	}

	if _, err = h.VoteManager.Vote(wallet, round, float64(amount)); err != nil {
		return err
	}
	return c.Send(utils.JsonOk("成功"))
}

func (h *WalletHandler) Info(c *fiber.Ctx) error {
	var err error
	var walletId uint
	if walletId, err = GetWalletId(c); err != nil {
		return err
	}

	var wallet model.Wallet
	if err = h.DB.Table(model.WalletTable).First(&wallet, "`id` = ?", walletId).Error; err != nil {
		return errors.New("钱包不存在")
	}

	var walletPoints []model.WalletPoint
	if err = h.DB.Table(model.WalletPointTable).Find(&walletPoints, "`address` = ?", wallet.Address).Error; err != nil {
		return errors.New("获取资产信息错误")
	}

	walletPointMap := make(map[string]float64)
	for _, wp := range walletPoints {
		if _, ok := walletPointMap[wp.Symbol]; !ok {
			walletPointMap[wp.Symbol] = utils.FixedFloat2(float64(wp.Amount) / config.SymbolDictionary[wp.Symbol])
		}
	}

	var tUsdt float64
	var as []resp.Asset
	for symbol, _ := range config.SymbolDictionary {
		if symbol == "" || symbol == enum.PCB {
			continue
		}
		price := impl.GetKtoSymbolPrice(symbol)
		amount := walletPointMap[symbol]
		icon := config.SymbolIconDictionary[symbol]

		tUsdt += amount * price
		if symbol == enum.USDT {
			as = append(as, resp.Asset{
				Address: wallet.TronAddress,
				Symbol:  symbol,
				Price:   utils.FixedFloat4(price),
				Icon:    icon,
				Amount:  utils.FixedFloat3(amount),
			})
		} else {
			as = append(as, resp.Asset{
				Address: wallet.Address,
				Symbol:  symbol,
				Price:   utils.FixedFloat4(price),
				Icon:    icon,
				Amount:  utils.FixedFloat3(amount),
			})
		}
	}

	for i := 0; i < len(as); i++ {
		for j := i + 1; j < len(as); j++ {
			if as[i].Symbol < as[j].Symbol {
				tmp := as[i]
				as[i] = as[j]
				as[j] = tmp
			}
		}
	}

	return c.Send(utils.JsonOk(resp.Wallet{
		Name:        wallet.Name,
		USDT:        utils.FixedFloat3(tUsdt),
		Address:     wallet.Address,
		UsdtAddress: wallet.TronAddress,
		AssetList:   as,
	}))
}

func (h *WalletHandler) Withdraw(c *fiber.Ctx) error {

	var err error
	var walletId uint
	if walletId, err = GetWalletId(c); err != nil {
		return err
	}

	var wallet model.Wallet
	if err = h.DB.Table(model.WalletTable).First(&wallet, "`id` = ?", walletId).Error; err != nil {
		return errors.New("钱包不存在")
	}

	var decryptData []byte
	if decryptData, err = DecryptData(c); err != nil {
		return c.Send(utils.JsonFail("decryptData error"))
	}
	var request req.Withdraw
	if err = json.Unmarshal(decryptData, &request); err != nil {
		return errors.New("参数错误")
	}

	to := request.Address
	symbol := request.Symbol
	amount := request.Amount
	pwd := request.Pwd

	log.Infof("钱包: %v 正在提币: symbol %v amount %v ..", wallet.ID, symbol, amount)

	// ⚠ 2026-09-30 owner 裁定：**提现无上限**（单笔不封顶、单日不限、不设笔数限制），只有"最低起提 100"。
	//   因此这里**删掉了历史上的 10000 硬编码兜底**（`legacyWithdrawHardCap`）——
	//   它会在"未配置上限（0 = 不限）"时把单笔卡在 10000，与裁定直接冲突。
	//   现在的口径：单笔/单日上限一律取 impl.WithdrawSingleMax / WithdrawDailyMax / WithdrawDailyCountMax，
	//   这三个值默认 0 = 不限；要设限制由运维经 /ops/params 下发（>0 设定 / -1 取消）。

	if !utils.ComparePasswords(wallet.Password, []byte(pwd)) {
		return errors.New("密码错误")
	}

	//可配置提现额度（单笔 / 单日累计 / 单日笔数，0 = 不限；见 core/impl/config.go 与 /ops/params）。
	//⚠ 这条原生路径原先完全没有这套检查——只有上面那个硬编码的"单次≤10000"和下面的 3 次/24h 计数，
	//   导致「提现额度」风控只在 /api/withdraw（适配层）生效，原生接口是敞开的（2026-09-22 补齐）。
	//   本路径写出的提币流水同样是 from=本人地址 + type=BlockOut，两边的单日统计口径一致。
	if msg := CheckWithdrawLimits(wallet.Address, amount); msg != "" {
		return errors.New(msg)
	}

	var balancePoint model.WalletPoint
	if err = h.DB.Table(model.WalletPointTable).First(&balancePoint, "address = ? and symbol = ?", wallet.Address, symbol).Error; err != nil {
		return errors.New("余额不够")
	}

	var feeBalance model.WalletPoint
	if err = h.DB.Table(model.WalletPointTable).First(&feeBalance, "address = ? and symbol = ?", wallet.Address, impl.FeeSymbol).Error; err != nil {
		return errors.New(fmt.Sprintf("需要手续费%s", impl.FeeSymbol))
	}

	//余额判断
	balance := balancePoint.Amount
	withdrawAmount := uint64(amount * config.SymbolDictionary[symbol])
	if balance < withdrawAmount {
		return errors.New("余额不足")
	}

	//2.检测手续费（手续费币种可配置：impl.FeeSymbol，默认 BOFI，见 config/params.go）
	//复盘 Wave-3：币价缺失时显式报错，避免除以 0 得 +Inf 误拒、或 0 元费率静默漏收
	feeSymPrice := impl.GetKtoSymbolPrice(impl.FeeSymbol)
	if feeSymPrice <= 0 {
		return errors.New(fmt.Sprintf("手续费币种 %s 暂无报价，请稍后再试", impl.FeeSymbol))
	}
	var feeAmount float64
	switch symbol {
	case enum.USDT:
		withdrawUsdt := amount * impl.GetCny()
		withdrawUsdt = withdrawUsdt * impl.FeeRate //费率
		feeAmount = withdrawUsdt / feeSymPrice

	default:
		symbolPrice := impl.GetKtoSymbolPrice(symbol)
		if symbolPrice <= 0 {
			return errors.New(fmt.Sprintf("%s 暂无报价，请稍后再试", symbol))
		}
		withdrawUsdt := amount * symbolPrice
		withdrawUsdt = withdrawUsdt * impl.FeeRate
		feeAmount = withdrawUsdt / feeSymPrice
	}
	fee := feeBalance.Amount
	feeWithDecimal := uint64(feeAmount * config.SymbolDictionary[impl.FeeSymbol])
	if fee < feeWithDecimal {
		return errors.New(fmt.Sprintf("手续费%s不足: %v", impl.FeeSymbol, utils.FixedFloat2(feeAmount)))
	}

	// ⚠ 2026-09-30 owner 裁定：**不设笔数限制**。原先这里用 Redis 计数硬卡"最多 3 次/24h"
	//   （`maxWithdrawCount = 3`），与"单日不限制"冲突，已移除。
	//   保留 **按地址的并发/防连点**语义：仅做一次极短窗口（10 秒）的重复提交去抖，
	//   不影响"一天提多少次"；真要限次由运维经 /ops/params 下发 WithdrawDailyCountMax（0 = 不限）。
	debounceKey := "n2_withdraw_debounce_" + strconv.FormatUint(uint64(walletId), 10) + symbol
	if ok, derr := config.RedisClient.SetNX(c.Context(), debounceKey, 1, 10*time.Second).Result(); derr == nil && !ok {
		return errors.New("操作过于频繁，请 10 秒后再试")
	}

	err = h.DB.Transaction(func(tx *gorm.DB) error {
		//事务内复核日累计/日笔数（并发/重放无法基于同一旧快照绕过；阈值 0 = 不限制时直接返回空串）
		if msg := CheckWithdrawLimitsTx(tx, wallet.Address, amount); msg != "" {
			return errors.New(msg)
		}
		//复盘 Wave-3：扣减改"带 amount>=? 守卫的原子减法"（快照被并发扣走/不足时 RowsAffected=0，
		//整笔回滚不超扣）；gorm 行锁下并发请求串行化后再评估条件。
		resFee := tx.Table(model.WalletPointTable).Where("`id` = ? and `amount` >= ?", feeBalance.ID, feeWithDecimal).
			Update("amount", gorm.Expr("`amount` - ?", feeWithDecimal))
		if resFee.Error != nil {
			return errors.New("提现错误")
		}
		if resFee.RowsAffected == 0 {
			return errors.New("手续费余额不足")
		}

		//N次方：手续费记账后同步写入 fee_burn 待销毁（status=0），
		//由 cmd/burn 定时程序从资金池链上转黑洞地址销毁，直至流通量仅剩 BurnTarget 枚（通缩设计，见技术方案 1.7/3.3）。
		feeBurn := new(model.FeeBurn)
		feeBurn.Symbol = impl.FeeSymbol
		feeBurn.Amount = feeWithDecimal
		feeBurn.Status = 0
		if err = tx.Table(model.FeeBurnTable).Create(feeBurn).Error; err != nil {
			return errors.New("记录手续费销毁失败")
		}

		resBal := tx.Table(model.WalletPointTable).Where("`id` = ? and `amount` >= ?", balancePoint.ID, withdrawAmount).
			Update("amount", gorm.Expr("`amount` - ?", withdrawAmount))
		if resBal.Error != nil {
			return errors.New("提现错误")
		}
		if resBal.RowsAffected == 0 {
			return errors.New("余额不足")
		}

		var hash string
		switch symbol {
		case enum.USDT:
			//tron 提币
			hash, err = tron.TransferUsdt(config.TronPoolPrivateKey(), to, withdrawAmount)
			if err != nil {
				log.Errorf("%v 上链失败: %v \n", symbol, err)
				return errors.New("上链失败")
			}

		default:
			//kto 提币
			_, hash, err = kto.KTOonChainSync(symbol, config.EtcConfig.KtoPool.Address, config.KtoPoolPrivateKey(), to, withdrawAmount)
			if err != nil {
				log.Errorf("%v 上链失败: %v \n", symbol, err)
				return errors.New("上链失败")
			}
		}

		wtxFee := new(model.WalletTx)
		wtxFee.Symbol = impl.FeeSymbol
		wtxFee.To = to
		wtxFee.From = wallet.Address
		wtxFee.Amount = feeWithDecimal
		wtxFee.Desc = enum.FeeText
		wtxFee.Type = enum.Fee
		wtxFee.Hash = hash
		// 注（2026-09-30 清理）：原此处有 `if err != nil { wtxFee.Success = 1 }`，
		// 但上面 switch 的每个分支在出错时都已 return，故该判断恒为 false（死分支）。已删除。

		wtx := new(model.WalletTx)
		wtx.Symbol = symbol
		wtx.To = to
		wtx.From = wallet.Address
		wtx.Amount = withdrawAmount
		wtx.Desc = enum.BlockOutText
		wtx.Type = enum.BlockOut //修复存量笔误：原 wtxFee.Type = enum.BlockOut 覆盖了手续费类型，此处为提币流水类型
		wtx.Hash = hash
		// 同上：原 `if err != nil { wtx.Success = 1 }` 为恒假死分支，已删除。
		if err = tx.Table(model.WalletTxTable).Create(wtxFee).Error; err != nil {
			log.Errorln("创建交易费转出记录失败", err, wtxFee)
			return nil
		}

		if err = tx.Table(model.WalletTxTable).Create(wtx).Error; err != nil {
			log.Errorln("创建转出记录失败", err, wtx)
			return nil
		}
		return nil
	})

	if err != nil {
		return c.Send(utils.JsonFail("上链失败"))
	}

	return c.Send(utils.JsonOk("正在上链"))
}

func (h *WalletHandler) Fee(c *fiber.Ctx) error {
	amount := c.Params("amount")
	symbol := c.Params("symbol")

	amountF, err := strconv.ParseFloat(amount, 64)
	if err != nil {
		return errors.New("参数数量错误")
	}

	//复盘 Wave-3：币价缺失显式报错，避免 0 价除零
	feeSymPrice := impl.GetKtoSymbolPrice(impl.FeeSymbol)
	if feeSymPrice <= 0 {
		return errors.New(fmt.Sprintf("手续费币种 %s 暂无报价，请稍后再试", impl.FeeSymbol))
	}
	var feeAmount float64
	switch symbol {
	case enum.USDT:
		withdrawUsdt := amountF * impl.GetCny()
		withdrawUsdt = withdrawUsdt * impl.FeeRate
		feeAmount = withdrawUsdt / feeSymPrice

	default:
		symbolPrice := impl.GetKtoSymbolPrice(symbol)
		if symbolPrice <= 0 {
			return errors.New(fmt.Sprintf("%s 暂无报价，请稍后再试", symbol))
		}
		withdrawUsdt := amountF * symbolPrice
		withdrawUsdt = withdrawUsdt * impl.FeeRate
		feeAmount = withdrawUsdt / feeSymPrice
	}

	return c.Send(utils.JsonOk(feeAmount))
}
