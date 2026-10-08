package utils

import (
	"math"
	"strconv"
)

func TrunFloat(f float64, prec int) float64 {
	x := math.Pow10(prec)
	return math.Trunc(f*x) / x
}

// 说明（2026-09-30 清理）：原文件里还有 FixedFloat0 / FixedFloat7，全仓库零引用，已删除。
// 保留 FixedFloat2/3/4/6（均有调用点）。

func FixedFloat2(num float64) float64 {
	if num == 0 {
		return 0
	}
	return TrunFloat(num, 2)
}

func FixedFloat3(num float64) float64 {
	if num == 0 {
		return 0
	}
	return TrunFloat(num, 3)
}


func FixedFloat4(num float64) float64 {
	if num == 0 {
		return 0
	}
	return TrunFloat(num, 4)
}

func FixedFloat6(num float64) float64 {
	if num == 0 {
		return 0
	}
	return TrunFloat(num, 6)
}

//StrToFloat64 str-> float64
func StrToFloat64(d string) (result float64, err error) {
	if result, err = strconv.ParseFloat(d, 64); err != nil {
		return
	}
	return
}

