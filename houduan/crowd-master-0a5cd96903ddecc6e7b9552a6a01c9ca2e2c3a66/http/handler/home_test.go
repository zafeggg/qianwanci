package handler

import (
	"com.fibonacci.crowd/http/resp"
	"fmt"
	"github.com/go-playground/assert/v2"
	"math"
	"sort"
	"testing"
	"time"
)

func TestSortBy(t *testing.T) {

	roundList := []resp.Round{
		resp.Round{
			StartTime: time.Now().Add(1 * time.Hour),
			EndTime:   time.Time{},
			Status:    3,
		},
		resp.Round{
			StartTime: time.Now().Add(2 * time.Hour),
			EndTime:   time.Time{},
			Status:    1,
		},
		resp.Round{
			StartTime: time.Now().Add(3 * time.Hour),
			EndTime:   time.Time{},
			Status:    2,
		},
		resp.Round{
			StartTime: time.Now().Add(4 * time.Hour),
			EndTime:   time.Time{},
			Status:    1,
		},
		resp.Round{
			StartTime: time.Now().Add(5 * time.Hour),
			EndTime:   time.Time{},
			Status:    0,
		},
		resp.Round{
			StartTime: time.Now().Add(6 * time.Hour),
			EndTime:   time.Time{},
			Status:    0,
		},
		resp.Round{
			StartTime: time.Now().Add(6 * time.Hour),
			EndTime:   time.Time{},
			Status:    0,
		},
	}

	t.Log("before: ", roundList)

	sort.SliceStable(roundList, func(i, j int) bool {
		if roundList[i].Status == 1 {
			return true
		}

		if roundList[i].Status < roundList[i].Status {
			return true
		}

		return false
	})

	sort.SliceStable(roundList, func(i, j int) bool {
		if roundList[i].Status == roundList[j].Status {
			if roundList[i].StartTime.After(roundList[j].StartTime) {
				return false
			} else {
				return true
			}
		}
		return false
	})

	t.Log("after: ", roundList)
	assert.Equal(t, roundList[0].Status, uint(1))
}

func TestMathRound(t *testing.T) {
	fmt.Println(math.Round(3712 + (3712 * 0.3)))
}
