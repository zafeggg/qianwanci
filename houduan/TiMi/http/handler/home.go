package handler

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core/impl"
	"com.fibonacci.crowd/http/resp"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils"
	"errors"
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
	"sort"
)

type HomeHandler struct {
	DB *gorm.DB
}

func NewHomeHandler() *HomeHandler {
	return &HomeHandler{DB: config.MysqlDBPool}
}

//Home 首页项目信息
func (h HomeHandler) Home(c *fiber.Ctx) error {
	var err error
	if _, err = GetWalletId(c); err != nil {
		return err
	}

	var projectList []model.Project
	var roundList []model.ProjectRound
	var notifyList []model.Notify
	if err = h.DB.Table(model.ProjectTable).Order("`created_at` DESC").Find(&projectList).Error; err != nil {
		return errors.New("查询项目失败")
	}

	if err = h.DB.Limit(10).Order("`start_time` DESC").Table(model.ProjectRoundTable).Find(&roundList).Error; err != nil {
		return errors.New("查询轮失败")
	}

	if err = h.DB.Table(model.NotifyTable).Find(&notifyList, "`type` = ?", 0).Error; err != nil {
		return errors.New("查询通知失败")
	}

	pjs := make([]resp.Project, 0)
	rds := make([]resp.Round, 0)
	nts := make([]resp.Notify, 0)
	for _, project := range projectList {
		pjs = append(pjs, resp.Project{
			Id:     project.ID,
			Period: project.Period,
			Symbol: project.Symbol,
			Status: project.Status,
		})
	}

	for _, round := range roundList {
		rds = append(rds, resp.Round{
			ProjectId:   round.ProjectId,
			Id:          round.ID,
			Period:      round.Period,
			Round:       round.Round,
			TargetVote:  round.TargetVote,
			MinVote:     round.MinVote,
			MaxVote:     round.MaxVote,
			CurrentVote: round.CurrentVote,
			StartTime:   round.StartTime,
			EndTime:     round.EndTime,
			Status:      round.Status,
			RewardRate:  impl.StaticRewardRate,
			Symbol:      round.Symbol,
		})
	}

	//sort by status
	sort.SliceStable(rds, func(i, j int) bool {
		var iStatus int64
		var jStatus int64
		switch rds[i].Status {
		case 0:
			iStatus = 8
		case 1:
			iStatus = 10
		case 2:
			iStatus = 5
		case 3:
			iStatus = 1
		}
		switch rds[j].Status {
		case 0:
			jStatus = 8
		case 1:
			jStatus = 10
		case 2:
			jStatus = 5
		case 3:
			jStatus = 1
		}

		if iStatus > jStatus {
			return true
		}

		if iStatus == jStatus {
			if rds[i].StartTime.Before(rds[j].StartTime) {
				return true
			} else {
				return false
			}
		}

		return false
	})

	for _, notify := range notifyList {
		nts = append(nts, resp.Notify{
			Id:      notify.ID,
			Content: notify.Content,
		})
	}
	return c.Send(utils.JsonOk(resp.Home{
		ProjectList: pjs,
		RoundList:   rds,
		NotifyList:  nts,
	}))
}

func (h HomeHandler) ProjectDetail(c *fiber.Ctx) error {
	var err error
	var walletId uint
	if walletId, err = GetWalletId(c); err != nil {
		return err
	}
	var wallet model.Wallet
	if err = h.DB.Table(model.WalletTable).First(&wallet, "`id` = ?", walletId).Error; err != nil {
		return errors.New("钱包不存在")
	}

	projectId, err := c.ParamsInt("projectId")
	if err != nil {
		return errors.New("参数错误")
	}

	var project model.Project
	var voteList []model.Vote
	var rewardList []model.VoteReward
	var roundList []model.ProjectRound
	if err = h.DB.Table(model.ProjectTable).First(&project, "`id` = ?", projectId).Error; err != nil {
		return errors.New("项目不存在")
	}
	if err = h.DB.Table(model.VoteTable).Find(&voteList, "`project_id` = ? and `wallet_id` = ?", project.ID, walletId).Error; err != nil {
		return errors.New("查询投资信息失败")
	}
	if err = h.DB.Table(model.VoteRewardTable).Find(&rewardList, "`project_id` = ? and `address` = ? and `loss` <= 0", projectId, wallet.Address).Error; err != nil {
		return errors.New("查询投资奖励失败")
	}
	if err = h.DB.Table(model.ProjectRoundTable).Order("`created_at` DESC").Find(&roundList, "`project_id` = ?", projectId).Error; err != nil {
		return errors.New("查询投资轮失败")
	}

	var votes float64
	var rewards float64
	for _, vote := range voteList {
		votes += vote.Amount
	}
	for _, reward := range rewardList {
		rewards += reward.Static + reward.DynamicTeam + reward.DynamicShard
	}

	rounds := make([]resp.Round, 0)
	for _, round := range roundList {
		rounds = append(rounds, resp.Round{
			Id:          round.ID,
			ProjectId:   round.ProjectId,
			Period:      round.Period,
			Round:       round.Round,
			TargetVote:  round.TargetVote,
			MinVote:     round.MinVote,
			MaxVote:     round.MaxVote,
			CurrentVote: round.CurrentVote,
			StartTime:   round.StartTime,
			EndTime:     round.EndTime,
			Status:      round.Status,
			Symbol:      round.Symbol,
			RewardRate:  impl.StaticRewardRate,
		})
	}
	return c.Send(utils.JsonOk(resp.ProjectDetail{
		Vote:      utils.FixedFloat2(votes),
		Reward:    utils.FixedFloat2(rewards),
		RoundList: rounds,
	}))
}

func (h HomeHandler) RoundDetail(c *fiber.Ctx) error {
	var err error
	var walletId uint
	if walletId, err = GetWalletId(c); err != nil {
		return err
	}

	var roundId int
	if roundId, err = c.ParamsInt("roundId"); err != nil {
		return errors.New("参数错误")
	}

	var wallet model.Wallet
	var round model.ProjectRound
	if err = h.DB.Table(model.WalletTable).First(&wallet, "`id` = ? ", walletId).Error; err != nil {
		return errors.New("钱包不存在")
	}

	if err = h.DB.Table(model.ProjectRoundTable).First(&round, "`id` = ?", roundId).Error; err != nil {
		return errors.New("该轮不存在")
	}

	var vote model.Vote
	var walletPoint model.WalletPoint
	var symbol = round.Symbol
	var address = wallet.Address
	if err = h.DB.Table(model.WalletPointTable).Find(&walletPoint, "`address` = ? and `symbol` = ?", address, symbol).Limit(1).Error; err != nil {
		return errors.New("获取余额信息失败")
	}

	if err = h.DB.Table(model.VoteTable).Find(&vote, "`wallet_id` = ? and `round_id` = ?", wallet.ID, round.ID).Limit(1).Error; err != nil {
		return errors.New("获取投资信息失败")
	}

	balance := utils.FixedFloat2(float64(walletPoint.Amount) / config.SymbolDictionary[walletPoint.Symbol])
	resps := resp.RoundDetail{
		Balance:     balance,
		Status:      round.Status,
		Vote:        vote.Amount,
		CurrentVote: round.CurrentVote,
		TargetVote:  round.TargetVote,
		Symbol:      round.Symbol,
		Icon:        config.SymbolIconDictionary[symbol],
		Usdt:        utils.FixedFloat2(balance * impl.GetKtoSymbolPrice(symbol)),
	}

	return c.Send(utils.JsonOk(resps))
}

func (h HomeHandler) VoteList(c *fiber.Ctx) error {
	var err error
	var walletId uint
	if walletId, err = GetWalletId(c); err != nil {
		return err
	}

	var voteList []model.Vote
	if err = h.DB.Table(model.VoteTable).Order("`created_at` DESC").Find(&voteList, "`wallet_id` = ?", walletId).Error; err != nil {
		return errors.New("获取投资列表失败")
	}

	var vs []resp.VoteRecord
	for _, vote := range voteList {
		vs = append(vs, resp.VoteRecord{
			Id:         vote.ID,
			Period:     vote.ProjectId,
			Round:      vote.Round,
			Amount:     vote.Amount,
			CreateTime: vote.CreatedAt,
			Symbol:     vote.Symbol,
		})
	}

	return c.Send(utils.JsonOk(vs))
}

func (h HomeHandler) Message(c *fiber.Ctx) error {

	var err error
	var notifyList []model.Notify
	if err = h.DB.Table(model.NotifyTable).Find(&notifyList, "type = ?", 1).Error; err != nil {
		return errors.New("查询通知失败")
	}

	nts := make([]resp.Notify, 0)
	for _, notify := range notifyList {
		nts = append(nts, resp.Notify{
			Id:         notify.ID,
			Content:    notify.Content,
			CreateTime: notify.CreatedAt,
		})
	}

	return c.Send(utils.JsonOk(nts))
}
