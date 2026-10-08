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

func (v *VoteManager) Vote(wallet model.Wallet, round model.ProjectRound, amount float64) (vote model.Vote, err error) {
	v.Lock.Lock()
	defer v.Lock.Unlock()

	log.WithFields(log.Fields{"walletId": wallet.ID, "roundId": round.ID, "amount": amount}).Infoln("wallet vote round")
	//1.check vote amount
	var isAdmin = wallet.Admin
	var address = wallet.Address
	var symbol = round.Symbol

	//N次方：多阶段币种开关校验（技术方案 1.8）—— 普通用户仅限当前阶段允许币种参与，超级账号不受限
	if !isAdmin && !isStageSymbol(symbol) {
		err = errors.New("当前阶段不支持该币种参与")
		return
	}

	var roundId = round.ID
	var projectId = round.ProjectId

	var walletPoint model.WalletPoint
	if err = v.DB.Table(model.WalletPointTable).Find(&walletPoint, "address = ? and symbol = ?", address, symbol).Limit(1).Error; err != nil {
		return
	}

	balance := walletPoint.Amount
	amountWithDecimal := uint64(amount * config.SymbolDictionary[walletPoint.Symbol])
	log.Infoln("balance", balance, "amountWithDecimal", amountWithDecimal)

	if round.Status != enum.RoundStarting {
		err = errors.New("该轮未开始")
		return
	}

	if time.Now().After(round.EndTime) {
		err = errors.New("该轮已结束")
		return
	}

	if balance < amountWithDecimal {
		err = errors.New("钱包余额不足")
		return
	}

	if !isAdmin {
		if amount < round.MinVote {
			err = errors.New("投入金额过小")
			return
		}
		if amount > round.MaxVote {
			err = errors.New("投入金额过大")
			return
		}
	}

	//2.1 check if round left amount smaller than vote amount
	currentVote := round.CurrentVote
	targetVote := round.TargetVote
	if currentVote >= targetVote {
		return vote, errors.New("已投满")
	}else {
		//用户还可以投多少 限制投满
		leftVote := targetVote - currentVote
		if amount > leftVote {
			amount = leftVote
			amountWithDecimal = uint64(amount * config.SymbolDictionary[walletPoint.Symbol])
		}
	}

	err = v.DB.Table(model.VoteTable).First(&vote, "`address` = ? and `round_id` = ?", address, roundId).Error
	//if vote.Count > MaxVoteCount {
	//	err = errors.New(fmt.Sprintf("已大于最大投入次数:%d", MaxVoteCount))
	//	return
	//}

	switch err {
	case nil:
		vote.Amount += amount
		vote.Level = wallet.Level
		vote.Count += 1

		//3. create vote and update round progress
		err = v.DB.Transaction(func(tx *gorm.DB) error {
			if err = tx.Table(model.VoteTable).Updates(&vote).Error; err != nil {
				return err
			}

			//4. ? maybe lock this project round protect out of target vote
			round.CurrentVote += amount
			round.Count += 1
			if err = tx.Table(model.ProjectRoundTable).Select("current_vote", "count").Updates(&round).Error; err != nil {
				return err
			}

			walletPoint.Amount -= amountWithDecimal
			if err = tx.Table(model.WalletPointTable).Select("amount").Updates(&walletPoint).Error; err != nil {
				return err
			}

			//4.记录wallet_tx
			wtx := new(model.WalletTx)
			wtx.Symbol = symbol
			wtx.Amount = amountWithDecimal
			wtx.From = wallet.Address
			wtx.To = VoteAddress
			wtx.Type = enum.Vote
			wtx.Desc = "[" + "第" + strconv.Itoa(int(round.Period)) + "期" + "第" + strconv.Itoa(int(round.Round)) + "轮" + "]" + enum.VoteText
			if err = tx.Table(model.WalletTxTable).Create(wtx).Error; err != nil {
				return err
			}
			return nil
		})
	case gorm.ErrRecordNotFound:
		vote.IsAdmin = wallet.Admin
		vote.Amount = amount
		vote.Symbol = symbol
		vote.Address = wallet.Address
		vote.WalletId = wallet.ID
		vote.RoundId = roundId
		vote.Round = round.Round
		vote.Period = round.Period
		vote.ProjectId = projectId
		vote.Level = wallet.Level
		vote.Count = 1

		//3. create vote and update round progress
		err = v.DB.Transaction(func(tx *gorm.DB) error {
			if err = tx.Table(model.VoteTable).Create(&vote).Error; err != nil {
				return err
			}

			//4. ? maybe lock this project round protect out of target vote
			round.CurrentVote += amount
			round.Count += 1
			if err = tx.Table(model.ProjectRoundTable).Select("current_vote", "count").Updates(&round).Error; err != nil {
				return err
			}

			walletPoint.Amount -= amountWithDecimal
			if err = tx.Table(model.WalletPointTable).Select("amount").Updates(&walletPoint).Error; err != nil {
				return err
			}

			//4.记录wallet_tx
			wtx := new(model.WalletTx)
			wtx.Symbol = symbol
			wtx.Amount = amountWithDecimal
			wtx.From = wallet.Address
			wtx.To = VoteAddress
			wtx.Type = enum.Vote
			wtx.Desc = "[" + "第" + strconv.Itoa(int(round.Period)) + "期" + "第" + strconv.Itoa(int(round.Round)) + "轮" + "]" + enum.VoteText
			if err = tx.Table(model.WalletTxTable).Create(wtx).Error; err != nil {
				return err
			}
			return nil
		})
	default:
	}
	return
}
