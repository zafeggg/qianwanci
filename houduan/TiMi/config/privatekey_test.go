package config

import (
	"os"
	"testing"
)

// ============================== 私钥注入口径单测（2026-09-30 新增）==============================
//
// 为什么必须有这些用例：`scripts/export-secrets.ps1` 把 yml 里的明文私钥清空、改成
// `privateKeyEnv: "XXX"` 引用环境变量。这条链一旦有一环没接好（变量名不一致、env 没传到进程、
// 解析顺序反了），**资金池私钥就会变成空串** —— 提现/销毁/归集会在运行时才失败，
// 而这种失败在生产上意味着"钱出不去"。所以这里把解析优先级钉死。

// withEnv 临时设置环境变量并在用例结束后恢复
func withEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		old, had := os.LookupEnv(k)
		if err := os.Setenv(k, v); err != nil {
			t.Fatalf("设置环境变量 %s 失败: %v", k, err)
		}
		t.Cleanup(func() {
			if had {
				_ = os.Setenv(k, old)
			} else {
				_ = os.Unsetenv(k)
			}
		})
	}
}

func TestPoolPrivateKey_EnvWinsOverYml(t *testing.T) {
	withEnv(t, map[string]string{"NPOWER_TEST_POOL_KEY": "env-key"})
	if got := PoolPrivateKey("NPOWER_TEST_POOL_KEY", "yml-key"); got != "env-key" {
		t.Fatalf("环境变量应优先于 yml：期望 env-key，实际 %q", got)
	}
}

func TestPoolPrivateKey_FallsBackToYml(t *testing.T) {
	// privateKeyEnv 指向的变量不存在时，必须回落到 yml 值（存量部署仍能跑）
	_ = os.Unsetenv("NPOWER_TEST_MISSING_KEY")
	if got := PoolPrivateKey("NPOWER_TEST_MISSING_KEY", "yml-key"); got != "yml-key" {
		t.Fatalf("env 缺失应回落到 yml：期望 yml-key，实际 %q", got)
	}
	// privateKeyEnv 为空时同样用 yml
	if got := PoolPrivateKey("", "yml-key"); got != "yml-key" {
		t.Fatalf("未配置 privateKeyEnv 应使用 yml：期望 yml-key，实际 %q", got)
	}
}

func TestPoolPrivateKey_EmptyWhenNothingConfigured(t *testing.T) {
	// 两者都没有 → 空串（调用方据此报"未配置私钥"并跳过链上操作，绝不能编造默认私钥）
	_ = os.Unsetenv("NPOWER_TEST_EMPTY_KEY")
	if got := PoolPrivateKey("NPOWER_TEST_EMPTY_KEY", ""); got != "" {
		t.Fatalf("都未配置应返回空串，实际 %q", got)
	}
	if got := PoolPrivateKey("", ""); got != "" {
		t.Fatalf("都未配置应返回空串，实际 %q", got)
	}
}

func TestPoolPrivateKey_EnvValueIsTrimmed(t *testing.T) {
	// .env 文件/命令行注入很容易带出尾部空格或换行（Windows 的 \r\n），
	// 直接把带空白的私钥交给签名库会报"invalid private key"，且很难查。
	withEnv(t, map[string]string{"NPOWER_TEST_PAD_KEY": "  env-key \r\n"})
	if got := PoolPrivateKey("NPOWER_TEST_PAD_KEY", "yml-key"); got != "env-key" {
		t.Fatalf("环境变量值应去掉首尾空白：期望 env-key，实际 %q", got)
	}
}

// 各资金池 helper 必须走同一条解析口径（避免"某一路忘了接环境变量"）
func TestPoolHelpers_UsePrivateKeyEnv(t *testing.T) {
	withEnv(t, map[string]string{
		"NPOWER_TEST_KTO":  "kto-env",
		"NPOWER_TEST_TRON": "tron-env",
		"NPOWER_TEST_BURN": "burn-env",
	})
	old := EtcConfig
	t.Cleanup(func() { EtcConfig = old })

	EtcConfig = &YmlConfig{}
	EtcConfig.KtoPool.PrivateKeyEnv = "NPOWER_TEST_KTO"
	EtcConfig.TronPool.PrivateKeyEnv = "NPOWER_TEST_TRON"
	EtcConfig.Burn.PrivateKeyEnv = "NPOWER_TEST_BURN"

	if got := KtoPoolPrivateKey(); got != "kto-env" {
		t.Fatalf("KtoPoolPrivateKey 应读环境变量，实际 %q", got)
	}
	if got := TronPoolPrivateKey(); got != "tron-env" {
		t.Fatalf("TronPoolPrivateKey 应读环境变量，实际 %q", got)
	}
	if got := BurnPrivateKey(); got != "burn-env" {
		t.Fatalf("BurnPrivateKey 应读环境变量，实际 %q", got)
	}
}
