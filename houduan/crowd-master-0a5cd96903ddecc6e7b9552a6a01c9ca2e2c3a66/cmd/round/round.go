package main

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core/impl"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/model"
	"flag"
	"github.com/panjf2000/ants/v2"
	"github.com/robfig/cron/v3"
	log "github.com/sirupsen/logrus"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//Build round start-end jobs
func main() {
	path := flag.String("f", "/Users/zhengjianfeng_1/go/src/crowd/config/round.yml", "-f 指定配置文件")
	flag.Parse()
	config.Init(*path) //mysql, redis, config file

	roundManager := impl.NewRoundManager()

	stopSignal := make(chan os.Signal, 1) //buffered：防止信号发送时无人接收导致丢失
	// 监听信号
	signal.Notify(stopSignal, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGQUIT)
	cronNew := cron.New(cron.WithSeconds(), cron.WithLogger(cron.VerbosePrintfLogger(log.StandardLogger())))

	_, err := cronNew.AddFunc("*/2 * * * * ?", func() {
		var err error
		var needStarts []model.ProjectRound

		diffTime := -1 * time.Minute + -15 * time.Second
		start := time.Now().Add(diffTime)
		if err = config.MysqlDBPool.Table(model.ProjectRoundTable).Find(&needStarts, "`status` = ? and `start_time` <= ?", enum.RoundWaiting, start).Error; err != nil {
			log.Errorln("search need start project round error: ", err)
		}

		for _, s := range needStarts {
			//N次方：轮次时限动态调整（技术方案 1.1/2.3 改造项6）——按近期轮完成速度动态缩短时限
			adjustRoundTimeLimit(&s)
			err = roundManager.Begin(s)
			if err != nil {
				log.WithFields(log.Fields{"项目轮ID": s.ID}).Errorln("开始项目失败", err)
				continue
			}
		}

		if err != nil {
			log.Errorln("commit starts ant pools error: ", err)
		}

	})

	_, err = cronNew.AddFunc("0 */1 * * * ?", func() {
		var err error
		var needEnds []model.ProjectRound
		end := time.Now()

		if err = config.MysqlDBPool.Table(model.ProjectRoundTable).Find(&needEnds, "`status` = ? and `end_time` <= ?", enum.RoundStarting, end).Error; err != nil {
			log.Errorln("search need end project round error: ", err)
		}
		err = ants.Submit(func() {
			for _, e := range needEnds {
				err = roundManager.End(e)
				if err != nil {
					log.WithFields(log.Fields{"项目轮ID": e.ID}).Errorln("结束项目失败", err)
					continue
				}
			}
		})

		if err != nil {
			log.Errorln("commit ends ant pools error: ", err)
		}

		log.Infoln("ants pools running: ", ants.Running(), ", free:", ants.Free())
	})

	if err != nil {
		log.Errorln("init project round job err", err)
		return
	}

	cronNew.Start()
	defer cronNew.Stop()

	var stopWaiting = 5 * time.Second
	end := make(chan int)
	go func() {
		for {
			select {
			case s := <-stopSignal:
				log.Infoln("stop signal:", s)
				for ants.Running() > 0 {
					log.Infoln("waiting stop ants pools running: ", ants.Running(), ", free:", ants.Free())
					time.Sleep(stopWaiting)
				}

				log.Infoln("end ants pools running: ", ants.Running(), ", free:", ants.Free())
				end <- 1
			}
		}
	}()

	<-end
}

//adjustRoundTimeLimit N次方轮次时限动态调整（技术方案 1.1 每轮时限按市场活跃度动态调整，可 1h/30min/10min）
//规则：查询该项目最近 5 轮已结束轮的实际完成耗时（end_time - start_time），
//      若平均耗时 < 本轮时限的一半（市场活跃、轮次提前完成），则按「平均耗时 × 1.5」缩短本轮时限；
//      受最小时限下限保护（impl.MinTimeLimitSeconds = 600s，10 分钟），防止时限过短导致系统来不及结算。
func adjustRoundTimeLimit(round *model.ProjectRound) {
	//1.查询该项目最近已结束的 5 轮（作为市场活跃度样本）
	var finished []model.ProjectRound
	if err := config.MysqlDBPool.Table(model.ProjectRoundTable).
		Order("`id` DESC").Limit(5).
		Find(&finished, "`project_id` = ? and `status` = ?", round.ProjectId, enum.RoundEnding).Error; err != nil || len(finished) == 0 {
		return //无历史数据，保持默认时限
	}

	//2.平均实际完成耗时（秒）
	var totalSeconds float64
	for _, f := range finished {
		totalSeconds += f.EndTime.Sub(f.StartTime).Seconds()
	}
	avgSeconds := totalSeconds / float64(len(finished))
	if avgSeconds <= 0 {
		return
	}

	//3.活跃度判断：平均耗时 < 当前时限的 50% 才缩短（否则维持原时限）
	curLimit := round.EndTime.Sub(round.StartTime).Seconds()
	if curLimit <= 0 || avgSeconds >= curLimit*0.5 {
		return
	}

	//4.新时限 = 平均耗时 × 1.5，受最小时限下限保护
	newSeconds := avgSeconds * 1.5
	if newSeconds < impl.MinTimeLimitSeconds {
		newSeconds = impl.MinTimeLimitSeconds
	}
	newEnd := round.StartTime.Add(time.Duration(newSeconds) * time.Second)
	round.EndTime = newEnd
	round.TimeLimit = newSeconds

	//5.落库更新（Begin 会读取该记录状态，需同步更新 end_time/time_limit）
	if err := config.MysqlDBPool.Table(model.ProjectRoundTable).
		Where("`id` = ?", round.ID).
		Updates(map[string]interface{}{"end_time": newEnd, "time_limit": newSeconds}).Error; err != nil {
		log.WithFields(log.Fields{"roundId": round.ID}).Errorln("动态调整轮次时限落库失败", err)
		return
	}
	log.WithFields(log.Fields{"roundId": round.ID, "avgSeconds": avgSeconds, "newLimit": newSeconds}).Infoln("轮次时限动态缩短完成")
}
