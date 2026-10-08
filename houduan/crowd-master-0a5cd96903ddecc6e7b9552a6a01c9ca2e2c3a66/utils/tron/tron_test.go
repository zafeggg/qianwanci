package tron

import (
	"os"
	"com.fibonacci.crowd/config"
	"flag"
	"testing"
	"time"
)


func Read() {
	path := flag.String("f", "/Users/zhengjianfeng_1/go/src/crowd/config/etc.yml", "-f 指定配置文件")
	flag.Parse()
	config.Init(*path) //mysql, redis, config file
}


func TestGetTransactionInfo(t *testing.T) {
	Read()

	status, err := GetTransactionInfo("233bcccd13ae4d7156b6b97da1532acf27b00cc69aac3c8cf44b5cc7f029b344")
	if err != nil {
		t.Fatal("get transaction info err", err)
	}

	t.Log("status", status)
}

func TestGetTrxBalance(t *testing.T) {
	Read()

	amount, err := GetTrxBalance("TR7DbxQJNTn1zErw7ihG46NuZJnB6E4KhY")
	if err != nil {
		t.Fatal("get trx balance err", err)
	}

	t.Log(amount)
}

func TestGetUsdtBalance(t *testing.T) {
	Read()

	amount, err := GetUsdtBalance("TR7DbxQJNTn1zErw7ihG46NuZJnB6E4KhY")
	if err != nil {
		t.Fatal("get trx balance err", err)
	}

	t.Log(amount)
}


func TestTransferUsdtNoTrxFee(t *testing.T) {
	Read()

	// 2026-10-08 安全修复：原硬编码私钥字面量已移除（仓库转 public 前的清密钥动作），
	//   改为环境变量注入；存档不再开发，仅保留用例骨架。
	priv := os.Getenv("TRON_POOL_PRIVATE_KEY")
	if priv == "" {
		t.Skip("未设置 TRON_POOL_PRIVATE_KEY，跳过")
	}
	txId, err := TransferUsdt(priv, "TRkEj9XA7nha3nQ2zjHVb5cdKXks8wrgQS", 2000000)
	if err != nil {
		t.Fatal("", err)
	}

	status, err := GetTransactionInfo(txId)
	if err != nil {
		t.Fatal("", err)
	}


	t.Log(txId, status)
}

func TestTransferTrx(t *testing.T) {
	Read()

	txId, err := TransferTrxFee(os.Getenv("TRON_POOL_PRIVATE_KEY"), "TRkEj9XA7nha3nQ2zjHVb5cdKXks8wrgQS")
	if err != nil {
		t.Fatal("", err)
	}



	for i := 0; i < 10; i++ {
		status, err := GetTransactionInfo(txId)
		if err != nil {
			t.Fatal("", err)
		}

		if status == "" {
			time.Sleep(50000)
		}else {


			break
		}

		t.Log(txId, status)
	}

}
