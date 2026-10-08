package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core/impl"

	log "github.com/sirupsen/logrus"
	glogger "gorm.io/gorm/logger"
)

// ============================== C 模块：账本与关系对账工具 ==============================
// 只读巡检，不改任何数据。用于上线前自检与日常巡检（可挂 cron / CI）。
//
// 用法：recon.exe -f config/etc.local.yml
// 退出码：0 = 全部通过；2 = 存在 ERROR 级异常；1 = 运行失败
//
// 检查项：
//   [ERROR] 钱包地址重复 / 邀请码重复 / 充值哈希重复（唯一索引缺失或历史脏数据）
//   [ERROR] 闭包表自环边、重复边、悬空边（会让动态/团队收益算错或漏算）
//   [ERROR] 充值记录指向不存在的钱包
//   [ERROR] 轮次 (project_id, round) 重复（同一轮存在多行，结算口径歧义）
//   [WARN]  结算器停摆：存在 status=Starting 且 end_time 已过的轮次
//   [WARN]  待销毁手续费积压：fee_burn 中 status IN (0,2) 的记录

type finding struct {
	Level   string // ERROR | WARN
	Name    string
	Count   int64
	Samples []string
}

var findings []finding

func add(level, name string, count int64, samples []string) {
	findings = append(findings, finding{Level: level, Name: name, Count: count, Samples: samples})
}

// scalar 执行只读查询并取第一列第一行（用于 count(*) 之类）
func scalar(sqlText string, args ...interface{}) int64 {
	var n int64
	if err := config.MysqlDBPool.Raw(sqlText, args...).Scan(&n).Error; err != nil {
		log.WithFields(log.Fields{"err": err}).Errorln("对账查询失败", sqlText)
		return -1
	}
	return n
}

// stringsOf 执行只读查询，取第一列的若干样例行
func stringsOf(limit int, sqlText string, args ...interface{}) []string {
	var out []string
	if err := config.MysqlDBPool.Raw(sqlText, args...).Scan(&out).Error; err != nil {
		log.WithFields(log.Fields{"err": err}).Warnln("对账取样失败", sqlText)
	}
	return out
}

func main() {
	path := flag.String("f", "./config/etc.yml", "-f 指定配置文件")
	flag.Parse()
	config.Init(*path)
	//对账工具是给人看的报告，先关掉 gorm 的 SQL 日志（默认 Info 级会把报告淹掉）
	config.MysqlDBPool.Logger = glogger.Default.LogMode(glogger.Silent)
	impl.LoadNpowerParams() //对账口径依赖运行参数（手续费币种/销毁目标）

	fmt.Println("==================== 千万次对账报告 ====================")
	fmt.Printf("时间: %s\n数据库: %s\n\n", time.Now().Format(time.RFC3339), maskDsn(config.EtcConfig.Mysql.Dns))

	checkWalletUniqueness()
	checkRechargeIntegrity()
	checkTreeIntegrity()
	checkRoundIntegrity()
	checkBurnBacklog()

	errCount, warnCount := 0, 0
	for _, f := range findings {
		//计数口径：只统计"真有命中"的检查项数，而非检查项总数
		if f.Count <= 0 {
			continue
		}
		if f.Level == "ERROR" {
			errCount++
		} else {
			warnCount++
		}
	}
	for _, f := range findings {
		mark := "OK  "
		if f.Count > 0 {
			mark = f.Level
		}
		fmt.Printf("[%s] %-34s 命中 %d\n", mark, f.Name, f.Count)
		if f.Count > 0 {
			for _, s := range f.Samples {
				fmt.Printf("        · %s\n", s)
			}
		}
	}
	fmt.Println("\n------------------------------------------------------")
	fmt.Printf("异常检查项: ERROR %d 项，WARN %d 项（共 %d 项检查）\n", errCount, warnCount, len(findings))
	if errCount > 0 {
		fmt.Println("结论: 未通过（请先修复 ERROR 级异常）")
		os.Exit(2)
	}
	fmt.Println("结论: 通过")
}

// maskDsn 隐去 DSN 中的口令，避免日志泄露
func maskDsn(dsn string) string {
	at := -1
	for i, ch := range dsn {
		if ch == '@' {
			at = i
			break
		}
	}
	if at < 0 {
		return dsn
	}
	colon := -1
	for i := 0; i < at; i++ {
		if dsn[i] == ':' {
			colon = i
			break
		}
	}
	if colon < 0 {
		return dsn
	}
	return dsn[:colon+1] + "******" + dsn[at:]
}

func checkWalletUniqueness() {
	n := scalar("SELECT COUNT(*) FROM (SELECT address FROM wallet GROUP BY address HAVING COUNT(*) > 1) t")
	add("ERROR", "钱包地址重复", n, stringsOf(5,
		"SELECT address FROM wallet GROUP BY address HAVING COUNT(*) > 1 LIMIT 5"))

	n = scalar("SELECT COUNT(*) FROM (SELECT code FROM wallet WHERE code <> '' GROUP BY code HAVING COUNT(*) > 1) t")
	add("ERROR", "邀请码重复", n, stringsOf(5,
		"SELECT code FROM wallet WHERE code <> '' GROUP BY code HAVING COUNT(*) > 1 LIMIT 5"))
}

func checkRechargeIntegrity() {
	n := scalar("SELECT COUNT(*) FROM (SELECT hash FROM wallet_hash_charge WHERE hash <> '' GROUP BY hash HAVING COUNT(*) > 1) t")
	add("ERROR", "充值哈希重复", n, stringsOf(5,
		"SELECT hash FROM wallet_hash_charge WHERE hash <> '' GROUP BY hash HAVING COUNT(*) > 1 LIMIT 5"))

	n = scalar("SELECT COUNT(*) FROM wallet_hash_charge c LEFT JOIN wallet w ON w.address = c.address WHERE w.id IS NULL")
	add("ERROR", "充值记录指向未知钱包", n, stringsOf(5,
		"SELECT c.address FROM wallet_hash_charge c LEFT JOIN wallet w ON w.address = c.address WHERE w.id IS NULL LIMIT 5"))

	n = scalar("SELECT COUNT(*) FROM wallet_hash_charge WHERE hash = '' OR amount = 0")
	add("WARN", "充值记录哈希为空或金额为 0", n, stringsOf(5,
		"SELECT CONCAT('id=', id, ' hash=', hash, ' amount=', amount) FROM wallet_hash_charge WHERE hash = '' OR amount = 0 LIMIT 5"))
}

func checkTreeIntegrity() {
	n := scalar("SELECT COUNT(*) FROM wallet_tree WHERE ancestor = descendant")
	add("ERROR", "闭包表自环边", n, stringsOf(5,
		"SELECT CONCAT('wallet=', ancestor) FROM wallet_tree WHERE ancestor = descendant LIMIT 5"))

	n = scalar("SELECT COUNT(*) FROM (SELECT ancestor, descendant, distance FROM wallet_tree GROUP BY ancestor, descendant, distance HAVING COUNT(*) > 1) t")
	add("ERROR", "闭包表重复边", n, stringsOf(5,
		"SELECT CONCAT(ancestor, '->', descendant, ' d=', distance) FROM wallet_tree GROUP BY ancestor, descendant, distance HAVING COUNT(*) > 1 LIMIT 5"))

	n = scalar(`SELECT COUNT(*) FROM wallet_tree t
		LEFT JOIN wallet a ON a.id = t.ancestor
		LEFT JOIN wallet d ON d.id = t.descendant
		WHERE a.id IS NULL OR d.id IS NULL`)
	add("ERROR", "闭包表悬空边", n, stringsOf(5, `SELECT CONCAT('a=', t.ancestor, ' d=', t.descendant) FROM wallet_tree t
		LEFT JOIN wallet a ON a.id = t.ancestor
		LEFT JOIN wallet d ON d.id = t.descendant
		WHERE a.id IS NULL OR d.id IS NULL LIMIT 5`))

	// 直推边（distance=0）应唯一：同一父对同一子只应有一条
	n = scalar(`SELECT COUNT(*) FROM (SELECT ancestor, descendant FROM wallet_tree WHERE distance = 0
		GROUP BY ancestor, descendant HAVING COUNT(*) > 1) t`)
	add("ERROR", "直推边重复", n, nil)
}

func checkRoundIntegrity() {
	n := scalar("SELECT COUNT(*) FROM (SELECT project_id, round FROM project_round GROUP BY project_id, round HAVING COUNT(*) > 1) t")
	add("ERROR", "轮次 (项目,轮号) 重复", n, stringsOf(5,
		"SELECT CONCAT('project=', project_id, ' round=', round) FROM project_round GROUP BY project_id, round HAVING COUNT(*) > 1 LIMIT 5"))

	// status=1（进行中）但 end_time 已过：结算器（round.exe）未运行或停摆
	n = scalar("SELECT COUNT(*) FROM project_round WHERE status = 1 AND end_time <= NOW()")
	add("WARN", "过期未结算轮次（结算器停摆）", n, stringsOf(5,
		"SELECT CONCAT('round=', round, ' end=', end_time) FROM project_round WHERE status = 1 AND end_time <= NOW() LIMIT 5"))

	// 已结束轮次的 status 与 success 组合异常（既非成功也非失败）
	n = scalar("SELECT COUNT(*) FROM project_round WHERE current_vote > target_vote AND status = 3 AND success = 0")
	add("WARN", "已结束但标记为失败的超募轮", n, nil)
}

func checkBurnBacklog() {
	n := scalar("SELECT COUNT(*) FROM fee_burn WHERE status IN (0,2)")
	add("WARN", "待销毁手续费记录积压", n, stringsOf(5,
		"SELECT CONCAT('id=', id, ' symbol=', symbol, ' amount=', amount, ' status=', status) FROM fee_burn WHERE status IN (0,2) LIMIT 5"))

	// status=3 为"销毁中"占位；链上成功但 DB 更新失败会滞留，需人工核查
	n = scalar("SELECT COUNT(*) FROM fee_burn WHERE status = 3")
	add("WARN", "销毁中滞留记录（需人工核查链上）", n, stringsOf(5,
		"SELECT CONCAT('id=', id, ' symbol=', symbol, ' amount=', amount) FROM fee_burn WHERE status = 3 LIMIT 5"))

	if impl.FeeSymbol != "" {
		var burned uint64
		if err := config.MysqlDBPool.Raw(
			"SELECT COALESCE(SUM(amount),0) FROM fee_burn WHERE symbol = ? AND status = 1",
			impl.FeeSymbol).Scan(&burned).Error; err == nil {
			scale := config.SymbolDictionary[impl.FeeSymbol]
			if scale == 0 {
				scale = 1
			}
			//⚠ 2026-09-30 澄清口径：BurnTarget 的语义是「剩余流通量下限」，而这里统计的是
			//「累计销毁量」——两者不是同一个量（TM 初始发行量未定时尤其不能混用）。
			//因此本行只作为**参考信息**输出，不再当成"通缩进度"；真实判据在链上 totalSupply
			//（cmd/burn 已改为读链上，见 evmBurnTargetReached）。
			fmt.Printf("[INFO] %s 累计销毁 %.4f 枚（参考值；通缩判据 = 链上剩余流通量 ≤ BurnTarget %.4f 枚，需查链上 totalSupply）\n",
				impl.FeeSymbol, float64(burned)/scale, impl.BurnTarget)
		}
	}
}
