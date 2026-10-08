package impl

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/model"
	"errors"
	"github.com/panjf2000/ants/v2"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"math"
	"strconv"
	"time"
)

type RoundManager struct {
	DB            *gorm.DB
	RewardManager core.Rewarding
	WalletManager core.OnBoarding
	WalletPointSafe core.WalletPointing
}

func (r RoundManager) Active(round model.ProjectRound) error {
	var voteList []model.Vote
	if err := r.DB.Table(model.VoteTable).Find(&voteList, "`round_id` = ?", round.ID).Error; err != nil {
		log.WithFields(log.Fields{"roundId": round.ID, "Method": "Success"}).Infoln("获取投资用户信息失败", err)
		return err
	}

	for _, vote := range voteList {
		if err := r.DB.Model(model.Wallet{}).Where("`id` = ?", vote.WalletId).Update("active", true).Error; err != nil {
			return err
		}
	}

	return nil
}

func (r RoundManager) Init(projectId uint, target, min, max float64, startTime, endTime time.Time) (round model.ProjectRound, err error) {
	//获取项目
	var project model.Project
	err = r.DB.Table(model.ProjectTable).First(&project, "`id` = ? and `status` = 1", projectId).Error
	switch err {
	case gorm.ErrRecordNotFound:
		err = errors.New("项目不存在")
		return
	}

	//检测项目是否进行中轮
	err = r.DB.Table(model.ProjectRoundTable).Where("`project_id` = ? and `status` IN ? and `current_vote` <> `target_vote`", projectId, []int{enum.ProjectStarting}).First(&round).Error
	switch err {
	case nil:
		err = errors.New("已存在未投满的进行中轮")
		return
	}

	var latestRound model.ProjectRound
	err = r.DB.Table(model.ProjectRoundTable).Order("created_at DESC").Limit(1).Where("`project_id` = ? ", projectId).Find(&latestRound).Error
	if err != nil {
		err = errors.New("创建错误")
		return
	}
	if latestRound.ID == 0 {
		if target == 0 {
			err = errors.New("请初始化第一轮目标数量")
			return
		}
	}

	if target == 0 {
		target = math.Round(latestRound.TargetVote + (latestRound.TargetVote * 0.3))
	}


	if target < 0 || target < min || target < max {
		err = errors.New("筹集目标数量错误")
		return
	}

	if startTime.Before(time.Now()) {
		err = errors.New("开始时间不能小于当前时间")
		return
	}

	if startTime.After(endTime) {
		err = errors.New("开始时间不能大于结束时间")
		return
	}

	if min > max {
		err = errors.New("最大众筹数量不能小于最小值")
		return
	}

	var maxRound uint
	err = r.DB.Table(model.ProjectRoundTable).Where("`project_id` = ? ", projectId).Pluck("COALESCE(MAX(`round`), 0) as maxRound", &maxRound).Error
	switch err {
	case gorm.ErrRecordNotFound:
		err = errors.New("项目不存在")
		return
	}

	//新开始一轮
	round.ProjectId = project.ID
	round.Period = project.Period
	round.Round = maxRound + 1
	round.TargetVote = target
	round.MinVote = min
	round.MaxVote = max
	round.StartTime = startTime
	round.EndTime = endTime
	round.TimeLimit = endTime.Sub(startTime).Seconds()
	round.Symbol = project.Symbol
	round.Status = enum.RoundWaiting
	err = r.DB.Table(model.ProjectRoundTable).Create(&round).Error
	return
}

func (r RoundManager) Begin(round model.ProjectRound) error {
	if round.Status != enum.RoundWaiting {
		return errors.New("项目已开始")
	}

	return r.DB.Model(&round).Update("`status`", enum.RoundStarting).Error
}

func (r RoundManager) End(round model.ProjectRound) error {
	log.Infoln("正在结束轮: ", round.ID, ".....")

	//不计状态 100% 退还超级账号投资额度
	adminVotes := r.RewardManager.GetAdminVotes(round)
	for _, adminVote := range adminVotes {
		amount := adminVote.Amount
		symbol := adminVote.Symbol

		//新增到钱包余额
		var walletPoint = r.WalletPointSafe.GetOrInitWalletPoint(r.DB, adminVote.Address, symbol)
		err := r.DB.Transaction(func(tx *gorm.DB) error {
			withdrawAmount, err := r.WalletPointSafe.AddPointAmount(tx, adminVote.Address, adminVote.Symbol, amount)
			if err != nil {
				return err
			}

			//创建退还记录
			wtx := new(model.WalletTx)
			wtx.Symbol = adminVote.Symbol
			wtx.Amount = withdrawAmount
			wtx.From = RewardAddress
			wtx.To = walletPoint.Address
			wtx.Type = enum.SuperAdmin100Withdraw
			wtx.Desc = "[" + "第" + strconv.Itoa(int(round.Period)) + "期" + "第" + strconv.Itoa(int(round.Round)) + "轮" + "]" + enum.SuperAdmin100WithdrawText
			if err = tx.Table(model.WalletTxTable).Create(wtx).Error; err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			log.Errorln("返还超级账号本金失败")
		}
	}

	//众筹是否达标
	var err error
	var projectId = round.ProjectId
	var success = round.CurrentVote >= round.TargetVote
	var currentRound = round.Round

	//更新当前轮状态
	round.Success = success
	round.Status = enum.RoundCompute
	if err = r.DB.Model(&round).Updates(&round).Error; err != nil {
		return err
	}

	//达标 判断第几轮结算收益
	//第3轮 -> 第1轮
	//第4轮 -> 第2轮
	if success {

		//1.获取参与项目的所有钱包地址
		var voteList = r.RewardManager.GetNoAdminVotes(round)
		for _, vote := range voteList {
			err = r.WalletManager.Promote(vote.WalletId, projectId)
			if err != nil {
				log.WithFields(log.Fields{"roundId": round.ID, "Method": "Promote"}).Infoln("提升等级错误", err)
			}
		}

		err = r.Active(round)
		if err != nil {
			return err
		}

		if currentRound <= MinRound {

		}else{
			rewardRoundNum := currentRound - MinRound
			var rewardRound model.ProjectRound
			if err = r.DB.Table(model.ProjectRoundTable).Where("`round` = ? and `project_id` = ? and `status` = ?", rewardRoundNum, projectId, enum.RoundCompute).First(&rewardRound).Error; err != nil {
				return err
			}

			err = ants.Submit(func() {
				r.RewardManager.Success(rewardRound)
			})

		}

	} else {

		if currentRound <= MinRound {
			//退轮
			withdrawRoundNums := make([]uint, 0)
			for i := 1; i <= int(currentRound); i++ {
				withdrawRoundNums = append(withdrawRoundNums, uint(i))
			}

			var withdrawRounds []model.ProjectRound
			if err = r.DB.Table(model.ProjectRoundTable).Where("`round` IN ? and `project_id` = ? and `status` = ?", withdrawRoundNums, projectId, enum.RoundCompute).Find(&withdrawRounds).Error; err != nil {
				return err
			}

			for _, round := range withdrawRounds {
				err = ants.Submit(func() {
					r.RewardManager.FailedNormal(round)
				})
			}
		}else{

			//failed withdraw n-1 n-2 rounds
			failedRoundNums := []uint{currentRound - 1, currentRound - 2}
			var withdrawRounds []model.ProjectRound
			if err = r.DB.Table(model.ProjectRoundTable).Where("`round` IN ? and `project_id` = ? and `status` = ?", failedRoundNums, projectId, enum.RoundCompute).Find(&withdrawRounds).Error; err != nil {
				return err
			}
			err = ants.Submit(func() {
				r.RewardManager.Failed(withdrawRounds)
			})
			if err != nil {
				return err
			}
			//100% withdraw current round
			err = ants.Submit(func() {
				r.RewardManager.FailedNormal(round)
			})
			if err != nil {
				return err
			}
		}
	}

	return err
}

func NewRoundManager() core.Rounding {
	return RoundManager{
		DB:            config.MysqlDBPool,
		RewardManager: NewRewardManager(),
		WalletManager: NewWalletManager(),
		WalletPointSafe: NewWalletPointSafe(),
	}
}
