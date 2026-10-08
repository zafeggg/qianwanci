// Demo 演示网关（仅限本地演示）
//
// 作用：
//  1. 把浏览器发的明文请求翻译成 npower 要求的安全协议：
//     随机 AES 口令 -> RSA 加密进 "Sign" 头；请求体用 openssl(AES-256-CBC+salt) 加密。
//     （等价于原版 crypto-js 前端做的事，只是放在服务端做，方便"看效果"。）
//  2. 提供 /demo/bootstrap、/demo/register、/demo/faucet 等演示专用接口，
//     绕开"链上创建钱包 / 链上充值"环节（本机没有 :1527 链服务），
//     钱包地址离线生成、余额直接入账，让注册-资产-众筹-投票链路本地就能动起来。
//
// 仅用于本地开发演示，切勿用于生产。
package main

import (
	"bytes"
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core/impl"
	crowdcrypto "com.fibonacci.crowd/crypto"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Luzifer/go-openssl"
	"github.com/golang-jwt/jwt/v4"
	"gorm.io/gorm"
)

var (
	apiBase  = flag.String("api", "http://127.0.0.1:3000", "-api npower 主服务地址")
	addr     = flag.String("addr", ":3001", "-addr 演示网关监听地址")
	webDir   = flag.String("web", "cmd/demo/web", "-web 静态页面目录")
	ymlPath  = flag.String("f", "config/demo.local.yml", "-f 配置文件")
	rootCode = "DEMO8888" // 种子"演示官方"钱包的邀请码
	rootPwd  = "123456"
	rootName = "演示官方"
	oss      = openssl.New()
)

func main() {
	flag.Parse()
	config.Init(*ymlPath)

	//确保 config 表存在 params 列（npower 未先启动时 demo 也能正常回源读取版本/参数）
	if err := config.MysqlDBPool.AutoMigrate(&model.Config{}); err != nil {
		log.Printf("[demo] 自动迁移 config 表失败(忽略): %v", err)
	}
	impl.LoadNpowerParams() //迭代0：加载运行参数（演示网关同样遵循统一参数源）
	impl.WatchNpowerParamsReload()

	h := &handler{client: &http.Client{Timeout: 60 * time.Second}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", h.proxy)
	mux.HandleFunc("/demo/", h.demo)
	mux.Handle("/", http.FileServer(http.Dir(*webDir)))

	log.Printf("[demo] 网关已启动 %s -> %s (页面目录 %s)", *addr, *apiBase, *webDir)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

// ---------------- 通用 ----------------

type handler struct {
	client *http.Client
}

func writeJSON(w http.ResponseWriter, code int, obj interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(obj)
}

func demoOk(w http.ResponseWriter, data interface{}) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "data": data})
}

func demoErr(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": msg})
}

// 本地演示用：随机生成一串"看起来像链上地址"的字符串（无真实链上密钥）
const addrCharset = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

func fakeAddr(prefix string, n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	out := make([]byte, n)
	for i, c := range b {
		out[i] = addrCharset[int(c)%len(addrCharset)]
	}
	return prefix + string(out)
}

// 生成并绑定 JWT（与 npower 同一 SecretKey、同一 Redis 键 Login_{id}）
func mintToken(walletId uint, name string) (string, error) {
	claims := jwt.MapClaims{
		"walletId": walletId,
		"name":     name,
		"exp":      time.Now().Add(30 * 24 * time.Hour).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	t, err := tok.SignedString([]byte(config.EtcConfig.SecretKey))
	if err != nil {
		return "", err
	}
	return t, utils.LoginToken(walletId, t, 30*24*time.Hour)
}

// ---------------- /api/* 安全代理 ----------------

func makeSign() (pass string, signHeader string, err error) {
	kb := make([]byte, 32)
	if _, err = rand.Read(kb); err != nil {
		return
	}
	pass = hex.EncodeToString(kb)
	enc, err := crowdcrypto.RsaEncrypt([]byte(pass))
	if err != nil {
		return
	}
	signHeader = base64.StdEncoding.EncodeToString(enc)
	return
}

func (h *handler) proxy(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
	}

	pass, signHeader, err := makeSign()
	if err != nil {
		demoErr(w, "生成签名失败: "+err.Error())
		return
	}

	var rawBody []byte
	if len(body) > 0 {
		rawBody, err = oss.EncryptBytes(pass, body)
		if err != nil {
			demoErr(w, "加密请求体失败: "+err.Error())
			return
		}
	}

	upURL := strings.TrimRight(*apiBase, "/") + path
	upReq, err := http.NewRequest(r.Method, upURL, bytes.NewReader(rawBody))
	if err != nil {
		demoErr(w, "构建上游请求失败")
		return
	}
	upReq.URL.RawQuery = r.URL.RawQuery
	upReq.Header.Set("Sign", signHeader)
	if len(rawBody) > 0 {
		upReq.Header.Set("Content-Type", "application/json")
	}
	if tok := r.Header.Get("X-Demo-Token"); tok != "" {
		upReq.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := h.client.Do(upReq)
	if err != nil {
		demoErr(w, "npower 不可达: "+err.Error()+"（先启动 npower）")
		return
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)

	for k := range resp.Header {
		if strings.EqualFold(k, "Content-Length") {
			continue
		}
		w.Header().Set(k, resp.Header.Get(k))
	}
	if resp.Header.Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(out)
}

// ---------------- /demo/* 演示数据 ----------------

func (h *handler) demo(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/demo/bootstrap":
		h.bootstrap(w, r)
	case r.URL.Path == "/demo/register":
		h.register(w, r)
	case r.URL.Path == "/demo/faucet":
		h.faucet(w, r)
	case r.URL.Path == "/demo/status":
		h.status(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"ok": false, "error": "unknown demo endpoint"})
	}
}

// bootstrap：保证存在 种子官方钱包 + 项目 + 进行中的轮（幂等）
func (h *handler) bootstrap(w http.ResponseWriter, r *http.Request) {
	db := config.MysqlDBPool

	//兜底：/home/version 在 Redis 未命中时会回源 config 表，表空（或版本行为空）则返回
	//"读取配置失败/版本配置未找到"，前端状态点会一直显示"npower 未就绪"。这里幂等补一条演示版本配置。
	var cfg model.Config
	errCfg := db.Table(model.ConfigTable).Order("`id` asc").First(&cfg).Error
	switch {
	case errCfg == gorm.ErrRecordNotFound:
		cfg = model.Config{Version: "1.0.0-demo", Download: "http://127.0.0.1:3001"}
		if err := db.Table(model.ConfigTable).Create(&cfg).Error; err != nil {
			demoErr(w, "创建版本配置失败: "+err.Error())
			return
		}
	case errCfg != nil:
		log.Printf("[demo] 读取 config 表失败(忽略): %v", errCfg)
	default:
		//存在记录但版本为空（例如 ops 曾单独建行只写 params）时补上演示版本
		if cfg.Version == "" || cfg.Download == "" {
			if err := db.Table(model.ConfigTable).Where("`id` = ?", cfg.ID).
				Updates(map[string]interface{}{"version": "1.0.0-demo", "download": "http://127.0.0.1:3001"}).Error; err != nil {
				log.Printf("[demo] 补写版本配置失败(忽略): %v", err)
			}
		}
	}
	var root model.Wallet
	if err := db.Table(model.WalletTable).First(&root, "code = ?", rootCode).Error; err != nil {
		root = model.Wallet{
			Address:     fakeAddr("KtoDemo", 40),
			Private:     hex.EncodeToString([]byte(fakeAddr("", 32))),
			TronAddress: fakeAddr("T", 33),
			TronPrivate: hex.EncodeToString([]byte(fakeAddr("", 32))),
			Name:        rootName,
			Password:    utils.HashAndSalt([]byte(rootPwd)),
			Sign:        hex.EncodeToString([]byte(fakeAddr("", 32))),
			Level:       0,
			Active:      true,
			Admin:       true,
			Code:        rootCode,
		}
		if err := db.Table(model.WalletTable).Create(&root).Error; err != nil {
			demoErr(w, "创建种子钱包失败: "+err.Error())
			return
		}
	}

	var project model.Project
	if err := db.Table(model.ProjectTable).First(&project, "period = ? and symbol = ?", 1, "FIBO").Error; err != nil {
		project = model.Project{Period: 1, Symbol: "FIBO", Status: 1}
		if err := db.Table(model.ProjectTable).Create(&project).Error; err != nil {
			demoErr(w, "创建项目失败: "+err.Error())
			return
		}
	}

	var activeRound model.ProjectRound
	hasActive := db.Table(model.ProjectRoundTable).First(&activeRound, "project_id = ? and status = 1", project.ID).Error == nil
	if !hasActive {
		var history model.ProjectRound
		now := time.Now()
		histExist := db.Table(model.ProjectRoundTable).First(&history, "project_id = ?", project.ID).Error == nil
		if !histExist {
			//单位口径（2026-09-10 复盘修复）：round 的 target/min/max/current 与
			//VoteManager.Vote / /home 返回一致，均以「主单位(币)」存储（见 vote.go:52 amount×DEC 换算，
			//min/max 与 target 参与主单位比较）；此前 ×SymbolDictionary 按最小单位造数导致演示投票必失败。
			history = model.ProjectRound{
				ProjectId:   project.ID,
				Period:      1,
				Round:       1,
				TimeLimit:   3600,
				TargetVote:  2_000_000,
				MinVote:     10,
				MaxVote:     400_000,
				CurrentVote: 2_000_000,
				StartTime:   now.Add(-2 * time.Hour),
				EndTime:     now.Add(-1 * time.Hour),
				Count:       0,
				Symbol:      "FIBO",
				Success:     true,
				Status:      3,
			}
			if err := db.Table(model.ProjectRoundTable).Create(&history).Error; err != nil {
				demoErr(w, "创建历史轮失败: "+err.Error())
				return
			}
		}
		//下一个轮号 = 该项目已有轮号的 MAX+1。
		//原实现用「首行 history.Round + 1」：项目已有 >=2 行轮次时必然重复轮号
		//（2026-09-18 对账工具 recon 报出 project=1 round=2 命中重复，即为此因）。
		var maxRow struct{ MaxRound uint }
		if err := db.Table(model.ProjectRoundTable).
			Select("COALESCE(MAX(`round`), 0) AS max_round").
			Where("project_id = ?", project.ID).Scan(&maxRow).Error; err != nil {
			demoErr(w, "查询最大轮号失败: "+err.Error())
			return
		}
		nextRound := maxRow.MaxRound + 1
		if nextRound < 1 {
			nextRound = 1
		}
		activeRound = model.ProjectRound{
			ProjectId:   project.ID,
			Period:      1,
			Round:       nextRound,
			TimeLimit:   12 * 3600,
			TargetVote:  2_000_000 * 1.3, //每轮定增 30%（主单位）
			MinVote:     10,
			MaxVote:     400_000,
			CurrentVote: 0,
			StartTime:   now,
			EndTime:     now.Add(12 * time.Hour),
			Count:       0,
			Symbol:      "FIBO",
			Success:     false,
			Status:      1,
		}
		if err := db.Table(model.ProjectRoundTable).Create(&activeRound).Error; err != nil {
			demoErr(w, "创建进行中轮失败: "+err.Error())
			return
		}
	}

	var walletCnt, roundCnt, voteCnt int64
	db.Table(model.WalletTable).Count(&walletCnt)
	db.Table(model.ProjectRoundTable).Count(&roundCnt)
	db.Table(model.VoteTable).Count(&voteCnt)

	demoOk(w, map[string]interface{}{
		"rootCode":    root.Code,
		"rootAddress": root.Address,
		"rootName":    root.Name,
		"projectId":   project.ID,
		"roundId":     activeRound.ID,
		"counts":      map[string]int64{"wallet": walletCnt, "round": roundCnt, "vote": voteCnt},
	})
}

// register：离线注册演示钱包（本地生成地址 + 关系树 + JWT 登录态），返回与真实注册一致的结构
type registerReq struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

func (h *handler) register(w http.ResponseWriter, r *http.Request) {
	var q registerReq
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		demoErr(w, "参数错误: "+err.Error())
		return
	}
	if q.Name == "" || q.Password == "" {
		demoErr(w, "name / password 不能为空")
		return
	}
	var fromWallet model.Wallet
	if err := config.MysqlDBPool.Table(model.WalletTable).First(&fromWallet, "code = ?", q.Code).Error; err != nil {
		demoErr(w, "邀请码不存在: "+q.Code)
		return
	}

	wl := model.Wallet{
		Address:     fakeAddr("KtoDemo", 40),
		Private:     hex.EncodeToString([]byte(fakeAddr("", 32))),
		TronAddress: fakeAddr("T", 33),
		TronPrivate: hex.EncodeToString([]byte(fakeAddr("", 32))),
		Name:        q.Name,
		Password:    utils.HashAndSalt([]byte(q.Password)),
		Sign:        hex.EncodeToString([]byte(fakeAddr("", 32))),
		Level:       0,
		Active:      true,
		Admin:       false,
		Code:        utils.GetString(8),
	}

	if err := config.MysqlDBPool.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table(model.WalletTable).Create(&wl).Error; err != nil {
			return err
		}
		// 邀请关系：复制父节点全部祖先 + 父节点本身
		type treeRow struct {
			Ancestor uint
			Distance uint
		}
		var rows []treeRow
		if err := tx.Table(model.WalletTreeTable).Select("ancestor, distance").
			Where("descendant = ?", fromWallet.ID).Find(&rows).Error; err != nil {
			return err
		}
		for _, t := range rows {
			tr := model.WalletTree{Ancestor: t.Ancestor, Descendant: wl.ID, Distance: t.Distance + 1}
			if err := tx.Table(model.WalletTreeTable).Create(&tr).Error; err != nil {
				return err
			}
		}
		tr := model.WalletTree{Ancestor: fromWallet.ID, Descendant: wl.ID, Distance: 0}
		if err := tx.Table(model.WalletTreeTable).Create(&tr).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		demoErr(w, "创建钱包失败: "+err.Error())
		return
	}

	token, err := mintToken(wl.ID, wl.Name)
	if err != nil {
		demoErr(w, "生成登录态失败: "+err.Error())
		return
	}
	demoOk(w, map[string]interface{}{
		"token":       token,
		"name":        wl.Name,
		"address":     wl.Address,
		"usdtAddress": wl.TronAddress,
		"code":        wl.Code,
	})
}

// faucet：给指定钱包直接发测试币（本地演示用，绕过链上充值）
type faucetReq struct {
	Address     string  `json:"address"`
	Symbol      string  `json:"symbol"`
	AmountUnits float64 `json:"amountUnits"`
}

func (h *handler) faucet(w http.ResponseWriter, r *http.Request) {
	var q faucetReq
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		demoErr(w, "参数错误: "+err.Error())
		return
	}
	dec, ok := config.SymbolDictionary[q.Symbol]
	if !ok || q.Symbol == "" {
		demoErr(w, "不支持的币种: "+q.Symbol)
		return
	}
	var wallet model.Wallet
	if err := config.MysqlDBPool.Table(model.WalletTable).First(&wallet, "address = ?", q.Address).Error; err != nil {
		demoErr(w, "钱包不存在（请先注册/获取钱包地址）")
		return
	}
	minor := uint64(q.AmountUnits * dec)
	if minor <= 0 {
		demoErr(w, "数量必须大于 0")
		return
	}

	if _, err := impl.NewWalletPointSafe().AddPointAmount(config.MysqlDBPool, wallet.Address, q.Symbol, q.AmountUnits); err != nil {
		demoErr(w, "入账失败: "+err.Error())
		return
	}
	wtx := model.WalletTx{
		From:    "demo-faucet",
		To:      wallet.Address,
		Symbol:  q.Symbol,
		Amount:  minor,
		Desc:    enum.BlockInText,
		Hash:    "DEMO-" + fmt.Sprint(time.Now().UnixNano()),
		Success: 0,
		Type:    enum.BlockIn,
	}
	_ = config.MysqlDBPool.Table(model.WalletTxTable).Create(&wtx).Error

	demoOk(w, map[string]interface{}{"address": wallet.Address, "symbol": q.Symbol, "amountMinor": minor})
}

func (h *handler) status(w http.ResponseWriter, r *http.Request) {
	var walletCnt, roundCnt, voteCnt int64
	config.MysqlDBPool.Table(model.WalletTable).Count(&walletCnt)
	config.MysqlDBPool.Table(model.ProjectRoundTable).Count(&roundCnt)
	config.MysqlDBPool.Table(model.VoteTable).Count(&voteCnt)
	demoOk(w, map[string]interface{}{
		"npower": *apiBase,
		"counts": map[string]int64{"wallet": walletCnt, "round": roundCnt, "vote": voteCnt},
	})
}
