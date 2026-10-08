package impl

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/model"
	"errors"
	"fmt"
	"gorm.io/gorm"
)

type ProjectManager struct {
	DB *gorm.DB
}

func (p ProjectManager) Init(symbol string, period uint) (project model.Project, err error) {
	var newProject model.Project
	err = p.DB.Table(model.ProjectTable).Order("`period` DESC").Limit(1).Find(&newProject).Error
	if err != nil {
		return
	}
	if period < newProject.Period {
		err = errors.New(fmt.Sprintf("期数不能小于当前%d期数", newProject.Period))
		return
	}

	project.Symbol = symbol
	project.Period = period
	err = p.DB.Model(&project).Create(&project).Error
	return
}

func (p ProjectManager) Start(project model.Project) error {
	return p.DB.Model(&project).Update("status", enum.ProjectStarting).Error
}

func (p ProjectManager) Stop(project model.Project) error {
	return p.DB.Transaction(func(tx *gorm.DB) error {
		project.Status = 2
		if err := tx.Model(model.Project{}).Where("`id` = ?", project.ID).Update("`status`", 2).Error; err != nil {
			return err
		}
		if err := tx.Where("`project_id` = ?", project.ID).Delete(model.ProjectRound{}).Error; err != nil {
			return err
		}
		return nil
	})
}

func (p ProjectManager) Current(project model.Project) (round model.ProjectRound, err error) {
	projectId := project.ID
	err = p.DB.Table(model.ProjectRoundTable).Where("`project_id` = ? and `status` = ?", projectId, enum.RoundStarting).First(&round).Error
	return
}

func (p ProjectManager) Next(project model.Project) (round model.ProjectRound, err error) {
	projectId := project.ID
	err = p.DB.Order("`created_at` DESC").Table(model.ProjectRoundTable).Where("`project_id` = ? and `status` = ?", projectId, enum.RoundWaiting).First(&round).Error
	return
}

func NewProjectManager() core.Projecting {
	return ProjectManager{
		DB: config.MysqlDBPool,
	}
}