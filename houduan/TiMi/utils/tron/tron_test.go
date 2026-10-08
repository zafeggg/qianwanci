package tron

import (
	"com.fibonacci.crowd/config"
	"os"
	"testing"
	"time"
)

// Read 初始化测试配置（集成测试前置）。
// 修复（2026-09-30）：原实现把原作者的 macOS 绝对路径写死成 flag 默认值，任何其它机器上
// 都直接 panic。现在改为显式 opt-in（NPOWER_TEST_CONFIG），未设置或依赖不可用时跳过。
func Read(t *testing.T) {
	t.Helper()
	path := os.Getenv("NPOWER_TEST_CONFIG")
	if path == "" {
		t.Skip("跳过集成测试：需设置 NPOWER_TEST_CONFIG 指向可用配置（依赖 MySQL/Redis/Tron 节点）")
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("跳过集成测试：未找到测试配置 %s", path)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("跳过集成测试：初始化依赖失败：%v", r)
		}
	}()
	config.Init(path) //mysql, redis, config file
}


func TestGetTransactionInfo(t *testing.T) {
	Read(t)
	status, err := GetTransactionInfo("233bcccd13ae4d7156b6b97da1532acf27b00cc69aac3c8cf44b5cc7f029b344")
	if err != nil {
		t.Fatal("get transaction info err", err)
	}

	t.Log("status", status)
}

func TestGetTrxBalance(t *testing.T) {
	Read(t)
	amount, err := GetTrxBalance("TR7DbxQJNTn1zErw7ihG46NuZJnB6E4KhY")
	if err != nil {
		t.Fatal("get trx balance err", err)
	}

	t.Log(amount)
}

func TestGetUsdtBalance(t *testing.T) {
	Read(t)
	amount, err := GetUsdtBalance("TR7DbxQJNTn1zErw7ihG46NuZJnB6E4KhY")
	if err != nil {
		t.Fatal("get trx balance err", err)
	}

	t.Log(amount)
}


func TestTransferUsdtNoTrxFee(t *testing.T) {
	Read(t)
	// ⚠ 2026-10-08 安全修复：这里原先也硬编码了资金池私钥明文（2026-09-30 的修复漏掉了本用例，
	//   只改了 TestTransferTrx）。与下方 TestTransferTrx 同口径：改环境变量读取，未设置即跳过。
	priv := os.Getenv("TRON_POOL_PRIVATE_KEY")
	if priv == "" {
		t.Skip("未设置 TRON_POOL_PRIVATE_KEY，跳过需要真实资金池私钥的集成用例（见 config/secrets.env.example）")
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
	Read(t)
	// ⚠ 2026-09-30 安全修复：这里原先**硬编码了资金池私钥明文**（与 config/*.yml 里同一把），
	//   等于把密钥又抄了一份进 Go 源码（连清理 yml 都挡不住）。
	//   改为从环境变量读取；没设就跳过该用例（它是连 TRON 真实网络的集成用例，本就需要显式 opt-in）。
	priv := os.Getenv("TRON_POOL_PRIVATE_KEY")
	if priv == "" {
		t.Skip("未设置 TRON_POOL_PRIVATE_KEY，跳过需要真实资金池私钥的集成用例（见 config/secrets.env.example）")
	}
	// 余额不足等链上情况仍会失败，但那属于环境问题，不再是"密钥写死在仓库里"
	txId, err := TransferTrxFee(priv, "TRkEj9XA7nha3nQ2zjHVb5cdKXks8wrgQS")
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
