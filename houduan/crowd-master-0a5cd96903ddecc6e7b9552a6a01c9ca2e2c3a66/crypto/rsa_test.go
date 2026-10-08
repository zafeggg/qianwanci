package crypto

import (
	"fmt"
	"github.com/go-playground/assert/v2"
	"testing"
)

func TestGenerateRSAKey(t *testing.T) {
	GenerateRSAKey(2048)
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

	assert.Equal(t, string(data), decrypt)
}