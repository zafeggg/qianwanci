package main

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core/impl"
	"flag"
	"github.com/robfig/cron/v3"
	log "github.com/sirupsen/logrus"
	"os"
	"os/signal"
	"syscall"
)

// ============================== hashpower 算力矿机定时任务（N次方新增程序） ==============================
// 功能：按 cron 配置周期性执行算力矿机每日产币（DailyReward）。
// 产币规则（详见 core/impl/hashpower.go）：
//   矿池日产出 = FIBO 每日区块总产出 × 0.3
//   个人日产出 = 矿池日产出 × (个人算力 / 总算力)
//   模式1/2 保底：日产出 ≥ (算力×3)/300/4h均价
//   金本位出局：累计产出价值 ≥ 算力×3
// 部署：与 mining 类似，Linux amd64 交叉编译后 nohup 常驻运行。
//
// 用法：./hashpowerRun -f ./hashpower.yml [-daily 每日区块总产出]

func main() {
	path := flag.String("f", "./config/hashpower.yml", "-f 指定配置文件")
	// rewardDailyFibo 为 FIBO 每日区块总产出（原 mining.go 硬编码 3498542274052*0.425，此处配置化）
	rewardDailyFibo := flag.Float64("daily", 3498542274052*0.425, "-daily 指定FIBO每日区块总产出")
	flag.Parse()

	config.Init(*path) //mysql, redis, config file

	hashPowerManager := impl.NewHashPowerManager()

	cronNew := cron.New(cron.WithSeconds(), cron.WithLogger(cron.VerbosePrintfLogger(log.StandardLogger())))

	//每日产币（默认每天 00:30 执行一次，可按需调整 cron 表达式）
	_, err := cronNew.AddFunc("0 30 0 * * ?", func() {
		if err := hashPowerManager.DailyReward(*rewardDailyFibo); err != nil {
			log.WithFields(log.Fields{"Method": "DailyReward"}).Errorln("算力矿机产币失败", err)
		}
	})
	if err != nil {
		panic(err)
	}

	cronNew.Start()
	defer cronNew.Stop()

	stopSignal := make(chan os.Signal, 1) //buffered：防止信号发送时无人接收导致丢失
	signal.Notify(stopSignal, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGQUIT)
	<-stopSignal
}
