package handler

import (
	"strings"
	"testing"
)

// D/E 模块纯函数单测：不依赖 MySQL/Redis，可在任意环境运行。
// 运行：go test -run 'TestRedactBody|TestRespCodeOf|TestRateLimitAllowLocal' -p 1 ./http/handler

func TestRedactBody(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		mustHide  []string //结果中不得出现的明文
		mustKeep  []string //结果中应保留的明文
		expectRaw string   //整体替换（非 JSON 等）
	}{
		{
			name:     "常见敏感字段全部打码",
			in:       `{"account":"blx","pwd":"blxup2022city","password":"p@ss","privateKey":"0xdead","secret":"s","apiKey":"k","token":"t","sign":"sig","symbol":"FIBO"}`,
			mustHide: []string{"blxup2022city", "p@ss", "0xdead", `"s"`, `"k"`, `"t"`, `"sig"`},
			mustKeep: []string{"blx", "FIBO"},
		},
		{
			name:      "非 JSON 整体隐藏（宁可不留内容也不泄漏口令）",
			in:        `pwd=blxup2022city&account=blx`,
			expectRaw: "[非 JSON 或解析失败，内容已隐藏]",
		},
		{
			name:      "空体返回空串",
			in:        ``,
			expectRaw: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := redactBody([]byte(c.in))
			if c.expectRaw != "" || c.name == "空体返回空串" {
				if got != c.expectRaw {
					t.Fatalf("期望 %q，实际 %q", c.expectRaw, got)
				}
				return
			}
			for _, s := range c.mustHide {
				if strings.Contains(got, s) {
					t.Fatalf("敏感值 %q 未被脱敏，结果=%s", s, got)
				}
			}
			for _, s := range c.mustKeep {
				if !strings.Contains(got, s) {
					t.Fatalf("非敏感值 %q 被误删，结果=%s", s, got)
				}
			}
		})
	}
}

func TestRedactBodyTruncates(t *testing.T) {
	big := `{"symbol":"` + strings.Repeat("A", 9000) + `"}`
	got := redactBody([]byte(big))
	if !strings.HasSuffix(got, "...[已截断]") {
		t.Fatalf("超长请求体未标记截断，长度=%d", len(got))
	}
}

func TestRespCodeOf(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{`{"code":200,"msg":"success"}`, 200},
		{`{"code":600,"msg":"账号密码错误"}`, 600},
		{`{"code":0,"data":{}}`, 0},
		{`not json`, -1},
		{``, -1},
		{`{"msg":"no code"}`, -1},
	}
	for _, c := range cases {
		if got := respCodeOf([]byte(c.in)); got != c.want {
			t.Fatalf("respCodeOf(%q)=%d，期望 %d", c.in, got, c.want)
		}
	}
}

func TestRateLimitAllowLocal(t *testing.T) {
	//进程内限流器：第 limit 次放行，第 limit+1 次拒绝；不同 key 互不影响
	localRLMu.Lock()
	localRL = map[string]int{}
	localRLMu.Unlock()

	const limit = 3
	key := "npower:rl:test:127.0.0.1:1"
	for i := 1; i <= limit; i++ {
		ok, remaining := rateLimitAllowLocal(key, limit)
		if !ok {
			t.Fatalf("第 %d 次应放行", i)
		}
		if want := limit - i; remaining != want {
			t.Fatalf("第 %d 次 remaining=%d，期望 %d", i, remaining, want)
		}
	}
	if ok, remaining := rateLimitAllowLocal(key, limit); ok || remaining != 0 {
		t.Fatalf("第 %d 次应被拒绝且 remaining=0，实际 ok=%v remaining=%d", limit+1, ok, remaining)
	}
	if ok, _ := rateLimitAllowLocal("npower:rl:test:10.0.0.1:1", limit); !ok {
		t.Fatal("不同 IP 的计数应互相独立")
	}
}
