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
// 用法：./hashpower -f ./hashpower.yml [-daily 每日区块总产出] [-once]
//
// 每日区块总产出的取值优先级（TiMi需求#8）：
//   -daily 命令行（显式指定） > params.HashPowerPoolDailyOutput（config 表 params / yml Params 段）> 代码默认值
// 即：**日常运营应改 params，不用碰命令行**；-daily 仅用于临时覆盖/排障。
//
// -once：只跑一次日产出就退出，不进 cron 常驻。
//   用途：①补跑漏掉的一天 ②离线/私链环境下做「金本位三倍出局」的多日仿真（脚本循环调用 N 次 = N 天）。
//   注意：-once 本身不补多天，也不会跳过时间——需要多少天就调用多少次。

func main() {
	path := flag.String("f", "./config/hashpower.yml", "-f 指定配置文件")
	// rewardDailyFibo 为 FIBO 每日区块总产出。0 = 未指定，改用 params/代码默认值
	// （原实现把历史值硬编码成 flag 默认值，导致「参数化了但实际改不动」）
	rewardDailyFibo := flag.Float64("daily", 0, "-daily 指定FIBO每日区块总产出（0=取参数 HashPowerPoolDailyOutput）")
	once := flag.Bool("once", false, "-once 只执行一次日产出后退出（不进 cron 常驻）")
	flag.Parse()

	config.Init(*path)      //mysql, redis, config file
	impl.LoadNpowerParams() //迭代0：加载运行参数（算力矿机 Buff/保底天数/矿池占比/每日区块总产出等）
	impl.WatchNpowerParamsReload()

	daily := *rewardDailyFibo
	if daily <= 0 {
		daily = impl.HashPowerPoolDailyOutput
	}
	log.WithFields(log.Fields{
		"dailyOutput": daily, "poolRatio": impl.HashPowerPoolDailyRatio,
		"poolDailyReward": daily * impl.HashPowerPoolDailyRatio,
	}).Infoln("算力矿机日产出参数")

	hashPowerManager := impl.NewHashPowerManager()

	if *once {
		if err := hashPowerManager.DailyReward(daily); err != nil {
			log.WithFields(log.Fields{"Method": "DailyReward"}).Errorln("算力矿机产币失败", err)
			os.Exit(1)
		}
		log.Infoln("算力矿机日产出单次执行完成（-once）")
		return
	}

	cronNew := cron.New(cron.WithSeconds(), cron.WithLogger(cron.VerbosePrintfLogger(log.StandardLogger())))

	//每日产币（默认每天 00:30 执行一次，可按需调整 cron 表达式）
	_, err := cronNew.AddFunc("0 30 0 * * ?", func() {
		//每轮从运行参数现取，支持 ops 改参后免重启生效
		if err := hashPowerManager.DailyReward(impl.HashPowerPoolDailyOutput); err != nil {
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
