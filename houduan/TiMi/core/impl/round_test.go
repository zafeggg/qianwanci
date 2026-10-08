package impl

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/model"
	"fmt"
	"testing"
	"time"
)

func TestProjectManager_Init(t *testing.T) {
	Read(t)
	var project model.Project
	err := config.MysqlDBPool.Table(model.ProjectTable).First(&project, "`id` = ?", 28).Error
	if err != nil {
		t.Fatal("find project failed", err)
	}


	roundManager := NewRoundManager()

	for i := 0; i < 20; i++ {
		projectId := project.ID
		target := 100.0
		min := 1.0
		max := 5.0
		startTime := time.Now().Add(1 * time.Second)
		endTime := time.Now().Add(10 * 60 * time.Second)
		projectRound, err := roundManager.Init(projectId, target, min, max, startTime, endTime)
		if err != nil {
			t.Fatal("init project round failed", err)
		}

		t.Log(projectRound)
	}


}

func TestProjectManager_Start(t *testing.T) {

	Read(t)
	var projectRounds []model.ProjectRound
	err := config.MysqlDBPool.Table(model.ProjectRoundTable).Find(&projectRounds, "`status` = ?", 0).Error
	if err != nil {
		t.Fatal("rounds find failed", err)
	}

	roundManager := NewRoundManager()
	for _, round := range projectRounds {
		err = roundManager.Begin(round)
		if err != nil {
			fmt.Println("begin project failed", err)
		}
	}
}

func TestRoundManager_End2(t *testing.T) {
	Read(t)
	roundId := 24
	var round model.ProjectRound
	err := config.MysqlDBPool.Table(model.ProjectRoundTable).First(&round, "`id` = ?", roundId).Error
	if err != nil {
		t.Fatal("rounds find failed", err)
	}


	roundManager := NewRoundManager()


	err = roundManager.End(round)
	if err != nil {
		t.Fatal("end round failed", err)
	}


}

func TestProjectManager_Stop(t *testing.T) {
	Read(t)
	var projects []model.Project
	err := config.MysqlDBPool.Table(model.ProjectTable).Find(&projects).Error
	if err != nil {
		t.Fatal("find project failed", err)
	}


	_ = NewProjectManager()

}
