package impl

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/model"
	"errors"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"strconv"
	"sync"
	"time"
)

type VoteManager struct {
	DB            *gorm.DB
	WalletManager core.OnBoarding
	Lock          sync.Mutex
}

func NewVoteManager() core.Voting {
	return &VoteManager{DB: config.MysqlDBPool, WalletManager: NewWalletManager()}
}

// Vote 用户投入众筹（复盘 Wave-2 并发安全改造）
// 要点：
//  1. 本进程内以 v.Lock 串行化，并在锁内【重读】轮与余额（不信任调用方传入的旧快照）；
//  2. 轮进度用加法 UPDATE（current_vote = current_vote + ?），余额用带条件的减法 UPDATE
//     （amount = amount - ? WHERE amount >= ?），跨实例并发也不会丢更新/超扣/超募；
//     若守卫失败（轮被并发结束/已投满/余额不足）则整笔事务回滚并返回明确错误。
func (v *VoteManager) Vote(wallet model.Wallet, round model.ProjectRound, amount float64) (vote model.Vote, err error) {
	v.Lock.Lock()
	defer v.Lock.Unlock()

	if amount <= 0 {
		err = errors.New("投入金额错误")
		return
	}

	var isAdmin = wallet.Admin
	var address = wallet.Address
	var roundId = round.ID
	var projectId = round.ProjectId

	//1. 锁内重读钱包余额（调用方传入的快照可能过期）
	var walletPoint model.WalletPoint
	if err = v.DB.Table(model.WalletPointTable).First(&walletPoint, "`address` = ? and `symbol` = ?", address, round.Symbol).Error; err != nil {
		err = errors.New("钱包余额不足")
		return
	}
	balance := walletPoint.Amount
	symbol := walletPoint.Symbol

	//2. 锁内重读轮次（进行中/未过期/最新进度），避免基于旧 CurrentVote 判断与更新
	var fresh model.ProjectRound
	if err = v.DB.Table(model.ProjectRoundTable).First(&fresh, "`id` = ?", roundId).Error; err != nil {
		err = errors.New("该轮不存在")
		return
	}
	if fresh.Status != enum.RoundStarting {
		err = errors.New("该轮未开始或已结束")
		return
	}
	if time.Now().After(fresh.EndTime) {
		err = errors.New("该轮已结束")
		return
	}

	//N次方：多阶段币种开关校验（普通用户仅限当前阶段允许币种参与，超级账号不受限）
	if !isAdmin && !isStageSymbol(fresh.Symbol) {
		err = errors.New("当前阶段不支持该币种参与")
		return
	}

	//3. 数量与进度校验（主单位；与全库口径一致，见复盘记录）
	amountWithDecimal := uint64(amount * config.SymbolDictionary[symbol])
	if balance < amountWithDecimal {
		err = errors.New("钱包余额不足")
		return
	}
	if !isAdmin {
		if amount < fresh.MinVote {
			err = errors.New("投入金额过小")
			return
		}
		if amount > fresh.MaxVote {
			err = errors.New("投入金额过大")
			return
		}
	}

	//4. 已投满截断：leftVote 用锁内最新进度计算
	if fresh.CurrentVote >= fresh.TargetVote {
		err = errors.New("已投满")
		return
	}
	leftVote := fresh.TargetVote - fresh.CurrentVote
	if amount > leftVote {
		amount = leftVote
		amountWithDecimal = uint64(amount * config.SymbolDictionary[symbol])
	}
	if amount <= 0 || amountWithDecimal <= 0 {
		err = errors.New("已投满")
		return
	}

	//每轮每用户累计投入限额（参数 MaxVoteIsPerUserRound=1 开启；默认关闭=存量单笔口径）
	if EnforceVoteRoundCap && !isAdmin {
		var preVote model.Vote
		usedBefore := 0.0
		if perr := v.DB.Table(model.VoteTable).First(&preVote, "`address` = ? and `round_id` = ?", address, roundId).Error; perr == nil {
			usedBefore = preVote.Amount
		}
		userLeft := fresh.MaxVote - usedBefore
		if userLeft <= 0 {
			err = errors.New("已超出本轮个人投入上限")
			return
		}
		if amount > userLeft {
			amount = userLeft
			amountWithDecimal = uint64(amount * config.SymbolDictionary[symbol])
		}
		if amount <= 0 || amountWithDecimal <= 0 {
			err = errors.New("已超出本轮个人投入上限")
			return
		}
	}

	//5. 事务内执行（守卫式更新兜底并发）
	err = v.DB.Transaction(func(tx *gorm.DB) error {
		//5.1 轮进度：加法 + 状态/上限守卫（跨实例并发安全）
		res := tx.Exec("UPDATE `"+model.ProjectRoundTable+"` SET `current_vote` = `current_vote` + ?, `count` = `count` + 1 "+
			"WHERE `id` = ? AND `status` = ? AND `current_vote` + ? <= `target_vote`",
			amount, roundId, enum.RoundStarting, amount)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errors.New("轮次进度已变化或已投满，请刷新重试")
		}

		//5.2 余额扣减：带 amount >= ? 守卫（跨实例并发不超扣）
		res = tx.Exec("UPDATE `"+model.WalletPointTable+"` SET `amount` = `amount` - ? WHERE `id` = ? AND `amount` >= ?",
			amountWithDecimal, walletPoint.ID, amountWithDecimal)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errors.New("余额不足")
		}

		//5.3 投入记录：已存在则加法累加，否则创建（轮内多次投入、无次数上限）
		var existing model.Vote
		eErr := tx.Table(model.VoteTable).First(&existing, "`address` = ? and `round_id` = ?", address, roundId).Error
		switch eErr {
		case nil:
			//幂等防重放：同一请求重复提交由（address,round）单行累加语义兜底，这里只做加法更新；
			//累计限额开启时追加 amount 总和 ≤ MaxVote 的守卫（并发也不会超用户上限）
			if EnforceVoteRoundCap && !isAdmin {
				res = tx.Exec("UPDATE `"+model.VoteTable+"` SET `amount` = `amount` + ?, `count` = `count` + 1 "+
					"WHERE `id` = ? AND `amount` + ? <= ?", amount, existing.ID, amount, fresh.MaxVote)
			} else {
				res = tx.Exec("UPDATE `"+model.VoteTable+"` SET `amount` = `amount` + ?, `count` = `count` + 1 WHERE `id` = ?",
					amount, existing.ID)
			}
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return errors.New("已超出本轮个人投入上限或记录变化，请刷新重试")
			}
			existing.Amount += amount
			existing.Count += 1
			vote = existing
		case gorm.ErrRecordNotFound:
			vote.IsAdmin = wallet.Admin
			vote.Amount = amount
			vote.Symbol = symbol
			vote.Address = wallet.Address
			vote.WalletId = wallet.ID
			vote.RoundId = roundId
			vote.Round = fresh.Round
			vote.Period = fresh.Period
			vote.ProjectId = projectId
			vote.Level = wallet.Level
			vote.Count = 1
			if err := tx.Table(model.VoteTable).Create(&vote).Error; err != nil {
				return err
			}
		default:
			return eErr
		}

		//5.4 资金流水（最小单位）
		wtx := new(model.WalletTx)
		wtx.Symbol = symbol
		wtx.Amount = amountWithDecimal
		wtx.From = wallet.Address
		wtx.To = VoteAddress
		wtx.Type = enum.Vote
		wtx.Desc = "[" + "第" + strconv.Itoa(int(fresh.Period)) + "期" + "第" + strconv.Itoa(int(fresh.Round)) + "轮" + "]" + enum.VoteText
		if err := tx.Table(model.WalletTxTable).Create(wtx).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		log.WithFields(log.Fields{"walletId": wallet.ID, "roundId": roundId, "amount": amount}).Errorln("投票事务失败", err)
		return vote, err
	}
	return
}
