package utils

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"os"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
)

// defaultAesSecretKey 用户私钥加密口令的**内置默认值**（历史值，必须保留）：
// 它用于加解密 wallet.sign（库内保存的用户私钥），改动会让既有数据无法解密。
const defaultAesSecretKey = "0xcefE1Db8be8399"

// aesSecretEnv 覆盖口令的环境变量名（可选）。
const aesSecretEnv = "NPOWER_AES_SECRET_KEY"

var (
	aesKeyOnce sync.Once
	aesKeyVal  []byte
)

// AesSecretKey 返回用户私钥加解密口令（AES-128/192/256，取决于长度）。
//
// 取值优先级：环境变量 NPOWER_AES_SECRET_KEY > 内置默认值（保证历史数据仍可解密）。
//
// ⚠ 为什么不能随便改：该口令用于 wallet.sign（注册时用 `AesEncrypt(用户私钥, 口令)` 落库，
// 导出私钥 / 链上出金时再解回来）。**换口令 = 库内既有密文全部解不开**。
// 因此：
//   - 新部署：在写入任何用户数据**之前**用环境变量设定（此时库里没有旧密文）；
//   - 已上线：必须先做数据迁移（旧口令解密 → 新口令重加密），不能只改环境变量。
// 非法长度（非 16/24/32）会被拒绝并回落到内置默认值，避免"配错一个值导致全站私钥解不开"。
func AesSecretKey() []byte {
	aesKeyOnce.Do(func() {
		def := []byte(defaultAesSecretKey)
		env := strings.TrimSpace(os.Getenv(aesSecretEnv))
		if env == "" {
			aesKeyVal = def
			return
		}
		switch len(env) {
		case 16, 24, 32:
			aesKeyVal = []byte(env)
			log.Warnln("[crypto] 正在使用环境变量 " + aesSecretEnv + " 作为用户私钥加密口令；" +
				"若库内已有 wallet.sign 且是用旧口令加密的，将无法解密（需先做重加密迁移）")
		default:
			log.Errorf("[crypto] %s 长度必须为 16/24/32（AES-128/192/256），当前 %d 位；已回落到内置默认口令",
				aesSecretEnv, len(env))
			aesKeyVal = def
		}
	})
	return aesKeyVal
}

func PKCS7Padding(ciphertext []byte, blockSize int) []byte {
	padding := blockSize - len(ciphertext) % blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(ciphertext, padtext...)
}

func PKCS7UnPadding(origData []byte) []byte {
	length := len(origData)
	unpadding := int(origData[length-1])
	return origData[:(length - unpadding)]
}

func AesEncrypt(origData, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	blockSize := block.BlockSize()
	origData = PKCS7Padding(origData, blockSize)
	blockMode := cipher.NewCBCEncrypter(block, key[:blockSize])
	crypted := make([]byte, len(origData))
	blockMode.CryptBlocks(crypted, origData)
	return crypted, nil
}

func AesDecrypt(crypted, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	blockSize := block.BlockSize()
	blockMode := cipher.NewCBCDecrypter(block, key[:blockSize])
	origData := make([]byte, len(crypted))
	blockMode.CryptBlocks(origData, crypted)
	origData = PKCS7UnPadding(origData)
	return origData, nil
}
