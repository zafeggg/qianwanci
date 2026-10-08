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

// nextMaxVote 计算下一轮的最高投入限额（TiMi需求#2：最高限额每轮增加一点点）。
// 纯函数便于单测；rate = RoundMaxVoteGrowRate（默认 0.1），并保证严格递增：
// 基数极小时（如 max=1）浮点乘法可能不增长，此时至少 +1，否则「每轮增加」的需求会失效。
func nextMaxVote(cur float64) float64 {
	if cur <= 0 {
		return cur
	}
	next := cur * (1 + RoundMaxVoteGrowRate)
	if next <= cur {
		next = cur + 1
	}
	return next
}

// AutoCreateNextRound 成功轮结束后自动创建下一轮（复盘 Wave-4：补齐"进入下一次循环"的自动化）。
//
// 口径（TiMi需求#1/#2 + N次方介绍）：
//   - target  = 当前目标 × 1.3（每轮定增 30%，固定）
//   - min     **不变**（「最低限额不会更改」）
//   - max     每轮「增加一点点」：next = 当前 × (1 + RoundMaxVoteGrowRate)，默认 10%
//             （N次方举例 10-100 → 10-110 → 10-130 → 10-150，增量为正；原实现直接沿用当前 max，
//              与需求不符——这是本轮补的实现）
//   - 时限沿用当前轮（run 时的动态缩短见 cmd/round#adjustRoundTimeLimit）
//
// 幂等：若该期已存在更高轮号（等待/进行中）则不重复创建。
func (r RoundManager) AutoCreateNextRound(round model.ProjectRound) error {
	var higher int64
	if err := r.DB.Table(model.ProjectRoundTable).Where("`project_id` = ? and `round` > ?", round.ProjectId, round.Round).Count(&higher).Error; err != nil {
		return err
	}
	if higher > 0 {
		return nil
	}
	target := round.TargetVote * 1.3
	if target <= 0 {
		return errors.New("目标额度无效，无法自动创建下一轮")
	}
	timeLimit := round.TimeLimit
	if timeLimit <= 0 {
		timeLimit = 3600 //1 小时兜底
	}

	//最高限额递增：rate 缺省 0.1；再兜一层「至少 +1」防止基数极小时浮点乘法不增长
	maxVote := nextMaxVote(round.MaxVote)

	now := time.Now()
	next := model.ProjectRound{
		ProjectId:   round.ProjectId,
		Period:      round.Period,
		Round:       round.Round + 1,
		TimeLimit:   timeLimit,
		TargetVote:  target,
		MinVote:     round.MinVote, //最低限额不变
		MaxVote:     maxVote,
		CurrentVote: 0,
		StartTime:   now,
		EndTime:     now.Add(time.Duration(timeLimit) * time.Second),
		Count:       0,
		Symbol:      round.Symbol,
		Success:     false,
		Status:      enum.RoundWaiting,
	}
	if err := r.DB.Table(model.ProjectRoundTable).Create(&next).Error; err != nil {
		return err
	}
	log.WithFields(log.Fields{
		"projectId": round.ProjectId, "nextRound": next.Round,
		"target": target, "min": next.MinVote, "max": next.MaxVote,
	}).Infoln("自动创建下一轮成功（目标×1.3、最高限额递增、最低限额不变）")
	return nil
}

func (r RoundManager) End(round model.ProjectRound) error {
	log.Infoln("正在结束轮: ", round.ID, ".....")

	//复盘 Wave-2：End 幂等抢占——仅当轮仍为"进行中(RoundStarting)"才进入结算流程，
	//防止定时器/重复调用对同一轮执行两次（尤其避免超级账号本金被重复退还）。
	//事务失败回滚后状态恢复 Starting 仍可重试；若在占用后中途失败会停在 RoundCompute，需人工核查。
	resClaim := r.DB.Table(model.ProjectRoundTable).Where("`id` = ? and `status` = ?", round.ID, enum.RoundStarting).
		Update("status", enum.RoundCompute)
	if resClaim.Error != nil {
		return resClaim.Error
	}
	if resClaim.RowsAffected == 0 {
		log.WithFields(log.Fields{"roundId": round.ID}).Infoln("轮已非进行中，跳过 End")
		return nil
	}

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
			//三进一出：投第 N 轮在第 N+MinRound 轮 End 时结算
			if err = r.settleDueRound(projectId, currentRound); err != nil {
				return err
			}
		}

		//复盘 Wave-4：成功轮后自动创建下一轮（target ×1.3；失败轮已走项目结束不再开）
		if err = r.AutoCreateNextRound(round); err != nil {
			log.WithFields(log.Fields{"roundId": round.ID}).Errorln("自动创建下一轮失败", err)
		}

	} else {

		//复盘（按 TiMi需求#5/#6 字面口径）：失败=当前轮(倒1) 100% 全额退回；
		//倒2/倒3=其前一轮/前二轮（存在且处于待结算 RoundCompute）扣 50% 折算算力。
		//即第 2 轮失败也对第 1 轮按倒2 扣半；第 1 轮失败（无前序轮）仅全退当前轮。
		//
		//⚠ 2026-09-30 修复「结算断口」：失败分支原先只处理 F-1/F-2/F，**漏掉了三进一出
		//到期的那一轮 F-MinRound**。第 N 轮应在第 N+MinRound 轮结束时结算（无论该轮成败），
		//而 F 失败时 F-MinRound 恰好到期 → 该轮仓位永久停在待结算：既拿不到本息，也没有
		//被折算算力（用户资产凭空消失）。现在与成功分支一样先结算到期轮。
		if err = r.settleDueRound(projectId, currentRound); err != nil {
			return err
		}

		//倒2/倒3 轮号；若 MinRound 被改小到与倒2/倒3 重叠，则以「三进一出到期结算」优先，
		//避免同一轮被 Success 与 Failed 竞争处理（claimSettleRound 会让先到者胜，但结果不确定）。
		dueRound, hasDue, failedRoundNums := failureRoundPlan(currentRound, MinRound)
		if hasDue && len(failedRoundNums) < 2 && currentRound > 1 {
			log.WithFields(log.Fields{"roundId": round.ID, "round": currentRound, "dueRound": dueRound, "minRound": MinRound}).
				Warnln("MinRound 过小导致倒2/倒3 与结算窗口重叠，重叠轮按三进一出成功结算处理")
		}
		if len(failedRoundNums) > 0 {
			var failRounds []model.ProjectRound
			if err = r.DB.Table(model.ProjectRoundTable).Where("`round` IN ? and `project_id` = ? and `status` = ?", failedRoundNums, projectId, enum.RoundCompute).Find(&failRounds).Error; err != nil {
				return err
			}
			err = ants.Submit(func() {
				r.RewardManager.Failed(failRounds)
			})
			if err != nil {
				return err
			}
		}

		//倒1=当前未筹满轮：100% 全额退回
		err = ants.Submit(func() {
			r.RewardManager.FailedNormal(round)
		})
		if err != nil {
			return err
		}
	}

	return err
}

// failureRoundPlan 计算"当前失败轮"要处理哪些轮号。
//
// 返回：
//   - dueRound / hasDue：三进一出到期该结算的那一轮（currentRound - minRound），
//     必须与成功轮一样被 Success 结算（无论当前轮成败）—— 这是 2026-09-30 修的"结算断口"；
//   - failedNums：倒2/倒3 轮号（currentRound-1 / currentRound-2），按"扣半 + 折算算力"处理；
//     若 minRound 被改小到与倒2/倒3 重叠，则以"三进一出到期结算"优先，把该轮排除，
//     避免同一轮被 Success 与 Failed 竞争（结果不确定）。
//
// 抽成纯函数便于单测钉死需求口径（N次方三进一出 = 第 N 轮在第 N+3 轮结束时必结算）。
func failureRoundPlan(currentRound, minRound uint) (dueRound uint, hasDue bool, failedNums []uint) {
	hasDue = currentRound > minRound
	if hasDue {
		dueRound = currentRound - minRound
	}
	for delta := uint(1); delta <= 2; delta++ {
		if currentRound < delta+1 {
			continue
		}
		num := currentRound - delta
		if hasDue && num == dueRound {
			continue //与到期轮重叠：由三进一出结算处理
		}
		failedNums = append(failedNums, num)
	}
	return dueRound, hasDue, failedNums
}

// settleDueRound 结算「三进一出」到期的那一轮：投第 N 轮 → 第 N+MinRound 轮 End 时结算。
//
// 成功轮与失败轮都必须调用（规则语义："第 N 轮投入在第 N+MinRound 轮结束时统一结算，
// 无论第 N+MinRound 轮成败" —— N次方-技术方案.md 1.2；千万次需求未规定结算轮次，
// owner 2026-09-08 裁定沿用 N次方三进一出）。
//
// 与旧实现的差异：旧代码在成功分支里直接 First() 且把 ErrRecordNotFound 当错误 return，
// 既让"失败轮漏结"（见 End 失败分支注释），又会在该轮已被人工/巡检结算时中断 End
// （连带跳过自动开下一轮 → 整条轮次链停摆）。现在统一为"该轮不存在或已结算 = 无事可做"。
func (r RoundManager) settleDueRound(projectId uint, currentRound uint) error {
	if currentRound <= MinRound {
		return nil
	}
	rewardRoundNum := currentRound - MinRound
	var rewardRound model.ProjectRound
	err := r.DB.Table(model.ProjectRoundTable).
		Where("`round` = ? and `project_id` = ? and `status` = ?", rewardRoundNum, projectId, enum.RoundCompute).
		First(&rewardRound).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil //该轮不存在或已结算：无需补结
	}
	if err != nil {
		return err
	}
	return ants.Submit(func() {
		r.RewardManager.Success(rewardRound)
	})
}

// RecoverStuckSettlements 结算补偿巡检（由 cmd/round 定时调用，可重复执行、幂等）。
//
// 补两类「结算缺口」：
//  1. 失败边界补偿：项目最近一个失败轮 F 的倒2/倒3（F-1/F-2）若仍停在待结算
//     （例如进程在异步 Failed 任务提交后崩溃、或爆仓价缺失导致旧版整笔回滚），
//     按「退还未扣部分 + 被扣部分折算算力」补结；
//  2. 三进一出到期补偿：任何 `round + MinRound <= 该项目已结束的最大轮号` 且仍待结算的轮，
//     按成功结算补结（覆盖历史遗留数据、异常中断，以及修复前遗留的 F-MinRound 悬空轮）。
//
// 资金安全：两处最终都走 claimSettleRound 的原子抢占，已被结算/正在结算的轮会被跳过，
// 不会重复发放。单个项目/单轮失败只记日志并继续，不影响其它项目。
func RecoverStuckSettlements() {
	var projectIds []uint
	if err := config.MysqlDBPool.Table(model.ProjectTable).Pluck("`id`", &projectIds).Error; err != nil {
		log.WithFields(log.Fields{"Method": "RecoverStuckSettlements"}).Errorln("巡检查询项目失败", err)
		return
	}
	rm := RoundManager{
		DB:              config.MysqlDBPool,
		RewardManager:   NewRewardManager(),
		WalletManager:   NewWalletManager(),
		WalletPointSafe: NewWalletPointSafe(),
	}
	for _, projectId := range projectIds {
		//1)失败边界补偿：最近一个失败轮的前一轮/前二轮
		var failRound model.ProjectRound
		if err := config.MysqlDBPool.Table(model.ProjectRoundTable).
			Where("`project_id` = ? and `success` = 0 and `status` = ?", projectId, enum.RoundEnding).
			Order("`round` DESC").First(&failRound).Error; err == nil {
			var nums []uint
			if failRound.Round >= 2 {
				nums = append(nums, failRound.Round-1)
			}
			if failRound.Round >= 3 {
				nums = append(nums, failRound.Round-2)
			}
			if len(nums) > 0 {
				var pending []model.ProjectRound
				if err := config.MysqlDBPool.Table(model.ProjectRoundTable).
					Where("`project_id` = ? and `round` IN ? and `status` = ?", projectId, nums, enum.RoundCompute).
					Find(&pending).Error; err == nil && len(pending) > 0 {
					log.WithFields(log.Fields{"projectId": projectId, "failRound": failRound.Round, "count": len(pending)}).
						Warnln("补偿巡检：发现未结算的倒2/倒3 轮，按扣半折算算力补结")
					rm.RewardManager.Failed(pending)
				}
			}
		}

		//2)三进一出到期补偿
		var maxEnded uint
		if err := config.MysqlDBPool.Table(model.ProjectRoundTable).
			Where("`project_id` = ? and `status` = ?", projectId, enum.RoundEnding).
			Pluck("COALESCE(MAX(`round`), 0)", &maxEnded).Error; err != nil || maxEnded <= MinRound {
			continue
		}
		var due []model.ProjectRound
		if err := config.MysqlDBPool.Table(model.ProjectRoundTable).
			Where("`project_id` = ? and `status` = ? and `round` + ? <= ?", projectId, enum.RoundCompute, MinRound, maxEnded).
			Find(&due).Error; err != nil || len(due) == 0 {
			continue
		}
		for _, d := range due {
			log.WithFields(log.Fields{"projectId": projectId, "round": d.Round}).
				Warnln("补偿巡检：发现已到期但仍待结算的轮，按三进一出补结")
			rm.RewardManager.Success(d)
		}
	}
}

func NewRoundManager() core.Rounding {
	return RoundManager{
		DB:            config.MysqlDBPool,
		RewardManager: NewRewardManager(),
		WalletManager: NewWalletManager(),
		WalletPointSafe: NewWalletPointSafe(),
	}
}
