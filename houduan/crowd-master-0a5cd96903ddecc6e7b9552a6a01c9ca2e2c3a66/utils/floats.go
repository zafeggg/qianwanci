package utils

import (
	"math"
	"strconv"
)

func TrunFloat(f float64, prec int) float64 {
	x := math.Pow10(prec)
	return math.Trunc(f*x) / x
}


func FixedFloat0(num float64) float64 {
	if num == 0 {
		return 0
	}
	return TrunFloat(num, 0)
}


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

func FixedFloat7(num float64) float64 {
	if num == 0 {
		return 0
	}
	return TrunFloat(num, 7)
}

//StrToFloat64 str-> float64
func StrToFloat64(d string) (result float64, err error) {
	if result, err = strconv.ParseFloat(d, 64); err != nil {
		return
	}
	return
}

