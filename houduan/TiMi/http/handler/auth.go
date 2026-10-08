package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core/impl"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils"

	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/gofiber/fiber/v2"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

/**
 * 千万次 - 钱包签名登录（EIP-191 personal_sign）
 *
 * 与既有 /api 适配层同属「纯 JSON」通道，沿用 {code:0,data} / {code:600,message} 信封
 * （apiOk / apiErr / apiErrCode），前端 requestReal 只认 code===0 与 message。
 *
 * 为什么不用 utils.JsonOk / JsonFailCode：二者信封是 {code:200,msg,...}，与前端既定的
 * code 0/600 + message 契约不一致（本组已有 9 个接口都走 apiOk/apiErr）。
 * 但「禁止 c.JSON()」的约束一律遵守：全部响应都经 encoding/json 预序列化后 c.Send，
 * 绕开 fiber v2.20.2 内置编码器在 Go 1.24+ 的 fatal fault（见 observability.go 顶部说明）。
 *
 * 凭据（Redis，沿用 npower: 前缀）：
 *   nonce  npower:auth:nonce:<address小写>        -> {nonce,issuedAt,chainId}
 *   session npower:auth:sess:<token>              -> walletId
 *   单会话  npower:auth:sess:wallet:<walletId>    -> token（新登录覆盖旧会话并删除旧 token 键）
 */

const (
	authNonceKeyPrefix      = "npower:auth:nonce:"
	authSessKeyPrefix       = "npower:auth:sess:"
	authSessWalletKeyPrefix = "npower:auth:sess:wallet:"

	// authWalletIdLocal 与既有 JWT 中间件写入的 Locals 键同名，后续写接口无需区分两种登录方式
	authWalletIdLocal = "walletId"

	// authMessageFirstLine 签名原文首行。按约定固定为 "TiMi Login"（不随 Auth.Brand 变化），
	// 避免品牌配置调整导致前后端签名原文漂移。
	authMessageFirstLine = "TiMi Login"
)

// authNonceRecord nonce 在 Redis 中的存档：登录时用它与当前配置重建签名原文
type authNonceRecord struct {
	Nonce    string `json:"nonce"`
	IssuedAt string `json:"issuedAt"`
	ChainId  int64  `json:"chainId"`
}

type authLoginReq struct {
	Address    string `json:"address"`
	Signature  string `json:"signature"`
	InviteCode string `json:"inviteCode"`
	Name       string `json:"name"`
}

type authNonceData struct {
	Address  string `json:"address"`
	Nonce    string `json:"nonce"`
	IssuedAt string `json:"issuedAt"`
	ChainId  int64  `json:"chainId"`
	Brand    string `json:"brand"`
	Message  string `json:"message"` //服务端拼好的完整签名原文，前端直接对它调 personal_sign
}

type authLoginData struct {
	Token            string `json:"token"`
	Address          string `json:"address"`
	WalletId         uint   `json:"walletId"`
	InviteCode       string `json:"inviteCode"`
	Registered       bool   `json:"registered"`
	ExpiresInSeconds int64  `json:"expiresInSeconds"`
}

type authMeData struct {
	WalletId         uint   `json:"walletId"`
	Address          string `json:"address"`
	EvmAddress       string `json:"evmAddress"`
	Name             string `json:"name"`
	Level            uint   `json:"level"`
	InviteCode       string `json:"inviteCode"`
	ExpiresInSeconds int64  `json:"expiresInSeconds"`
}

/* ---------- 配置读取（config 可能未初始化，全部走兜底） ---------- */

func authConfig() *config.YmlConfig {
	return config.EtcConfig
}

func authNonceTTL() time.Duration {
	if c := authConfig(); c != nil && c.Auth.NonceTTLSeconds > 0 {
		return time.Duration(c.Auth.NonceTTLSeconds) * time.Second
	}
	return config.DefaultAuthNonceTTLSeconds * time.Second
}

func authSessionTTL() time.Duration {
	if c := authConfig(); c != nil && c.Auth.SessionTTLSeconds > 0 {
		return time.Duration(c.Auth.SessionTTLSeconds) * time.Second
	}
	return config.DefaultAuthSessionTTLSeconds * time.Second
}

func authBrand() string {
	if c := authConfig(); c != nil && c.Auth.Brand != "" {
		return c.Auth.Brand
	}
	return config.DefaultAuthBrand
}

func authEnforceSession() bool {
	return authConfig() != nil && authConfig().Auth.EnforceSession
}

// authAllowUnsignedWrite 是否允许无会话写请求沿用 body 地址（**仅联调**）。
// Mode=prod 时无条件返回 false —— 生产永远要求钱包签名会话，误配也打不开。
func authAllowUnsignedWrite() bool {
	c := authConfig()
	if c == nil || !c.Auth.AllowUnsignedWrite {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(c.Mode), "prod") {
		return false
	}
	return true
}

func authRequireInviteCode() bool {
	return authConfig() != nil && authConfig().Auth.RequireInviteCode
}

/* ---------- 签名原文 / 验签 ---------- */

// authMessage 重建 EIP-191 签名原文：6 行用 \n 连接，无末尾换行。
// 必须与 GET /api/auth/nonce 返回的 message 完全一致，前端不做任何拼接。
func authMessage(brand, address string, chainId int64, nonce, issuedAt string) string {
	return strings.Join([]string{
		authMessageFirstLine,
		"Brand: " + brand,
		"Address: " + address,
		"ChainId: " + strconv.FormatInt(chainId, 10),
		"Nonce: " + nonce,
		"IssuedAt: " + issuedAt,
	}, "\n")
}

// authRecoverAddress 从 65 字节签名恢复签名者地址（小写）。
// 哈希口径：keccak256("\x19Ethereum Signed Message:\n" + len(msg) + msg)。
func authRecoverAddress(message, signature string) (string, error) {
	sigHex := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(signature), "0x"), "0X")
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return "", errors.New("签名格式错误")
	}
	if len(sig) != 65 {
		return "", errors.New("签名长度错误")
	}
	//部分钱包（含 ethers v5 signMessage）返回 27/28，go-ethereum 需要 0/1
	if sig[64] == 27 || sig[64] == 28 {
		sig[64] -= 27
	}
	if sig[64] != 0 && sig[64] != 1 {
		return "", errors.New("签名 V 值非法")
	}

	hash := ethcrypto.Keccak256([]byte("\x19Ethereum Signed Message:\n" + strconv.Itoa(len(message)) + message))
	pub, err := ethcrypto.SigToPub(hash, sig)
	if err != nil {
		return "", err
	}
	return strings.ToLower(ethcrypto.PubkeyToAddress(*pub).Hex()), nil
}

/* ---------- 通用小工具 ---------- */

// authRandomHex 生成 n 字节随机数的 hex（n=16 → 32 位 nonce；n=32 → 64 位会话票）
func authRandomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// authNormalizeAddress 校验并统一小写（EVM 地址大小写不敏感，Redis 键与 DB 查询都依赖小写口径）
func authNormalizeAddress(address string) (string, bool) {
	address = strings.TrimSpace(address)
	if !common.IsHexAddress(address) {
		return "", false
	}
	return strings.ToLower(address), true
}

func authNonceKey(address string) string {
	return authNonceKeyPrefix + address
}

func authSessKey(token string) string {
	return authSessKeyPrefix + token
}

func authSessWalletKey(walletId uint) string {
	return authSessWalletKeyPrefix + strconv.FormatUint(uint64(walletId), 10)
}

// authBearerToken 解析 Authorization: Bearer <token>
func authBearerToken(c *fiber.Ctx) string {
	raw := strings.TrimSpace(c.Get(fiber.HeaderAuthorization))
	if raw == "" {
		return ""
	}
	parts := strings.Fields(raw)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

// authLoadSession 解析 Bearer 会话票：返回 walletId 与剩余有效期（秒级）。
// 无效/缺失一律 (0,false)，由调用方决定放行还是 401。
func authLoadSession(c *fiber.Ctx) (walletId uint, token string, remain time.Duration, ok bool) {
	if config.RedisClient == nil {
		return 0, "", 0, false
	}
	token = authBearerToken(c)
	if token == "" {
		return 0, "", 0, false
	}

	ctx := context.Background()
	raw, err := config.RedisClient.Get(ctx, authSessKey(token)).Result()
	if err != nil || strings.TrimSpace(raw) == "" {
		return 0, "", 0, false
	}
	id64, perr := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if perr != nil || id64 == 0 {
		return 0, "", 0, false
	}

	remain = authSessionTTL()
	if d, terr := config.RedisClient.TTL(ctx, authSessKey(token)).Result(); terr == nil && d > 0 {
		remain = d
	}
	return uint(id64), token, remain, true
}

// currentWalletId 会话 walletId 读取入口：优先取中间件写入的 Locals，缺失时回退实时解析会话票
// （/api/auth/* 路由注册在 SessionMiddleware 之后仍可直接使用）。
func currentWalletId(c *fiber.Ctx) (uint, bool) {
	if v := c.Locals(authWalletIdLocal); v != nil {
		if id, ok := v.(uint); ok && id > 0 {
			return id, true
		}
	}
	walletId, _, _, ok := authLoadSession(c)
	if !ok {
		return 0, false
	}
	return walletId, true
}

// sessionAddress 有有效会话时返回该会话钱包的地址（原样来自 DB），否则返回空串。
//
// 为什么需要它：/api 写接口（参投/提现/领取）一直以 **body 里的 address** 作为用户标识，
// 而 body 是客户端自述、可以随便改 —— 带着自己的会话票、把 body 换成别人的地址，
// 就能操作别人的钱包（提现、领取）。有了这个函数，写接口可以改成「会话身份优先」。
func sessionAddress(c *fiber.Ctx) string {
	walletId, ok := currentWalletId(c)
	if !ok || config.MysqlDBPool == nil {
		return ""
	}
	var addr string
	if err := config.MysqlDBPool.Table(model.WalletTable).Select("address").
		Where("`id` = ?", walletId).Scan(&addr).Error; err != nil {
		return ""
	}
	return strings.TrimSpace(addr)
}

// resolveActingAddress 解析写请求的**实际操作地址**：
//   - 带有效会话票：以**会话地址**为准；若 body 也给了地址且与会话不一致 → 直接拒绝
//   - 无会话（Auth.EnforceSession=false 的兼容路径，含存量前端与脚本直连）：沿用 body
//
// 返回错误表示「身份与请求不符」，调用方必须拒绝，不得退回用 body。
//
// ⚠ 资金接口请改用 resolveActingAddressStrict：本函数在无会话时信任 body 地址，
// 而 /api 的唯一凭据 X-API-Key 是随前端包公开的，等于允许以任意地址操作钱包。
func resolveActingAddress(c *fiber.Ctx, bodyAddress string) (string, error) {
	body := strings.TrimSpace(bodyAddress)
	sess := sessionAddress(c)
	if sess == "" {
		return body, nil
	}
	if body != "" && !strings.EqualFold(body, sess) {
		return "", errors.New("请求地址与登录会话不一致")
	}
	return sess, nil
}

// resolveActingAddressStrict 资金类接口的强制身份解析：**必须**带有效会话票。
//
// 背景（2026-09-30 实测复现）：/api 仅凭 X-API-Key 放行，而这个 key 是编译进前端 JS 的
// （VITE_API_KEY），任何用户都能读到；叠加 Auth.EnforceSession 默认 false 时
// resolveActingAddress 会回落到 body 地址 → 未登录调用者可用任意地址
// 参投/提现/领取（实测 POST /api/participate 带任意 body 地址直接进入业务逻辑）。
//
// 会话票由 EIP-191 钱包签名换取（GET /api/auth/nonce → POST /api/auth/login），无法伪造；
// 脚本调用请先登录再带 Authorization: Bearer <token>。
func resolveActingAddressStrict(c *fiber.Ctx, bodyAddress string) (string, error) {
	sess := sessionAddress(c)
	if sess == "" {
		//联调例外：私链脚本用 SQL 造的无私钥账号无法签名登录（生产 Mode=prod 时此分支恒不生效）
		if authAllowUnsignedWrite() {
			body := strings.TrimSpace(bodyAddress)
			if body != "" {
				log.WithFields(log.Fields{"path": c.Path(), "address": body}).
					Warnln("[auth] 联调模式：无会话写请求沿用 body 地址（Auth.AllowUnsignedWrite=true，生产禁止）")
				return body, nil
			}
		}
		return "", errors.New("未登录：请先完成钱包签名登录（/api/auth/nonce → /api/auth/login）")
	}
	body := strings.TrimSpace(bodyAddress)
	if body != "" && !strings.EqualFold(body, sess) {
		return "", errors.New("请求地址与登录会话不一致")
	}
	return sess, nil
}

// resolveReadAddress 读接口（资产/用户/挖矿）的地址解析。
//
// 口径（2026-09-30 收紧）：
//   - **带了有效会话**：只能读自己的地址；body/query 给了别人的地址 → 直接拒绝。
//     否则"用自己会话读他人资产"就是白送的越权（原先只在写接口做了这个校验）。
//   - 无会话：沿用 query 里的地址（DApp 未签名前的可读性 + 脚本/巡检直连），
//     若要彻底关闭"匿名按地址读"，请开 Auth.EnforceSession（owner 决策，见部署清单 §12）。
func resolveReadAddress(c *fiber.Ctx, queryAddress string) (string, error) {
	addr := strings.TrimSpace(queryAddress)
	sess := sessionAddress(c)
	if sess == "" {
		return addr, nil
	}
	if addr != "" && !strings.EqualFold(addr, sess) {
		return "", errors.New("请求地址与登录会话不一致")
	}
	return sess, nil
}

// apiErrCode 带业务码的错误响应（与 apiErr 同风格，仅 code 可指定；供 401 等场景使用）
func apiErrCode(code int, msg string) []byte {
	bs, _ := json.Marshal(apiResponse{Code: code, Message: msg})
	return bs
}

// authFail401 统一的未登录响应：HTTP 401 + {code:401,message}
func authFail401(c *fiber.Ctx) error {
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	return c.Status(fiber.StatusUnauthorized).Send(apiErrCode(401, "未登录或会话已过期"))
}

// authIsWriteMethod 是否需要会话的写方法
func authIsWriteMethod(method string) bool {
	switch method {
	case fiber.MethodPost, fiber.MethodPut, fiber.MethodPatch, fiber.MethodDelete:
		return true
	}
	return false
}

/* ---------- 会话中间件 ---------- */

// apiReadNeedsSession 判断某个 **GET** 路径是否属于"按地址查用户数据"的读接口。
//
// 为什么要分类（2026-09-30）：`EnforceSession=true` 起初被实现成"拦下**所有**读请求"，
// 结果连 `/api/prices`（行情）、`/api/rounds`（轮次）、`/api/rules`（规则）这些**不含任何用户隐私**
// 的公共接口也 401 —— 首屏（未连接钱包时）直接白屏，而它们本来就不需要身份。
// 现在的口径：只有"带 address 参数、返回某个钱包的资产/团队/算力"的接口才要求会话票；
// 公共只读接口始终开放（这正是它们存在的意义：让未连接钱包的用户也能看到玩法与行情）。
func apiReadNeedsSession(path string) bool {
	switch path {
	case "/api/assets", "/api/user", "/api/miner":
		return true
	default:
		return false
	}
}

// SessionMiddleware /api 会话中间件（挂在 /api 组、且必须注册在路由之前）：
//   - 会话有效：walletId 写入 Locals("walletId")，后续写接口可直接读取；
//   - 会话缺失/无效：**写方法一律 401**（2026-09-30 收紧：写接口必须能证明身份，
//     因为 X-API-Key 随前端包公开，仅凭它无法区分用户）；
//   - 读方法：`Auth.EnforceSession=true` 时拦截**用户数据类读接口**（/api/assets|user|miner，
//     它们按 address 返回某个钱包的隐私数据），公共只读接口（prices/rounds/rules/deposit/info）始终放行；
//     `=false` 时读接口一律放行（私链脚本按地址直连的口径）。
//   - /api/auth/* 自身就是登录入口，永不拦截。
func SessionMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		walletId, _, _, ok := authLoadSession(c)
		if ok {
			c.Locals(authWalletIdLocal, walletId)
			return c.Next()
		}
		if strings.HasPrefix(c.Path(), "/api/auth/") {
			return c.Next()
		}
		if authIsWriteMethod(c.Method()) && authAllowUnsignedWrite() {
			//联调模式：放行无会话写请求，由 resolveActingAddressStrict 沿用 body 地址（Mode=prod 时不生效）
			return c.Next()
		}
		if authIsWriteMethod(c.Method()) {
			return authFail401(c)
		}
		if authEnforceSession() && apiReadNeedsSession(c.Path()) {
			return authFail401(c)
		}
		return c.Next()
	}
}

/* ---------- 1. GET /api/auth/nonce ---------- */

// AuthNonce 下发一次性 nonce 与完整签名原文（覆盖写入：重新获取即作废旧 nonce）
func (a *ApiAdapter) AuthNonce(c *fiber.Ctx) error {
	address, valid := authNormalizeAddress(c.Query("address"))
	if !valid {
		return c.Send(apiErr("地址格式错误"))
	}
	if config.RedisClient == nil {
		return c.Send(apiErr("服务暂不可用，请稍后重试"))
	}

	nonce, err := authRandomHex(16) //32 位 hex
	if err != nil {
		log.WithFields(log.Fields{"method": "api.authNonce", "err": err}).Errorln("生成 nonce 失败")
		return c.Send(apiErr("生成 nonce 失败"))
	}
	issuedAt := time.Now().UTC().Format(time.RFC3339)
	chainId := int64(0)
	if cfg := authConfig(); cfg != nil {
		chainId = cfg.Auth.ChainId
	}
	brand := authBrand()

	rec := authNonceRecord{Nonce: nonce, IssuedAt: issuedAt, ChainId: chainId}
	bs, merr := json.Marshal(rec)
	if merr != nil {
		return c.Send(apiErr("生成 nonce 失败"))
	}
	if serr := config.RedisClient.Set(c.Context(), authNonceKey(address), bs, authNonceTTL()).Err(); serr != nil {
		log.WithFields(log.Fields{"method": "api.authNonce", "err": serr}).Errorln("写入 nonce 失败")
		return c.Send(apiErr("服务暂不可用，请稍后重试"))
	}

	return c.Send(apiOk(authNonceData{
		Address:  address,
		Nonce:    nonce,
		IssuedAt: issuedAt,
		ChainId:  chainId,
		Brand:    brand,
		Message:  authMessage(brand, address, chainId, nonce, issuedAt),
	}))
}

/* ---------- 2. POST /api/auth/login ---------- */

// AuthLogin 校验签名并登录；地址不存在则自动注册（可选邀请码）
func (a *ApiAdapter) AuthLogin(c *fiber.Ctx) error {
	var req authLoginReq
	if err := c.BodyParser(&req); err != nil {
		return c.Send(apiErr("参数错误"))
	}
	address, valid := authNormalizeAddress(req.Address)
	if !valid {
		return c.Send(apiErr("地址格式错误"))
	}
	if strings.TrimSpace(req.Signature) == "" {
		return c.Send(apiErr("签名不能为空"))
	}
	if config.RedisClient == nil {
		return c.Send(apiErr("服务暂不可用，请稍后重试"))
	}

	ctx := context.Background()
	raw, err := config.RedisClient.Get(ctx, authNonceKey(address)).Result()
	if err != nil || raw == "" {
		//nonce 不存在/已过期/已被本次或他人消费：一律要求重新获取（不区分原因，避免探测）
		return c.Send(apiErr("nonce 已过期，请重新获取"))
	}
	var rec authNonceRecord
	if jerr := json.Unmarshal([]byte(raw), &rec); jerr != nil || rec.Nonce == "" {
		return c.Send(apiErr("nonce 已过期，请重新获取"))
	}

	//服务端重建原文验签：chainId 用 nonce 存档值（避免下发后改配置导致验签漂移）
	message := authMessage(authBrand(), address, rec.ChainId, rec.Nonce, rec.IssuedAt)
	recovered, verr := authRecoverAddress(message, req.Signature)
	if verr != nil || recovered != address {
		log.WithFields(log.Fields{"method": "api.authLogin", "address": address, "err": verr}).Warnln("签名校验失败")
		return c.Send(apiErr("签名校验失败"))
	}
	//一次性：验签通过即刻删除，同一 nonce 的重放/复用一律拒绝
	config.RedisClient.Del(ctx, authNonceKey(address))

	//按 evm_address 查钱包；不存在则注册。
	//兼容取舍：注册时把 Address 与 EvmAddress 都写成该 EVM 地址（小写），
	//使既有按 wallet.address 查询的 /api 接口（assets/user/participate/withdraw/miner）继续可用。
	var wallet model.Wallet
	registered := false
	switch ferr := a.DB.Table(model.WalletTable).First(&wallet, "evm_address = ?", address).Error; ferr {
	case nil:
	case gorm.ErrRecordNotFound:
		created, cerr := a.authRegister(address, req.InviteCode, req.Name)
		if cerr != nil {
			return c.Send(apiErr(cerr.Error()))
		}
		wallet = created
		registered = true
	default:
		log.WithFields(log.Fields{"method": "api.authLogin", "err": ferr}).Errorln("查询钱包失败")
		return c.Send(apiErr("登录失败，请稍后重试"))
	}

	token, ttl, terr := a.authIssueSession(wallet.ID)
	if terr != nil {
		return c.Send(apiErr("登录失败，请稍后重试"))
	}
	log.WithFields(log.Fields{
		"method": "api.authLogin", "walletId": wallet.ID, "address": address, "registered": registered,
	}).Infoln("签名登录成功")

	return c.Send(apiOk(authLoginData{
		Token:            token,
		Address:          wallet.Address,
		WalletId:         wallet.ID,
		InviteCode:       wallet.Code,
		Registered:       registered,
		ExpiresInSeconds: int64(ttl.Seconds()),
	}))
}

// authRegister 签名登录首登注册：建钱包 + 可选邀请树边（同一事务）
func (a *ApiAdapter) authRegister(address, inviteCode, name string) (model.Wallet, error) {
	inviteCode = strings.TrimSpace(inviteCode)
	var parent *model.Wallet
	if inviteCode != "" {
		var p model.Wallet
		switch err := a.DB.Table(model.WalletTable).First(&p, "code = ?", inviteCode).Error; err {
		case nil:
			parent = &p
		case gorm.ErrRecordNotFound:
			return model.Wallet{}, errors.New("邀请人邀请码不存在")
		default:
			return model.Wallet{}, errors.New("查询邀请人失败，请稍后重试")
		}
	}
	if authRequireInviteCode() && parent == nil {
		return model.Wallet{}, errors.New("请填写邀请码")
	}

	code, err := a.authUniqueInviteCode()
	if err != nil {
		return model.Wallet{}, err
	}

	//EvmAddress 用局部变量取址：Address / EvmAddress 双写同一小写 EVM 地址（见 AuthLogin 注释）
	evm := address
	wallet := model.Wallet{
		Address:    address,
		EvmAddress: &evm,
		Name:       strings.TrimSpace(name),
		Code:       code,
	}

	//复用既有 WalletManager.InviteWithTx：闭包表口径为 distance=0 直推、写 (父,子,0)、不写自环
	walletMgr := impl.NewWalletManager()
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		if cerr := tx.Table(model.WalletTable).Create(&wallet).Error; cerr != nil {
			return cerr
		}
		if parent == nil {
			return nil
		}
		return walletMgr.InviteWithTx(tx, parent.ID, wallet.ID)
	})
	if err != nil {
		log.WithFields(log.Fields{"method": "api.authRegister", "address": address, "err": err}).Errorln("注册钱包失败")
		return model.Wallet{}, errors.New("注册失败，请稍后重试")
	}
	return wallet, nil
}

// authUniqueInviteCode 生成唯一邀请码（与 Registration 同口径：utils.GetString 的 8 位大写字母数字）。
// 预查 5 次尽量避开 uk_wallet_code 冲突；极端并发下由唯一索引兜底报错。
func (a *ApiAdapter) authUniqueInviteCode() (string, error) {
	for i := 0; i < 5; i++ {
		code := utils.GetString(8)
		var cnt int64
		if err := a.DB.Table(model.WalletTable).Where("code = ?", code).Count(&cnt).Error; err != nil {
			return "", errors.New("注册失败，请稍后重试")
		}
		if cnt == 0 {
			return code, nil
		}
	}
	return "", errors.New("邀请码生成失败，请重试")
}

// authIssueSession 发会话票：写 sess:<token> 与 sess:wallet:<walletId> 两键，
// 并把旧会话的 token 键删除（同一钱包只允许一个有效会话，旧设备随即失效）。
func (a *ApiAdapter) authIssueSession(walletId uint) (string, time.Duration, error) {
	token, err := authRandomHex(32) //64 位 hex
	if err != nil {
		log.WithFields(log.Fields{"method": "api.authIssueSession", "err": err}).Errorln("生成会话票失败")
		return "", 0, err
	}
	ttl := authSessionTTL()
	ctx := context.Background()

	if old, gerr := config.RedisClient.Get(ctx, authSessWalletKey(walletId)).Result(); gerr == nil && old != "" {
		config.RedisClient.Del(ctx, authSessKey(old))
	}
	if serr := config.RedisClient.Set(ctx, authSessKey(token), strconv.FormatUint(uint64(walletId), 10), ttl).Err(); serr != nil {
		log.WithFields(log.Fields{"method": "api.authIssueSession", "err": serr}).Errorln("写入会话失败")
		return "", 0, serr
	}
	if serr := config.RedisClient.Set(ctx, authSessWalletKey(walletId), token, ttl).Err(); serr != nil {
		//反查键写失败会让「新登录覆盖旧会话」失效（旧 token 无法再被顶掉），回滚本次 token 键
		config.RedisClient.Del(ctx, authSessKey(token))
		log.WithFields(log.Fields{"method": "api.authIssueSession", "err": serr}).Errorln("写入单会话索引失败")
		return "", 0, serr
	}
	return token, ttl, nil
}

/* ---------- 3. GET /api/auth/me ---------- */

// AuthMe 当前会话对应的钱包信息（无有效会话 → HTTP 401）
func (a *ApiAdapter) AuthMe(c *fiber.Ctx) error {
	walletId, _, remain, ok := authLoadSession(c)
	if !ok {
		return authFail401(c)
	}
	var wallet model.Wallet
	if err := a.DB.Table(model.WalletTable).First(&wallet, "id = ?", walletId).Error; err != nil {
		//会话有效但钱包已不存在：按未登录处理，避免前端拿到半截用户信息
		return authFail401(c)
	}

	evmAddress := ""
	if wallet.EvmAddress != nil {
		evmAddress = *wallet.EvmAddress
	}
	if evmAddress == "" && common.IsHexAddress(wallet.Address) {
		//历史数据兜底：早期 EVM 钱包只填了 address 列（evm_address 为 NULL）
		evmAddress = strings.ToLower(wallet.Address)
	}

	return c.Send(apiOk(authMeData{
		WalletId:         wallet.ID,
		Address:          wallet.Address,
		EvmAddress:       evmAddress,
		Name:             wallet.Name,
		Level:            wallet.Level,
		InviteCode:       wallet.Code,
		ExpiresInSeconds: int64(remain.Seconds()),
	}))
}
