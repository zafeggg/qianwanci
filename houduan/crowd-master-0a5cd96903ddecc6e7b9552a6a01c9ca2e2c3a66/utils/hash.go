package utils

import (
	"crypto/sha256"
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"time"

	"github.com/JaSei/hashutil-go"
	"github.com/gofrs/uuid"
)

func GetHash(bs []byte) (string, error) {
	timestamp := time.Now().Unix()
	sb := string(bs) + Int64ToStr(timestamp)
	hash, err := hashutil.StringToSha256(sb)
	if err != nil {
		return "", err
	}
	return hash.String(), nil
}

func GetRandomHash() (string, error) {
	u2, err := uuid.NewV4()
	if err != nil {
		log.Printf("GetRandomHash: %v \n", err)
		timestamp := time.Now().Unix()
		return Int64ToStr(timestamp), nil
	}
	bytes := u2.Bytes()
	h := sha256.New()
	h.Write(bytes)
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
const (
	letterIdxBits = 6                    // 6 bits to represent a letter index
	letterIdxMask = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
	letterIdxMax  = 63 / letterIdxBits   // # of letter indices fitting in 63 bits
)

//RandStringBytesMaskImprovement 获取随机字符串
func RandStringBytesMaskImprovement(n int) string {
	b := make([]byte, n)
	// A rand.Int63() generates 63 random bits, enough for letterIdxMax letters!
	for i, cache, remain := n-1, rand.Int63(), letterIdxMax; i >= 0; {
		if remain == 0 {
			cache, remain = rand.Int63(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < len(letterBytes) {
			b[i] = letterBytes[idx]
			i--
		}
		cache >>= letterIdxBits
		remain--
	}
	return string(b)
}

func Int64ToStr(i int64) string {
	return strconv.FormatInt(i, 10)
}