package impl

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/model"

	log "github.com/sirupsen/logrus"
)

// ============================== 启动期数据清理（必须先于 AutoMigrate）==============================
//
// 背景：ProjectRound 新增唯一索引 uk_project_round_no(project_id, round)。
// 历史库中可能存在重复轮号（/demo/bootstrap 早期实现用「首行 round+1」推下一个轮号，
// 项目已有 ≥2 行轮次时必然重复；recon 的「轮次 (项目,轮号) 重复」检查即为此项）。
// 唯一索引建立失败会让 AutoMigrate 报 err 1062 且索引静默缺失——必须在迁移前清干净。
//
// ⚠ 必须用**硬删除**：软删除行仍占用唯一索引（MySQL 唯一索引不排除 deleted_at 非空的行），
//   把重复行置 deleted_at 后再建索引，仍然会撞 1062（本次实测复现）。
//   被删的是「同一项目同一轮号的旧副本」，业务上等价于同一条轮次记录的陈旧状态，
//   且各程序只按 (project_id, round) 或 id 读取/结算，删除不会造成引用悬空。
//
// 保留策略：同组内保留 id 最大的一行（最后写入 = 当前状态，可能是进行中/待结算，不能丢）。
func DedupeProjectRounds() {
	if config.MysqlDBPool == nil {
		return
	}

	//1.找出重复组。注意：**包含已软删的行**（它们同样占用唯一索引，必须先物理清掉）
	type dupGroup struct {
		ProjectId uint
		Round     uint
	}
	var groups []dupGroup
	if err := config.MysqlDBPool.Raw(
		"SELECT project_id AS project_id, round AS round FROM " + model.ProjectRoundTable +
			" GROUP BY project_id, round HAVING COUNT(*) > 1",
	).Scan(&groups).Error; err != nil {
		log.WithFields(log.Fields{"err": err}).Warnln("重复轮号巡检失败（跳过清理，不阻断启动）")
		return
	}
	if len(groups) == 0 {
		return
	}

	log.WithFields(log.Fields{"groups": len(groups)}).Warnln("检测到重复轮号，开始清理（保留 id 最大的一行，其余物理删除）")

	for _, g := range groups {
		//Unscoped：连软删行一起查，确保同组只留一行
		var rows []model.ProjectRound
		if err := config.MysqlDBPool.Unscoped().Table(model.ProjectRoundTable).
			Where("`project_id` = ? and `round` = ?", g.ProjectId, g.Round).
			Order("`id` DESC").Find(&rows).Error; err != nil {
			log.WithFields(log.Fields{"projectId": g.ProjectId, "round": g.Round, "err": err}).
				Errorln("读取重复轮次失败，跳过该组")
			continue
		}
		if len(rows) <= 1 {
			continue
		}
		keep := rows[0]  //id 最大 = 最后写入 = 当前状态
		drop := rows[1:] //更早的重复副本（含历史上被软删的）
		ids := make([]uint, 0, len(drop))
		for _, d := range drop {
			ids = append(ids, d.ID)
		}
		//硬删除：Unscoped().Delete 才会真正 DELETE（否则只是再置一次 deleted_at）
		if err := config.MysqlDBPool.Unscoped().Table(model.ProjectRoundTable).
			Where("`id` IN ?", ids).Delete(&model.ProjectRound{}).Error; err != nil {
			log.WithFields(log.Fields{"ids": ids, "err": err}).Errorln("删除重复轮次失败，跳过该组")
			continue
		}
		log.WithFields(log.Fields{
			"projectId": g.ProjectId, "round": g.Round,
			"keepId": keep.ID, "droppedIds": ids,
		}).Warnln("重复轮次已清理（保留最新行）")
	}
}
