package main

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core/impl"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils/kto"
	"errors"
	"flag"
	"github.com/robfig/cron/v3"
	log "github.com/sirupsen/logrus"
	"os"
	"os/signal"
	"syscall"
)

// ============================== burn 手续费销毁程序（N次方新增） ==============================
// 功能：汇总 fee_burn 表中 status=0（待销毁）的手续费，从资金池钱包链上转出到黑洞地址，
//      直至手续费币 TM 流通量仅剩 7777 枚（通缩设计，BurnTarget 见 core/impl/config.go，
//      手续费币种集中定义于 enum.FeeSymbol）。
// 流程：
//   1. 查询待销毁记录（fee_burn.status=0），按币种汇总
//   2. 链上转账：KtoPool(资金池) → 黑洞地址（yml 配置 Burn.Address）
//   3. 更新记录 status=1（已销毁）并记录 tx_hash
//   4. 已销毁总量达到目标后停止（监控提示）
// 前置条件：确认链支持 burn；不支持则使用黑洞地址 + 公开审计（技术方案 3.3 流程3）。
//
// 用法：./burnRun -f ./burn.yml  （常驻定时任务，默认每小时执行一次）

// burnAddress 黑洞地址（若 yml 未配置则取此默认值，部署前必须替换为链上真实黑洞地址）
func getBurnAddress() string {
	if config.EtcConfig.Burn.Address != "" {
		return config.EtcConfig.Burn.Address
	}
	return "KtoB1ackHole0000000000000000000000000000000" //占位，须替换
}

func main() {
	path := flag.String("f", "./config/burn.yml", "-f 指定配置文件")
	flag.Parse()
	config.Init(*path) //mysql, redis, config file

	//启动前先执行一次（便于上线时立即清零存量待销毁手续费）
	if err := burnOnce(); err != nil {
		log.Errorln("首次销毁失败", err)
	}

	cronNew := cron.New(cron.WithSeconds(), cron.WithLogger(cron.VerbosePrintfLogger(log.StandardLogger())))
	//每小时检查一次待销毁手续费（频率可按需调整）
	if _, err := cronNew.AddFunc("0 0 * * * ?", func() {
		if err := burnOnce(); err != nil {
			log.Errorln("手续费销毁任务失败", err)
		}
	}); err != nil {
		panic(err)
	}

	cronNew.Start()
	defer cronNew.Stop()

	stopSignal := make(chan os.Signal, 1) //buffered：防止信号发送时无人接收导致丢失
	signal.Notify(stopSignal, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGQUIT)
	<-stopSignal
}

// burnOnce 执行一轮销毁：汇总待销毁手续费 → 链上转黑洞 → 标记已销毁
func burnOnce() error {
	//0. 已销毁总量监控（通缩目标 7777，仅统计手续费币 FeeSymbol，
	//   避免历史其他币种销毁记录混入导致除数/口径错乱）
	var burnedTotal uint64
	if err := config.MysqlDBPool.Table(model.FeeBurnTable).
		Select("COALESCE(SUM(`amount`), 0)").
		Where("`status` = ? and `symbol` = ?", 1, enum.FeeSymbol).
		Scan(&burnedTotal).Error; err != nil {
		log.Errorln("查询已销毁总量失败", err)
	}
	burnedFloat := float64(burnedTotal) / config.SymbolDictionary[enum.FeeSymbol]
	if burnedFloat >= impl.BurnTarget {
		log.Infof("已达通缩目标 %v 枚 %v，停止销毁", impl.BurnTarget, enum.FeeSymbol)
		return nil
	}
	log.Infof("当前已销毁(%v): %v，目标: %v", enum.FeeSymbol, burnedFloat, impl.BurnTarget)

	//1. 查询待销毁记录（status IN (0,2)：0 待销毁，2 上次上链失败待重试），按币种汇总
	var pending []model.FeeBurn
	if err := config.MysqlDBPool.Table(model.FeeBurnTable).Find(&pending, "`status` IN ?", []uint{0, 2}).Error; err != nil {
		return err
	}
	if len(pending) == 0 {
		log.Infoln("无待销毁手续费")
		return nil
	}

	//按币种汇总（当前仅手续费币 FeeSymbol，循环保留多币种扩展）
	summary := make(map[string]uint64)
	for _, fb := range pending {
		summary[fb.Symbol] += fb.Amount
	}

	burnAddr := getBurnAddress()
	pool := config.EtcConfig.KtoPool
	for symbol, amount := range summary {
		if amount <= 0 {
			continue
		}
		//2. 原子抢占本币种待销毁记录为"销毁中(status=3)"，防重复销毁。
		//   若链上转账成功但 DB 更新失败，记录滞留 status=3，不会再次上链转账（绝不重复销毁），
		//   由人工核查该笔链上转账后处理（宁可少销毁，不可重复销毁）。
		res := config.MysqlDBPool.Table(model.FeeBurnTable).
			Where("`symbol` = ? and `status` IN ?", symbol, []uint{0, 2}).
			Update("status", 3)
		if res.Error != nil {
			log.Errorf("标记销毁中(%v)失败: %v", symbol, res.Error)
			continue
		}
		if res.RowsAffected == 0 {
			continue // 无待处理记录（已被并发抢占）
		}

		//3. 链上转账到黑洞地址（销毁）
		_, hash, err := kto.KTOonChainSync(symbol, pool.Address, pool.Private, burnAddr, amount)
		if err != nil {
			log.Errorf("销毁 %v %v 上链失败: %v", amount, symbol, err)
			//status=3 → 2（失败，下次 cron 自动重试）
			if err = config.MysqlDBPool.Table(model.FeeBurnTable).Where("`symbol` = ? and `status` = ?", symbol, 3).Update("status", 2).Error; err != nil {
				log.Errorln("标记销毁失败记录出错", err)
			}
			return errors.New("销毁上链失败")
		}
		log.Infof("销毁成功: %v %v, tx_hash: %v, 目标地址: %v", amount, symbol, hash, burnAddr)

		//4. status=3 → 1（已销毁）并记录 tx_hash
		if err = config.MysqlDBPool.Table(model.FeeBurnTable).Where("`symbol` = ? and `status` = ?", symbol, 3).
			Updates(map[string]interface{}{"status": 1, "tx_hash": hash}).Error; err != nil {
			log.Errorln("更新销毁记录失败(记录滞留 status=3，需人工核查该笔链上转账是否已成功)", err)
			return err
		}
	}
	return nil
}
