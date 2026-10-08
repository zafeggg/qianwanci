package utils

import (
	"fmt"
	"math/rand"
	"strings"
)

var strstr = []byte("0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

// GetUID ...
func GetUID() string {
	data := make([]byte, 16)
	_, err := rand.Read(data)
	if err != nil {
		panic(err)
	}
	uuid := fmt.Sprintf("%X-%X-%X-%X-%X", data[0:4], data[4:6], data[6:8], data[8:10], data[10:])
	return uuid
}

// GetString ...
func GetString(size int) string {
	data := make([]byte, size)
	out := make([]byte, size)
	buffer := len(strstr)
	_, err := rand.Read(data)
	if err != nil {
		panic(err)
	}
	for id, key := range data {
		x := byte(int(key) % buffer)
		out[id] = strstr[x]
	}

	outUp := strings.ToUpper(string(out))
	return outUp
}


