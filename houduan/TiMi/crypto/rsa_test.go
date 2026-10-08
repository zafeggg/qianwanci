package crypto

import (
	"fmt"
	"github.com/go-playground/assert/v2"
	"os"
	"testing"
)

func TestGenerateRSAKey(t *testing.T) {
	//⚠ 该用例会把 private.pem/public.pem 写到**当前工作目录**。
	//原来直接在包目录（crypto/）下运行，等于用测试随机密钥覆盖仓库里真正用于传输协议的
	//rsa 密钥对（跑一次 go test 就把现网签名密钥换掉）。这里切到临时目录生成，只验证能生成。
	t.Chdir(t.TempDir())
	GenerateRSAKey(2048)
	for _, f := range []string{"private.pem", "public.pem"} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("应生成 %s：%v", f, err)
		}
	}
}

func TestRsaDecryptToString(t *testing.T) {
	data := []byte("aduCNHCExHd2pvodmZNd9Iz+c8rLMXG755qLqkCStkgteDheF50I1Tl/b4q3MQflRX6D3xSBnxOAfIScdprvVnFvpSxuRvWLW9TfXEOdt1N+bZt2+Vup3EV6lotCQs8rWWzfGlM3AKZhgUH8XApM4N5fE9ap0xjuC830vP73QoXKkcJ2PZJW2RCrW7gYr1eVryyDz6jPigelhKvjZvC6lFpnSzdU/E87cCvcM12ZMp6kqVJJoHWLDNdVfIicjNcDy4q+e6r5EQLu+M8kHbRkqw9ZepV6rscV+URknyhaoCR5NMem990HPY5gJP/9drvJQJa2dYtwe8nCgho5LjhJgQ==")
	decrypt, err := RsaDecryptBase(data)
	if err != nil {
		t.Fatal("解密错误", err)
	}
	fmt.Println("decrypt: ", string(decrypt))
}

func TestRas(t *testing.T) {

	data := []byte("0.01")

	encrypt, err := RsaEncrypt(data)
	if err != nil {
		t.Fatal("加密错误", err)
	}
	decrypt, err := RsaDecrypt(encrypt)
	if err != nil {
		t.Fatal("解密错误", err)
	}

	fmt.Println("decrypt: ", string(decrypt))

	//修复（2026-09-30）：原断言把 string 与 []byte 直接比较，永远失败
	//（报错形如 `0.01 does not equal [48 46 48 49]`）—— 即该用例从来没通过过，
	//这会掩盖真正的加解密回归。加解密本身是正确的（解密结果就是 "0.01"）。
	assert.Equal(t, string(data), string(decrypt))
}