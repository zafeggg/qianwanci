package handler

/**
 * 千万次 DApp - /api 适配层（联调用纯 JSON 接口）
 *
 * 背景：前端（dapp-vue）契约是 REST 明文 JSON（src/api/index.js 的 9 个接口），
 *       后端 npower 原生路由（/home /wallet /self）走 RSA+AES 签名 + JWT + 验证码。
 *       本文件在后端直接挂 /api/* 纯 JSON 路由，内部复用现有 Manager/DB 逻辑，
 *       使前端零协议改造即可吃真实数据。响应封装 {code:0,message,data} 与前端约定一致。
 *
 * 注意（2026-09-30 更新，已与实现对齐）：
 *  1. 鉴权：/api 组挂 ApiKeyMiddleware（yml apiAuthKey，常量时间比较）+ SessionMiddleware；
 *     **三个写接口（参投/提现/领取）强制要求钱包签名会话**（resolveActingAddressStrict）——
 *     因为 X-API-Key 会随浏览器包一起下发（VITE_API_KEY 编译进 JS），对用户不是秘密，
 *     仅凭它回落 body 地址等于"任何用户都能操作任意地址的钱包"。
 *  2. 模式：Mode=prod 时 apiAuthKey 为空将**拒绝启动**（见 cmd/main.go 的配置体检），不再静默放行。
 *  3. 链上操作（出金广播/手续费销毁/结算轮）由 cmd/evmwatch、cmd/burn、cmd/round 承担；
 *     本适配层只做账本与广播编排，Withdraw.Mode=ledger 时为本地记账语义。
 *
 * 数据口径：玩法规则对齐前端 constants/config.js（TiMi 基准）：
 *   - 结算偏移 settleOffset=3：投 R 轮，第 R+3 轮结束时强制结算本息（静态 13%）
 *   - 轮次 phase：进行中 open / 成功 success / 失败 failed（筹满即成功语义，供展示）
 */

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core"
	"com.fibonacci.crowd/core/impl"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils/evm"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/gofiber/fiber/v2"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
)

/* ---------- /api 鉴权中间件 ---------- */

// ApiKeyMiddleware /api 组鉴权：yml apiAuthKey 非空时要求请求头 X-API-Key 常量时间匹配；
// 为空时：dev/test 放行（本地联调），**Mode=prod 一律拒绝**（fail-closed）。
//
// ⚠ 该 key 会随浏览器包下发（VITE_API_KEY 编译进 JS），因此它只用于挡住扫描器/爬虫，
// 不能作为用户身份凭据；资金与身份一律靠会话票（见 resolveActingAddressStrict）。
func ApiKeyMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		key := config.EtcConfig.ApiAuthKey
		if key == "" {
			if isProdMode() {
				log.WithFields(log.Fields{"path": c.Path()}).Errorln("[api] Mode=prod 但 apiAuthKey 为空，拒绝全部 /api 请求（请配置后重启）")
				return c.Send(apiErr("服务未完成安全配置"))
			}
			return c.Next()
		}
		got := c.Get("X-API-Key")
		if subtle.ConstantTimeCompare([]byte(got), []byte(key)) != 1 {
			return c.Send(apiErr("未授权"))
		}
		return c.Next()
	}
}

// isProdMode 是否生产模式（yml Mode=prod）
func isProdMode() bool {
	return config.EtcConfig != nil && strings.EqualFold(strings.TrimSpace(config.EtcConfig.Mode), "prod")
}

/* ---------- 规则常量（与前端 config.js 保持一致） ---------- */
const (
	// apiSettleOffset 结算偏移：投第 N 轮在第 N+3 轮 End 时结算（与后端 MinRound=3 同口径）。
	// 规则来源：千万次 TiMi(1).docx 未明确结算轮次，按 owner 裁定沿用 N次方"三进一出"
	//（N次方-技术方案.md 1.2：第 N 轮投入在第 N+3 轮结束时统一结算）。前端 settleOffset 同步为 3。
	apiSettleOffset   = 3
	apiStaticRate     = 0.13 // 静态收益 13%
	apiFeeRate        = 0.03 // 提币手续费 3%
	apiMaxTreeDepth   = 3    // underTree 展示深度上限（直推=1，三代=3）
	apiFloatPrecision = 4    // 金额展示精度（截断）
)

// apiFallbackPrices 行情源不可用时的默认展示价（避免前端显示 0）
var apiFallbackPrices = map[string]float64{"FIBO": 0.5, "USDT": 1.0, "TM": 3.4}

/* ---------- 响应封装（code 0 成功，对齐前端 requestReal 约定） ---------- */
type apiResponse struct {
	Code    int         `json:"code"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data"`
}

func apiOk(data interface{}) []byte {
	bs, _ := json.Marshal(apiResponse{Code: 0, Data: data})
	return bs
}

func apiErr(msg string) []byte {
	bs, _ := json.Marshal(apiResponse{Code: 600, Message: msg})
	return bs
}

func apiFloat(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	// 截断 float 精度尾巴（0.30000000000000004 → 0.3）
	return math.Round(v*math.Pow10(apiFloatPrecision)) / math.Pow10(apiFloatPrecision)
}

/* ---------- 适配器主体 ---------- */
type ApiAdapter struct {
	DB           *gorm.DB
	VoteManager  core.Voting       // 复用后端投入记账（纯 DB，不上链）
	HashPowerMgr core.HashPowering // 复用算力矿机（纯 DB）
}

func NewApiAdapter() *ApiAdapter {
	return &ApiAdapter{
		DB:           config.MysqlDBPool,
		VoteManager:  impl.NewVoteManager(),
		HashPowerMgr: impl.NewHashPowerManager(),
	}
}

/* ---------- 请求参数 ---------- */
type apiParticipateReq struct {
	Address string  `json:"address"`
	RoundId uint    `json:"roundId"`
	Amount  float64 `json:"amount"`
	Asset   string  `json:"asset"`
}

type apiWithdrawReq struct {
	Address  string  `json:"address"`
	Asset    string  `json:"asset"`
	Amount   float64 `json:"amount"`
	FeeToken string  `json:"feeToken"`
}

type apiClaimReq struct {
	Address string `json:"address"`
}

/* ---------- 通用查询小工具 ---------- */

// walletBalance 查询地址在某币种的展示余额（最小单位 → 浮点）
func (a *ApiAdapter) walletBalance(address, symbol string) float64 {
	var p model.WalletPoint
	if err := a.DB.Table(model.WalletPointTable).First(&p, "address = ? AND symbol = ?", address, symbol).Error; err != nil {
		return 0
	}
	return apiFloat(float64(p.Amount) / symbolDictValue(symbol))
}

// setWalletPointAmount 事务内更新某余额记录（amount 为最小单位新值）
func setWalletPointAmount(tx *gorm.DB, id uint, amount uint64) error {
	return tx.Table(model.WalletPointTable).Where("id = ?", id).Update("amount", amount).Error
}

// sumReward 累加 address 作为受益人的指定收益字段
func (a *ApiAdapter) sumReward(address, column string) float64 {
	var v float64
	a.DB.Table(model.VoteRewardTable).Where("address = ?", address).
		Select("IFNULL(SUM(" + column + "),0)").Scan(&v)
	return apiFloat(v)
}

// addWalletPoint 事务内入账（记录不存在则新建），返回更新后的最小单位余额
func addWalletPoint(tx *gorm.DB, address, symbol string, amount uint64) error {
	var p model.WalletPoint
	err := tx.Table(model.WalletPointTable).First(&p, "address = ? AND symbol = ?", address, symbol).Error
	switch err {
	case gorm.ErrRecordNotFound:
		return tx.Table(model.WalletPointTable).
			Create(&model.WalletPoint{Address: address, Symbol: symbol, Amount: amount}).Error
	case nil:
		return setWalletPointAmount(tx, p.ID, p.Amount+amount)
	default:
		return err
	}
}

/* ================= 1/2. GET /api/rounds 与 /api/rounds/current ================= */
type apiRoundItem struct {
	Id        uint    `json:"id"`
	Index     uint    `json:"index"`  // 轮序号（第几轮）
	Round     uint    `json:"round"`  // 轮序号（冗余，兼容前端字段名）
	Target    float64 `json:"target"` // 本轮总额度
	Raised    float64 `json:"raised"` // 已筹
	Status    string  `json:"status"` // open | success | failed（前端以此判断展示）
	Phase     string  `json:"phase"`  // 与 status 同值（store 读 phase 落到 status）
	IsSuccess bool    `json:"isSuccess"`
	Progress  float64 `json:"progress"` // 0-1
	StartAt   int64   `json:"startAt"`  // 毫秒时间戳
	EndAt     int64   `json:"endAt"`
	Asset     string  `json:"asset"` // 轮次代币
	Symbol    string  `json:"symbol"`
	// FailType 爆仓结算类型（仅"已结算的失败轮"才有值）：
	//   "refund"  = 倒1：全额退本
	//   "penalty" = 倒2/倒3：扣一半 + 折算算力
	// 2026-09-30 新增：原先前端把**所有**失败轮都标成"爆仓 倒1"，倒2/倒3 无法区分（展示不准）。
	FailType string `json:"failType,omitempty"`
}

type apiCurrentRound struct {
	apiRoundItem
	MinLimit    float64 `json:"minLimit"`
	MaxLimit    float64 `json:"maxLimit"`
	SettleRound uint    `json:"settleRound"` // 当前轮投注的结算轮序号 = 当前轮 + settleOffset
	StaticRate  float64 `json:"staticRate"`
}

// apiRoundPhase 推导展示相位：未结束(状态 0/1 且当前时间未到)为 open，否则筹满即 success。
// 真实成败由 round 程序结算后写入 Success/Status，此处仅供展示推导。
func apiRoundPhase(r model.ProjectRound, now time.Time) (status string, progress float64) {
	target := r.TargetVote
	progress = 0.0
	if target > 0 {
		progress = math.Min(1, r.CurrentVote/target)
	}
	if !r.EndTime.IsZero() && now.Before(r.EndTime) && r.Status <= enum.RoundStarting {
		return "open", progress
	}
	if target > 0 && r.CurrentVote >= target {
		return "success", 1
	}
	return "failed", progress
}

// roundFailTypes 计算"每个**已结算的失败轮**是倒1 还是倒2/倒3"。
//
// 口径与 apiVotePosition 一致：某失败轮 F 的前一轮/前二轮（F-1/F-2）属倒2/倒3（扣半 + 折算算力），
// F 自身是倒1（全额退本）。只对"状态已结束且 Success=false"的真实失败轮给值；
// 尚未结算、但按相位推导为 failed 的轮不在此列（它有可能是还没结算的倒1）。
func roundFailTypes(rounds []model.ProjectRound) map[uint]string {
	failed := make(map[uint]bool, len(rounds))
	for _, r := range rounds {
		if r.Status == enum.RoundEnding && !r.Success {
			failed[r.Round] = true
		}
	}
	out := make(map[uint]string, len(failed))
	for rn := range failed {
		if failed[rn+1] || failed[rn+2] {
			out[rn] = "penalty"
		} else {
			out[rn] = "refund"
		}
	}
	return out
}

func buildRoundItem(r model.ProjectRound, now time.Time, failTypes map[uint]string) apiRoundItem {
	status, progress := apiRoundPhase(r, now)
	return apiRoundItem{
		Id: r.ID, Index: r.Round, Round: r.Round,
		Target: apiFloat(r.TargetVote), Raised: apiFloat(r.CurrentVote),
		Status: status, Phase: status, IsSuccess: status == "success", Progress: progress,
		StartAt: r.StartTime.UnixMilli(), EndAt: r.EndTime.UnixMilli(),
		Asset: r.Symbol, Symbol: r.Symbol,
		FailType: failTypes[r.Round],
	}
}

func buildCurrentRound(r model.ProjectRound, now time.Time, failTypes map[uint]string) apiCurrentRound {
	return apiCurrentRound{
		apiRoundItem: buildRoundItem(r, now, failTypes),
		MinLimit:     apiFloat(r.MinVote),
		MaxLimit:     apiFloat(r.MaxVote),
		SettleRound:  r.Round + apiSettleOffset,
		StaticRate:   apiStaticRate,
	}
}

// loadRounds 加载全部轮（新轮在前）
func (a *ApiAdapter) loadRounds() ([]model.ProjectRound, error) {
	var rounds []model.ProjectRound
	err := a.DB.Table(model.ProjectRoundTable).Order("round DESC, id DESC").Find(&rounds).Error
	return rounds, err
}

func (a *ApiAdapter) Rounds(c *fiber.Ctx) error {
	rounds, err := a.loadRounds()
	if err != nil {
		return c.Send(apiErr("查询轮次失败"))
	}
	now := time.Now()
	failTypes := roundFailTypes(rounds)
	out := make([]apiRoundItem, 0, len(rounds))
	for _, r := range rounds {
		out = append(out, buildRoundItem(r, now, failTypes))
	}
	return c.Send(apiOk(out))
}

func (a *ApiAdapter) CurrentRound(c *fiber.Ctx) error {
	rounds, err := a.loadRounds()
	if err != nil {
		return c.Send(apiErr("查询轮次失败"))
	}
	if len(rounds) == 0 {
		return c.Send(apiErr("暂无轮次"))
	}
	now := time.Now()
	failTypes := roundFailTypes(rounds)
	for _, r := range rounds {
		if status, _ := apiRoundPhase(r, now); status == "open" {
			return c.Send(apiOk(buildCurrentRound(r, now, failTypes)))
		}
	}
	// 无进行中轮：以最新轮兜底（沿用其上限配置）
	return c.Send(apiOk(buildCurrentRound(rounds[0], now, failTypes)))
}

/* ================= 3. GET /api/assets?address= 用户资产 ================= */
type apiPosition struct {
	Id             string  `json:"id"`         // 仓位标识（vote id 字符串化）
	RoundIndex     uint    `json:"roundIndex"` // 参与轮序号
	Amount         float64 `json:"amount"`
	JoinTime       int64   `json:"joinTime"`       // 毫秒
	Status         string  `json:"status"`         // pending | settled | refund(倒1退本) | penalty(倒2倒3扣半折算算力)
	Profit         float64 `json:"profit"`         // 已结算收益
	Loss           float64 `json:"loss,omitempty"` // 倒2/倒3 被扣除的数量（读账本 vote_reward.loss；倒1/成功为 0）
	ExpectedProfit float64 `json:"expectedProfit"` // 若成功可获静态收益
	SettleRound    uint    `json:"settleRound"`    // 结算轮序号
}

type apiAssets struct {
	Balances       map[string]float64 `json:"balances"`
	Positions      []apiPosition      `json:"positions"`
	TotalValueUsd  float64            `json:"totalValueUsd"`
	TotalProfitUsd float64            `json:"totalProfitUsd"`
}

// apiVotePosition 由一条 vote 推导展示仓位状态
// 判定顺序（先失败后成功，避免失败轮仓位被乐观显示成已结算）：
//  1. 仓位所在轮自身已失败结束 → 该仓位已被失败流程处理：
//     账本里有 loss 记录（vote_reward.loss>0）→ penalty（倒2/倒3 扣半 + 折算算力）；
//     没有 → refund（倒1 全额退本），均无收益
//  2. 结算轮（N+settleOffset）已结束 → settled，收益取 vote_reward 真实值，缺失按 13% 公式
//  3. 其余 → pending
//
// ⚠ 2026-09-30 改进：失败分支原先靠"同项目里 F+1/F+2 是否存在失败轮"的**启发式**判断倒2/倒3，
// 一旦历史数据或人工结算顺序与假设不符就会标错（把倒1 显示成扣半，或反之）。
// 现在直接读账本：`Failed()` 会为每个倒2/倒3 仓位写一条 vote_reward.loss>0，`FailedNormal()`（倒1）
// 不写 —— 有 loss 行即扣半，无比即全额退本，与结算实现同源、无需推断。
func (a *ApiAdapter) apiVotePosition(v model.Vote, now time.Time) (apiPosition, float64) {
	settleNo := v.Round + apiSettleOffset
	pos := apiPosition{
		Id:             strconv.FormatUint(uint64(v.ID), 10),
		RoundIndex:     v.Round,
		Amount:         apiFloat(v.Amount),
		JoinTime:       v.CreatedAt.UnixMilli(),
		SettleRound:    settleNo,
		ExpectedProfit: apiFloat(v.Amount * apiStaticRate),
	}
	// 1) 仓位所在轮自身失败结束 → 读账本判定（倒1退本 / 倒2倒3扣半）
	var joinRound model.ProjectRound
	if jErr := a.DB.Table(model.ProjectRoundTable).First(&joinRound, "id = ?", v.RoundId).Error; jErr == nil {
		if joinRound.Status == enum.RoundEnding && !joinRound.Success {
			var lossRow model.VoteReward
			lErr := a.DB.Table(model.VoteRewardTable).
				Where("round_id = ? AND address = ? AND loss > 0", v.RoundId, v.Address).
				First(&lossRow).Error
			if lErr == nil {
				pos.Status = "penalty" // 倒2/倒3：扣 50% 并折算算力（账本有 loss 记录）
				pos.Loss = apiFloat(lossRow.Loss)
			} else {
				pos.Status = "refund" // 倒1：本金全额退回（账本无 loss 记录）
			}
			return pos, 0
		}
	}
	// 结算轮（同项目 round=settleNo）是否已结束
	var settleRound model.ProjectRound
	err := a.DB.Table(model.ProjectRoundTable).
		Where("project_id = ? AND round = ?", v.ProjectId, settleNo).
		First(&settleRound).Error
	if err != nil || settleRound.EndTime.IsZero() || now.Before(settleRound.EndTime) {
		pos.Status = "pending"
		return pos, 0
	}
	// 已结算：优先取 vote_reward 真实静态收益，缺失按 13% 公式
	var reward model.VoteReward
	profit := v.Amount * apiStaticRate
	if rErr := a.DB.Table(model.VoteRewardTable).
		Where("round_id = ? AND address = ?", settleRound.ID, v.Address).
		First(&reward).Error; rErr == nil && reward.Static > 0 {
		profit = reward.Static
	}
	pos.Status = "settled"
	pos.Profit = apiFloat(profit)
	return pos, profit
}

func (a *ApiAdapter) Assets(c *fiber.Ctx) error {
	// 读接口也做会话一致性校验：带会话时只能读自己的地址（见 resolveReadAddress）
	address, aerr := resolveReadAddress(c, c.Query("address"))
	if aerr != nil {
		return c.Send(apiErr(aerr.Error()))
	}
	if address == "" {
		return c.Send(apiOk(apiAssets{Balances: map[string]float64{}, Positions: []apiPosition{}}))
	}
	var wallet model.Wallet
	if err := a.DB.Table(model.WalletTable).First(&wallet, "address = ?", address).Error; err != nil {
		return c.Send(apiErr("钱包不存在"))
	}
	// 余额
	var points []model.WalletPoint
	if err := a.DB.Table(model.WalletPointTable).Find(&points, "address = ?", address).Error; err != nil {
		return c.Send(apiErr("查询余额失败"))
	}
	balances := make(map[string]float64, len(points))
	for _, p := range points {
		balances[p.Symbol] = apiFloat(float64(p.Amount) / symbolDictValue(p.Symbol))
	}
	// 仓位 + 收益
	var votes []model.Vote
	if err := a.DB.Table(model.VoteTable).Find(&votes, "wallet_id = ?", wallet.ID).Error; err != nil {
		return c.Send(apiErr("查询持仓失败"))
	}
	now := time.Now()
	positions := make([]apiPosition, 0, len(votes))
	totalProfit := 0.0
	for _, v := range votes {
		pos, profit := a.apiVotePosition(v, now)
		positions = append(positions, pos)
		totalProfit += profit
	}
	// 总资产估值（U）
	totalValue := 0.0
	for sym, amt := range balances {
		totalValue += amt * apiTokenPrice(sym)
	}
	return c.Send(apiOk(apiAssets{
		Balances: balances, Positions: positions,
		TotalValueUsd: apiFloat(totalValue), TotalProfitUsd: apiFloat(totalProfit),
	}))
}

/* ================= 4. GET /api/user?address= 用户信息 ================= */
type apiUnderTreeItem struct {
	Address string  `json:"address"`
	Depth   uint    `json:"depth"`   // 展示代距（直推=1）
	Invest  float64 `json:"invest"`  // 累计参与额
	Success int64   `json:"success"` // 成功结算次数（vote_reward 条数近似）
}

type apiUser struct {
	Address              string             `json:"address"`
	DirectCount          int64              `json:"directCount"`
	TeamLevel            string             `json:"teamLevel"` // F1/F2/F3 或空串
	ReferralIncome       float64            `json:"referralIncome"`
	TeamIncome           float64            `json:"teamIncome"`
	UnderTreeTotalInvest float64            `json:"underTreeTotalInvest"`
	SubF1Count           int64              `json:"subF1Count"`
	SubF2Count           int64              `json:"subF2Count"`
	UnderTreeActive      int64              `json:"underTreeActive"`
	UnderTree            []apiUnderTreeItem `json:"underTree"`
}

func apiTeamLevelName(level uint) string {
	switch level {
	case enum.Level1:
		return "F1"
	case enum.Level2:
		return "F2"
	case enum.Level3:
		return "F3"
	}
	return ""
}

// buildUnderTree 伞下统计：成员（wallet_id 列表）填充等级/活跃/投资额
func (a *ApiAdapter) buildUnderTree(walletID uint, out *apiUser) {
	// 直推数（distance=0 为直推边），无条件统计（无伞下记录也需返回直推数）
	a.DB.Table(model.WalletTreeTable).
		Where("ancestor = ? AND distance = 0", walletID).Count(&out.DirectCount)
	var subIds []uint
	a.DB.Table(model.WalletTreeTable).
		Where("ancestor = ? AND distance >= 1", walletID).
		Distinct("descendant").Pluck("descendant", &subIds)
	if len(subIds) == 0 {
		return
	}
	// 伞下等级 / 活跃 / 累计投资
	a.DB.Table(model.WalletTable).Where("id IN ? AND level = ?", subIds, enum.Level1).Count(&out.SubF1Count)
	a.DB.Table(model.WalletTable).Where("id IN ? AND level = ?", subIds, enum.Level2).Count(&out.SubF2Count)
	a.DB.Table(model.VoteTable).Where("wallet_id IN ?", subIds).Distinct("wallet_id").Count(&out.UnderTreeActive)
	var investTotal float64
	a.DB.Table(model.VoteTable).Where("wallet_id IN ?", subIds).Select("IFNULL(SUM(amount),0)").Scan(&investTotal)
	out.UnderTreeTotalInvest = apiFloat(investTotal)
	// underTree 明细（深度 ≤3，按参与额倒序，最多 50）
	type treeRow struct {
		Address string
		Depth   uint
		Invest  float64
	}
	var rows []treeRow
	a.DB.Raw(`SELECT w.address, MIN(t.distance) AS depth,
		IFNULL((SELECT SUM(v.amount) FROM `+model.VoteTable+` v WHERE v.wallet_id = w.id), 0) AS invest
		FROM `+model.WalletTreeTable+` t JOIN `+model.WalletTable+` w ON w.id = t.descendant
		WHERE t.ancestor = ? AND t.distance BETWEEN 1 AND ?
		GROUP BY w.id, w.address
		ORDER BY invest DESC LIMIT 50`, walletID, apiMaxTreeDepth).Scan(&rows)
	for _, row := range rows {
		var successCnt int64
		a.DB.Table(model.VoteRewardTable).Where("address = ?", row.Address).Count(&successCnt)
		out.UnderTree = append(out.UnderTree, apiUnderTreeItem{
			Address: row.Address, Depth: row.Depth,
			Invest: apiFloat(row.Invest), Success: successCnt,
		})
	}
}

func (a *ApiAdapter) User(c *fiber.Ctx) error {
	address, aerr := resolveReadAddress(c, c.Query("address"))
	if aerr != nil {
		return c.Send(apiErr(aerr.Error()))
	}
	if address == "" {
		return c.Send(apiOk(apiUser{UnderTree: []apiUnderTreeItem{}}))
	}
	var wallet model.Wallet
	if err := a.DB.Table(model.WalletTable).First(&wallet, "address = ?", address).Error; err != nil {
		return c.Send(apiErr("钱包不存在"))
	}
	out := apiUser{
		Address:   wallet.Address,
		TeamLevel: apiTeamLevelName(wallet.Level),
		UnderTree: []apiUnderTreeItem{},
	}
	a.buildUnderTree(wallet.ID, &out)
	// 作为受益人的累计动态收益
	out.ReferralIncome = a.sumReward(address, "dynamic_shard")
	out.TeamIncome = a.sumReward(address, "dynamic_team")
	return c.Send(apiOk(out))
}

/* ================= 5. GET /api/miner?address= 挖矿（算力矿机） ================= */
type apiMiner struct {
	Hashrate     float64 `json:"hashrate"`     // 算力（U 计价）
	Mode         string  `json:"mode"`         // default | fixed300 | tm（展示用名）
	ModeId       uint    `json:"modeId"`       // 0 默认三倍出局 / 1 最长300天 / 2 TM
	Created      int64   `json:"created"`      // 最早账户创建时间
	PayoutTotal  float64 `json:"payoutTotal"`  // 已累计产出（币本位）
	Claimable    float64 `json:"claimable"`    // 可领取（币本位）
	PayoutTarget float64 `json:"payoutTarget"` // 三倍出局目标（U 计价 = 算力 × 3）
	TargetCoin   float64 `json:"targetCoin"`   // 三倍出局目标（币本位，后端口径，前端不再自算）
	DailyOutput  float64 `json:"dailyOutput"`  // 当前模式下的日产出估算（币本位/天）
	// PriceSource 保底折算用的币价来源：avg4h=4 小时均价 / spot=样本不足回落实时价（需求#8）
	PriceSource  string  `json:"priceSource"`
	Switchable   bool    `json:"switchable"`   // 是否可切换模式（有未产出且未出局的账户）
	AccountCount int     `json:"accountCount"` // 算力账户数
}

func apiMinerModeName(m uint) string {
	switch m {
	case 1:
		return "fixed300"
	case 2:
		return "tm"
	}
	return "default"
}

func (a *ApiAdapter) Miner(c *fiber.Ctx) error {
	address, aerr := resolveReadAddress(c, c.Query("address"))
	if aerr != nil {
		return c.Send(apiErr(aerr.Error()))
	}
	if address == "" {
		return c.Send(apiOk(apiMiner{}))
	}
	var powers []model.HashPower
	// 按创建时间倒序，首条为最新账户，其 mode 作为展示口径
	if err := a.DB.Table(model.HashPowerTable).Order("created_at DESC").Find(&powers, "address = ?", address).Error; err != nil {
		return c.Send(apiErr("查询挖矿失败"))
	}
	out := apiMiner{Mode: "default", Created: time.Now().UnixMilli()}
	var latestMode uint
	for i, p := range powers {
		out.Hashrate += p.Power
		out.PayoutTotal += p.TotalReward
		out.Claimable += p.RemainReward
		out.TargetCoin += p.TargetReward
		if p.Status == 0 && p.TotalReward == 0 {
			out.Switchable = true //还有可调整的账户
		}
		if i == 0 {
			latestMode = p.Mode
			out.Mode = apiMinerModeName(p.Mode)
			out.ModeId = p.Mode
			out.Created = p.CreatedAt.UnixMilli()
		}
	}
	out.AccountCount = len(powers)
	out.Hashrate = apiFloat(out.Hashrate)
	out.PayoutTotal = apiFloat(out.PayoutTotal)
	out.Claimable = apiFloat(out.Claimable)
	out.PayoutTarget = apiFloat(out.Hashrate * impl.HashPowerBuff)
	out.TargetCoin = apiFloat(out.TargetCoin)
	//日产出由后端按统一口径估算（与 DailyReward / EstimateDailyOutput 同源），
	//避免前端用另一套公式重算导致"页面数字 ≠ 结算数字"
	out.DailyOutput = apiFloat(impl.EstimateDailyOutput(out.Hashrate, latestMode))
	//保底口径的币价来源：avg4h = 4 小时均价（需求#8），spot = 样本不足已回落实时价。
	//前端据此标注，避免把实时价当成均价展示（"看着是均价、其实是瞬时价"是一种误导）。
	_, priceSource := impl.Price4hWithSource(enum.FIBO)
	out.PriceSource = priceSource
	return c.Send(apiOk(out))
}

/* ================= 5b. POST /api/miner/mode 用户自选算力出局模式 ================= */

type apiMinerModeReq struct {
	Address string `json:"address"`
	Symbol  string `json:"symbol"` // 可选：仅调整该币种的算力账户
	Mode    *uint  `json:"mode"`   // 必填：0 默认三倍出局 / 1 最长300天 / 2 TM
}

// MinerMode 用户自选算力出局方式（需求#8「算力：选择其中一种方式」）。
// 修复前该能力只存在于运维接口 /ops/setHashPowerMode：DApp 上的"切换模式"只改本地状态，
// 不发任何请求（假交互），用户实际无法选择。
// 身份强制走会话票；仅允许调整自己名下、尚未产出且未出局的账户（防挖到一半改模式套利）。
func (a *ApiAdapter) MinerMode(c *fiber.Ctx) error {
	var req apiMinerModeReq
	if err := c.BodyParser(&req); err != nil {
		return c.Send(apiErr("参数错误"))
	}
	if req.Mode == nil {
		return c.Send(apiErr("缺少 mode"))
	}
	addr, aerr := resolveActingAddressStrict(c, req.Address)
	if aerr != nil {
		return c.Send(apiErr(aerr.Error()))
	}
	if _, err := a.HashPowerMgr.SwitchMode(addr, strings.TrimSpace(req.Symbol), *req.Mode); err != nil {
		return c.Send(apiErr(err.Error()))
	}
	return c.Send(apiOk(fiber.Map{
		"mode":      *req.Mode,
		"modeName":  apiMinerModeName(*req.Mode),
		"updatedAt": time.Now().UnixMilli(),
	}))
}

/* ================= 6. GET /api/prices 价格表 ================= */
func (a *ApiAdapter) Prices(c *fiber.Ctx) error {
	prices := map[string]float64{
		"FIBO": apiFloat(apiTokenPrice("FIBO")),
		"USDT": apiFloat(apiTokenPrice("USDT")),
		"TM":   apiFloat(apiTokenPrice("TM")),
	}
	return c.Send(apiOk(prices))
}

/* ================= 10. GET /api/deposit/info 充值信息 ================= */

// DepositInfo 下发给前端的充值参数：收款地址、代币合约、精度、确认数、最小充值额。
//
// 为什么要有这个接口：DApp 需要「往哪转、转什么、转多少起算」才能做充值引导或一键充值，
// 而这些都属于后端/运维配置（Chain.WatchPool / WatchToken / Confirmations）。
// 把它们硬编码在前端有两个问题：①换地址要发前端版本；②地址写错就会把用户的钱转丢。
// 本接口只读、不下发任何私钥，收款地址本就是公开信息（链上可查）。
func (a *ApiAdapter) DepositInfo(c *fiber.Ctx) error {
	cfg := config.EtcConfig
	if cfg == nil {
		return c.Send(apiErr("服务暂不可用"))
	}
	//收款地址：优先 WatchPool（充值监听的收款池）；为空时回落到手续费池地址，避免前端拿不到值
	pool := strings.TrimSpace(cfg.Chain.WatchPool)
	if pool == "" {
		pool = strings.TrimSpace(cfg.TronPool.Address)
	}
	token := strings.TrimSpace(cfg.Chain.WatchToken)
	if token == "" {
		token = strings.TrimSpace(cfg.Chain.TmContract)
	}
	//充值入账币种 = 手续费币种（私链/当前阶段就是 TM）
	symbol := impl.FeeSymbol
	//⚠ 这里下发的是 **ERC20 的 decimals 位数**（TM/FIBO = 8），不是 SymbolDictionary 里的换算倍率（1e8）。
	//   前端用它做 parseUnits/formatUnits，给错会把金额放大/缩小 1e8 倍。
	decimals := cfg.Withdraw.TokenDecimals
	if decimals == 0 {
		decimals = 8
	}
	out := fiber.Map{
		"symbol":        symbol,
		"address":       pool,
		"contract":      token,
		"decimals":      decimals,
		"chainId":       cfg.Chain.FiboChainId,
		"rpcUrl":        cfg.Chain.FiboRpcUrl,
		"confirmations": cfg.Chain.Confirmations,
		"enabled":       pool != "" && token != "",
	}
	return c.Send(apiOk(out))
}

/* ---------- 价格 / 精度工具 ---------- */
func symbolDictValue(symbol string) float64 {
	// 精度换算：wallet_point 存最小单位整数，展示时除以 SymbolDictionary
	if v, ok := config.SymbolDictionary[symbol]; ok && v > 0 {
		return v
	}
	return 1
}

func apiTokenPrice(symbol string) float64 {
	// 优先取后端内存行情（RefreshFIBO20PricePrice 60s 刷新），TM 固定 3.4；
	// 行情源不可用时回退默认展示价，避免前端价格显示 0
	if p := impl.GetKtoSymbolPrice(symbol); p > 0 {
		return p
	}
	if p, ok := apiFallbackPrices[symbol]; ok {
		return p
	}
	return 1.0
}

/* ================= 7. POST /api/participate 参与 ================= */
func (a *ApiAdapter) Participate(c *fiber.Ctx) error {
	var req apiParticipateReq
	if err := c.BodyParser(&req); err != nil {
		return c.Send(apiErr("参数错误"))
	}
	// 身份强制走会话票（无会话直接拒绝）：X-API-Key 是公开的，不能作为身份凭据
	addr, aerr := resolveActingAddressStrict(c, req.Address)
	if aerr != nil {
		return c.Send(apiErr(aerr.Error()))
	}
	req.Address = addr
	var wallet model.Wallet
	if err := a.DB.Table(model.WalletTable).First(&wallet, "address = ?", req.Address).Error; err != nil {
		return c.Send(apiErr("钱包不存在"))
	}
	var round model.ProjectRound
	if err := a.DB.Table(model.ProjectRoundTable).First(&round, "id = ?", req.RoundId).Error; err != nil {
		return c.Send(apiErr("该轮不存在"))
	}
	if req.Asset != "" && req.Asset != round.Symbol {
		return c.Send(apiErr("资产与轮次币种不一致"))
	}
	// 下限校验（与 VoteManager.Vote 内一致，先给友好提示）
	if !wallet.Admin && req.Amount < round.MinVote {
		return c.Send(apiErr(fmt.Sprintf("低于最低限额 %v", round.MinVote)))
	}
	vote, err := a.VoteManager.Vote(wallet, round, req.Amount)
	if err != nil {
		return c.Send(apiErr(err.Error()))
	}
	out := fiber.Map{
		"txHash": fmt.Sprintf("local-vote-%d-%d", vote.ID, time.Now().UnixMilli()),
		"position": apiPosition{
			Id:             strconv.FormatUint(uint64(vote.ID), 10),
			RoundIndex:     vote.Round,
			Amount:         apiFloat(vote.Amount),
			JoinTime:       time.Now().UnixMilli(),
			Status:         "pending",
			SettleRound:    vote.Round + apiSettleOffset,
			ExpectedProfit: apiFloat(vote.Amount * apiStaticRate),
		},
		"balance": a.walletBalance(req.Address, round.Symbol),
	}
	return c.Send(apiOk(out))
}

/* ================= 8. POST /api/withdraw 提币（本地记账，3% TM 手续费） ================= */
func (a *ApiAdapter) Withdraw(c *fiber.Ctx) error {
	var req apiWithdrawReq
	if err := c.BodyParser(&req); err != nil {
		return c.Send(apiErr("参数错误"))
	}
	// 身份强制走会话票：否则带着自己的会话、把 body 的 address 换成别人，就能提走别人的币；
	// 而没有会话时（修复前）直接信任 body 地址 = 任何拿到公开 API Key 的人都能提走别人的币
	addr, aerr := resolveActingAddressStrict(c, req.Address)
	if aerr != nil {
		return c.Send(apiErr(aerr.Error()))
	}
	req.Address = addr
	if req.FeeToken != impl.FeeSymbol {
		return c.Send(apiErr("手续费代币须为 " + impl.FeeSymbol))
	}
	var wallet model.Wallet
	if err := a.DB.Table(model.WalletTable).First(&wallet, "address = ?", req.Address).Error; err != nil {
		return c.Send(apiErr("钱包不存在"))
	}
	var point model.WalletPoint
	if err := a.DB.Table(model.WalletPointTable).First(&point, "address = ? AND symbol = ?", req.Address, req.Asset).Error; err != nil {
		return c.Send(apiErr("余额不足"))
	}
	withdrawDecimal := uint64(req.Amount * symbolDictValue(req.Asset))
	//最低起提额（owner 2026-09-30 敲定：**100 个起提**）。
	//⚠ 用小数比较前先挡掉 <=0；阈值 0 = 关闭校验（私链联调配置里就是 0，便于小额测试）。
	if impl.WithdrawMinAmount > 0 && req.Amount < impl.WithdrawMinAmount {
		return c.Send(apiErr(fmt.Sprintf("最低起提 %v %s", impl.WithdrawMinAmount, req.Asset)))
	}
	if point.Amount < withdrawDecimal {
		return c.Send(apiErr("余额不足"))
	}
	// 手续费：提币价值(U) × 3% / 手续费币价(U) → 所需 TM 数量
	feeAmount := req.Amount * apiTokenPrice(req.Asset) * apiFeeRate / apiTokenPrice(impl.FeeSymbol)
	feeDecimal := uint64(feeAmount * symbolDictValue(impl.FeeSymbol))
	var feePoint model.WalletPoint
	if err := a.DB.Table(model.WalletPointTable).First(&feePoint, "address = ? AND symbol = ?", req.Address, impl.FeeSymbol).Error; err != nil {
		return c.Send(apiErr("需要手续费 " + impl.FeeSymbol))
	}
	if feePoint.Amount < feeDecimal {
		return c.Send(apiErr("手续费 " + impl.FeeSymbol + " 不足"))
	}
	//D 模块：提现额度校验（单笔/单日/笔数；阈值默认 0 = 不限制，数值待 owner 拍板）。
	//单笔上限在事务外先给友好提示；日累计/日笔数**放进扣账事务内**（见下方），
	//否则并发或重放可以基于同一旧快照绕过日累计与日笔数（单笔上限不受影响）。
	if msg := CheckWithdrawLimits(req.Address, req.Amount); msg != "" {
		return c.Send(apiErr(msg))
	}
	//B 模块预检（在扣账之前做，失败即整笔不受理）：
	//确认该资产确实开放了链上出金，并拿到对应的 ERC20 合约。
	//⚠ 2026-09-19 私链实测暴露的资金级缺陷：原实现无论如何都用 Withdraw.TokenContract 广播，
	//   于是「提现 10 USDT」在链上转出了 10 个 TM（合约/资产错配），而记账扣的是 USDT。
	//   现在改为按资产解析合约，未开放链上出金的资产直接拒绝，绝不用别的代币顶替。
	broadcast := strings.EqualFold(config.EtcConfig.Withdraw.Mode, "broadcast")
	var outToken string
	if broadcast {
		var oerr error
		if outToken, oerr = resolveWithdrawToken(req.Asset); oerr != nil {
			log.WithFields(log.Fields{"address": req.Address, "asset": req.Asset, "err": oerr}).
				Warnln("[withdraw] 出金预检未通过，本笔不受理")
			return c.Send(apiErr(oerr.Error()))
		}
	}
	// 记账事务：扣主币 + 扣手续费 + 双流水 + 手续费入待销毁队列
	txHash := fmt.Sprintf("local-withdraw-%d", time.Now().UnixMilli())
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		//事务内复核日累计/日笔数（并发安全；阈值 0 = 不限制时本调用直接返回空串）
		if msg := CheckWithdrawLimitsTx(tx, req.Address, req.Amount); msg != "" {
			return errors.New(msg)
		}
		if err := setWalletPointAmount(tx, point.ID, point.Amount-withdrawDecimal); err != nil {
			return err
		}
		if err := setWalletPointAmount(tx, feePoint.ID, feePoint.Amount-feeDecimal); err != nil {
			return err
		}
		// 提币流水 + 手续费流水（手续费 Type=Fee，修复笔误后的正确语义）。
		// 出账地址：开启出金时链上转出方是热钱包，记账应与链上一致；仅记账模式仍记自身。
		outFrom := wallet.Address
		if broadcast {
			if hot := hotWalletAddress(); hot != "" {
				outFrom = hot
			}
		}
		if err := tx.Table(model.WalletTxTable).Create(makeWalletTx(req.Asset, outFrom, wallet.Address, withdrawDecimal, enum.BlockOut, enum.BlockOutText, txHash)).Error; err != nil {
			return err
		}
		if err := tx.Table(model.WalletTxTable).Create(makeWalletTx(impl.FeeSymbol, wallet.Address, wallet.Address, feeDecimal, enum.Fee, enum.FeeText, txHash)).Error; err != nil {
			return err
		}
		//手续费入待销毁队列（fee_burn, status=0），由 cmd/burn 汇总后链上销毁。
		//⚠ 2026-09-19 补：此前本接口只扣费不写 fee_burn，而前端 DApp 走的正是 /api/withdraw
		//   （原生的 /wallet/withdraw 才写），导致 TM 手续费收了却永远不会被销毁，通缩模型断链。
		if feeDecimal > 0 {
			feeBurn := &model.FeeBurn{
				Symbol: impl.FeeSymbol,
				Amount: feeDecimal,
				Status: 0,
			}
			if err := tx.Table(model.FeeBurnTable).Create(feeBurn).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return c.Send(apiErr(err.Error()))
	}

	//B 模块：出金上链。**默认 Mode != "broadcast" 时行为与改造前完全一致（仅记账）**，
	//出金是动真钱的操作，必须显式配置才开启。此后的失败**不回滚记账**（回滚会造成
	//「余额退回但链上已转账」的双花风险），保留记账 + ERROR 日志 + 返回可核查的哈希。
	if broadcast {
		txHash = a.broadcastWithdraw(wallet.Address, outToken, withdrawDecimal, txHash)
	}

	return c.Send(apiOk(fiber.Map{
		"txHash":    txHash,
		"amount":    apiFloat(req.Amount),
		"feeAmount": apiFloat(feeAmount),
		"feeToken":  impl.FeeSymbol,
		"balance":   apiFloat(float64(point.Amount-withdrawDecimal) / symbolDictValue(req.Asset)),
	}))
}

// resolveWithdrawToken 按提现资产解析链上出金合约与精度（fail-closed）。
//
// 解析顺序：
//  1. Withdraw.AssetContracts[资产]（多资产映射，命中即用）；
//  2. 资产 == Withdraw.ChainAsset（单资产缺省，用 Withdraw.TokenContract/TokenDecimals）；
//  3. 其余一律拒绝 —— **绝不用别的代币顶替**。
//
// 为什么必须 fail-closed：出金是链上不可逆动作。资产与合约错配会让用户提 USDT 却收到 TM，
// 且链上数量按 USDT 的记账数量转出，属于资金级事故（2026-09-19 私链实测暴露）。
func resolveWithdrawToken(asset string) (string, error) {
	cfg := config.EtcConfig.Withdraw
	asset = strings.TrimSpace(asset)

	if len(cfg.AssetContracts) > 0 {
		for sym, addr := range cfg.AssetContracts {
			if strings.EqualFold(strings.TrimSpace(sym), asset) {
				if strings.TrimSpace(addr) == "" {
					return "", fmt.Errorf("资产 %s 的出金合约地址为空，已拒绝出金", asset)
				}
				if d, ok := lookupAssetDecimals(cfg.AssetDecimals, asset); ok && d != 0 {
					if err := verifyTokenDecimals(addr, d); err != nil {
						return "", err
					}
				}
				return addr, nil
			}
		}
	}

	chainAsset := strings.TrimSpace(cfg.ChainAsset)
	if chainAsset == "" {
		return "", fmt.Errorf("未配置 Withdraw.ChainAsset（可链上出金的资产），已拒绝链上出金；请在 yml 显式指定")
	}
	if !strings.EqualFold(chainAsset, asset) {
		return "", fmt.Errorf("暂不支持 %s 链上出金（当前仅支持 %s）", asset, chainAsset)
	}
	if strings.TrimSpace(cfg.TokenContract) == "" {
		return "", fmt.Errorf("资产 %s 未配置出金合约地址（Withdraw.TokenContract），已拒绝出金", asset)
	}
	if err := verifyTokenDecimals(cfg.TokenContract, cfg.TokenDecimals); err != nil {
		return "", err
	}
	return cfg.TokenContract, nil
}

// lookupAssetDecimals 在资产精度映射里做大小写不敏感查找
func lookupAssetDecimals(m map[string]uint8, asset string) (uint8, bool) {
	for sym, d := range m {
		if strings.EqualFold(strings.TrimSpace(sym), asset) {
			return d, true
		}
	}
	return 0, false
}

// verifyTokenDecimals 广播前核对链上 decimals 与配置值。
// 精度写错会让出金数量差 10 的若干次方，链上不可逆，必须提前拦住。
// configDecimals 为 0 表示未配置（按旧行为放行，仅告警）。
func verifyTokenDecimals(token string, configDecimals uint8) error {
	rpcUrl := config.EtcConfig.Chain.FiboRpcUrl
	onChain, err := evm.ReadERC20Decimals(rpcUrl, token)
	if err != nil {
		log.WithFields(log.Fields{"token": token, "err": err}).
			Warnln("[withdraw] 无法读取链上 decimals，跳过精度校验（请确认 TokenContract/RPC 是否正确）")
		return nil
	}
	if configDecimals == 0 {
		log.WithFields(log.Fields{"token": token, "chainDecimals": onChain}).
			Warnln("[withdraw] 未配置出金代币精度，已按链上值放行；建议在 yml 显式配置以避免记账/链上口径漂移")
		return nil
	}
	if onChain != configDecimals {
		return fmt.Errorf("出金代币精度不一致（配置 %d / 链上 %d），已拒绝出金", configDecimals, onChain)
	}
	return nil
}

// broadcastWithdraw 把已记账的提现做链上广播（B 模块）。
//
// 失败取舍：**记账不回滚**。回滚会造成「用户余额退回但链上已转账」的双花风险；
// 保留记账 + ERROR 日志，由人工按 localHash 核查补发，是资金侧更安全的一侧。
// outToken 必须是 resolveWithdrawToken 解析出的合约地址（调用方已完成资产预检）。
// 返回值：链上 hash；广播失败时原样返回 localHash。
func (a *ApiAdapter) broadcastWithdraw(address, outToken string, amountDecimal uint64, localHash string) string {
	//统一取值口：环境变量（Withdraw.HotWalletKeyEnv 指定的名字）优先，yml 值仅作本地兜底
	hotKey := config.HotWalletPrivateKey()
	if outToken == "" || hotKey == "" {
		log.WithFields(log.Fields{"address": address, "localHash": localHash}).
			Errorln("[withdraw] Mode=broadcast 但出金合约/热钱包私钥未配置，本笔停留在记账态，需人工处理")
		return localHash
	}
	rpcUrl := config.EtcConfig.Chain.FiboRpcUrl

	realHash, err := evm.SendERC20(
		rpcUrl,
		outToken,
		hotKey,
		address,
		new(big.Int).SetUint64(amountDecimal),
		config.EtcConfig.Chain.FiboChainId,
	)
	if err != nil {
		//注意：SendERC20 现在会等待回执，revert / 回执超时都会走到这里。
		//回执超时返回的 err 是 ErrTxStatusUnknown，此时 realHash 是**已广播**的交易哈希，
		//必须记下来供人工核查（不能当作「没发出去」重发，否则会双花）。
		log.WithFields(log.Fields{
			"address": address, "amountDecimal": amountDecimal, "token": outToken,
			"localHash": localHash, "broadcastHash": realHash, "err": err,
		}).Errorln("[withdraw] 链上广播未确认，记账保留，需人工按 hash 核查")
		if realHash != "" && errors.Is(err, evm.ErrTxStatusUnknown) {
			//把「已广播但未确认」的哈希落库（success=1 表示未确认/失败，见 model.WalletTx）
			if uerr := a.DB.Table(model.WalletTxTable).Where("`hash` = ?", localHash).
				Updates(map[string]interface{}{"hash": realHash, "success": 1}).Error; uerr != nil {
				log.WithFields(log.Fields{"localHash": localHash, "err": uerr}).
					Errorln("[withdraw] 记录未确认哈希失败，请人工核对")
			}
		}
		return localHash
	}
	if uerr := a.DB.Table(model.WalletTxTable).Where("`hash` = ?", localHash).
		Updates(map[string]interface{}{"hash": realHash, "success": 0}).Error; uerr != nil {
		log.WithFields(log.Fields{"localHash": localHash, "realHash": realHash, "err": uerr}).
			Errorln("[withdraw] 链上已成功但本地流水 hash 更新失败，请人工合并")
	}
	log.WithFields(log.Fields{"address": address, "token": outToken, "txHash": realHash}).
		Infoln("[withdraw] 出金已广播并确认（receipt.status=1）")
	return realHash
}

// makeWalletTx 构造一条 wallet_tx（记账流水）。
// from/to 必须显式传入：原实现把出账目标写成自身，导致流水看不出钱实际转给了谁 / 谁转出的。
func makeWalletTx(symbol, from, to string, amount uint64, txType uint, desc, hash string) *model.WalletTx {
	return &model.WalletTx{
		Symbol: symbol, From: from, To: to,
		Amount: amount, Desc: desc, Type: txType, Hash: hash, Success: 0, //0=成功（见 model.WalletTx 注释）
	}
}

/* ================= GET /api/rules 规则数值（以**后端实际生效参数**为准） ================= */

// apiRules 规则参数快照。
//
// 为什么要有这个接口（2026-09-30 新增）：
//   规则数值此前在**三处各写一份** —— 后端 `core/impl/config.go`（真正参与结算的 var，
//   可经 `/ops/params` 热改）、本适配层（`apiStaticRate/apiSettleOffset` 等硬编码）、
//   前端 `constants/config.js`。运维一改参数，适配层与前端仍是旧数字：
//   页面写着"13%"，实际结算可能已经是别的值 —— 用户看到的就是"说好的收益不对"。
//   现在前端启动时拉这份快照覆盖本地默认值，页面展示与结算口径同源；
//   前端保留默认值仅为"接口不可用时不至于白屏"的兜底。
type apiRuleTeamLevel struct {
	Level               string  `json:"level"`
	Rate                float64 `json:"rate"`
	NeedDirect          uint    `json:"needDirect"`
	NeedUnderTreeActive int     `json:"needUnderTreeActive"`
	NeedSubCount        uint    `json:"needSubCount"`
}

type apiRules struct {
	StaticRewardRate    float64 `json:"staticRewardRate"`    //静态收益比例（如 0.13）
	SettleOffset        uint    `json:"settleOffset"`        //三进一出：第 N 轮仓位在第 N+settleOffset 轮结算（= MinRound）
	LossRate            float64 `json:"lossRate"`            //倒2/倒3 扣除比例（如 0.5）
	FeeRate             float64 `json:"feeRate"`             //提币手续费比例（如 0.03）
	FeeSymbol           string  `json:"feeSymbol"`           //手续费币种
	BurnTarget          float64 `json:"burnTarget"`          //通缩目标（手续费币种剩余流通量下限）
	CurrentStage        int     `json:"currentStage"`        //当前阶段（决定可参与币种）
	MinTimeLimitSeconds float64 `json:"minTimeLimitSeconds"` //轮次最小时限（秒）
	MaxVoteGrowRate     float64 `json:"maxVoteGrowRate"`     //每轮最高投入限额递增率
	MiningRewardBuff    float64 `json:"miningRewardBuff"`    //矿机产出倍数（模式0/1 三倍出局）
	//动态收益各代比例（第 1/3/5 代）
	DynamicShardRate1 float64 `json:"dynamicShardRate1"`
	DynamicShardRate3 float64 `json:"dynamicShardRate3"`
	DynamicShardRate5 float64 `json:"dynamicShardRate5"`
	//团队等级：比例与门槛（F1/F2/F3）
	TeamLevels []apiRuleTeamLevel `json:"teamLevels"`
	//算力矿机：三倍倍数 / 最长天数 / 矿池占比 / 矿池日产出
	HashPowerBuff          float64 `json:"hashPowerBuff"`
	HashPowerMaxDays       float64 `json:"hashPowerMaxDays"`
	HashPowerPoolDailyRatio float64 `json:"hashPowerPoolDailyRatio"`
	HashPowerPoolDailyOutput float64 `json:"hashPowerPoolDailyOutput"`
	//提现额度（0=不限）
	WithdrawMinAmount     float64 `json:"withdrawMinAmount"` //最低起提额（0=不限；owner 2026-09-30 定为 100）
	WithdrawSingleMax     float64 `json:"withdrawSingleMax"`
	WithdrawDailyMax      float64 `json:"withdrawDailyMax"`
	WithdrawDailyCountMax int     `json:"withdrawDailyCountMax"`
}

func (a *ApiAdapter) Rules(c *fiber.Ctx) error {
	return c.Send(apiOk(apiRules{
		StaticRewardRate:        impl.StaticRewardRate,
		SettleOffset:            impl.MinRound,
		LossRate:                impl.LossRate,
		FeeRate:                 impl.FeeRate,
		FeeSymbol:               impl.FeeSymbol,
		BurnTarget:              impl.BurnTarget,
		CurrentStage:            int(impl.CurrentStage),
		MinTimeLimitSeconds:     impl.MinTimeLimitSeconds,
		MaxVoteGrowRate:         impl.RoundMaxVoteGrowRate,
		MiningRewardBuff:        impl.MiningRewardBuff,
		DynamicShardRate1:       impl.DynamicShardReward1GenerateRate,
		DynamicShardRate3:       impl.DynamicShardReward3GenerateRate,
		DynamicShardRate5:       impl.DynamicShardReward5GenerateRate,
		TeamLevels: []apiRuleTeamLevel{
			// F1：门槛是"直推数 + 同期伞下参投人数"；F2/F3 的门槛是"伞下 F1/F2 的个数"
			//（本地前端 levels[1]/[2] 的 needDirect 为 0，若这里也下发 F1 的直推门槛会把它覆盖错）
			{Level: "F1", Rate: impl.TeamRewardRate1, NeedDirect: impl.F1DirectCount, NeedUnderTreeActive: impl.F1VoteCount, NeedSubCount: 0},
			{Level: "F2", Rate: impl.TeamRewardRate2, NeedDirect: 0, NeedUnderTreeActive: 0, NeedSubCount: impl.F1ToF2Count},
			{Level: "F3", Rate: impl.TeamRewardRate3, NeedDirect: 0, NeedUnderTreeActive: 0, NeedSubCount: impl.F2ToF3Count},
		},
		HashPowerBuff:            impl.HashPowerBuff,
		HashPowerMaxDays:         impl.HashPowerMaxDays,
		HashPowerPoolDailyRatio:  impl.HashPowerPoolDailyRatio,
		HashPowerPoolDailyOutput: impl.HashPowerPoolDailyOutput,
		WithdrawMinAmount:        impl.WithdrawMinAmount,
		WithdrawSingleMax:        impl.WithdrawSingleMax,
		WithdrawDailyMax:         impl.WithdrawDailyMax,
		WithdrawDailyCountMax:    impl.WithdrawDailyCountMax,
	}))
}

// hotWalletAddress 从配置的出金热钱包私钥推导地址（仅用于把记账流水的转出方写成与链上一致）。
// 私钥缺失/非法时返回空串，由调用方回落到原口径，不影响主流程。
func hotWalletAddress() string {
	//走统一取值口：支持 Withdraw.HotWalletKeyEnv 指定的环境变量注入
	key := strings.TrimPrefix(config.HotWalletPrivateKey(), "0x")
	if key == "" {
		return ""
	}
	priv, err := ethcrypto.HexToECDSA(key)
	if err != nil {
		return ""
	}
	return ethcrypto.PubkeyToAddress(priv.PublicKey).Hex()
}

/* ================= 9. POST /api/miner/claim 领取挖矿产出（入账 FIBO） ================= */
func (a *ApiAdapter) ClaimMiner(c *fiber.Ctx) error {
	var req apiClaimReq
	if err := c.BodyParser(&req); err != nil {
		return c.Send(apiErr("参数错误"))
	}
	// 身份以会话票为准：否则改 body 的 address 就能领走别人的矿机产出（无会话时直接拒绝）
	addr, aerr := resolveActingAddressStrict(c, req.Address)
	if aerr != nil {
		return c.Send(apiErr(aerr.Error()))
	}
	req.Address = addr
	var wallet model.Wallet
	if err := a.DB.Table(model.WalletTable).First(&wallet, "address = ?", req.Address).Error; err != nil {
		return c.Send(apiErr("钱包不存在"))
	}
	var powers []model.HashPower
	if err := a.DB.Table(model.HashPowerTable).Find(&powers, "address = ?", req.Address).Error; err != nil {
		return c.Send(apiErr("查询算力账户失败"))
	}
	claimable := 0.0
	for _, p := range powers {
		claimable += p.RemainReward
	}
	if claimable <= 0 {
		return c.Send(apiOk(fiber.Map{"claimed": 0, "balance": 0}))
	}
	// 本地入账：清空 remain_reward → wallet_point.FIBO（与 impl.HashPowerManager.Withdraw 同语义，跳过最低起提门槛便于联调）
	claimDecimal := uint64(claimable * symbolDictValue(enum.FIBO))
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table(model.HashPowerTable).Where("address = ?", req.Address).
			Update("remain_reward", 0).Error; err != nil {
			return err
		}
		return addWalletPoint(tx, req.Address, enum.FIBO, claimDecimal)
	})
	if err != nil {
		return c.Send(apiErr(err.Error()))
	}
	balance := a.walletBalance(req.Address, enum.FIBO)
	log.WithFields(log.Fields{"method": "api.claimMiner", "address": req.Address, "claimed": claimable}).Infoln("算力矿机领取成功")
	return c.Send(apiOk(fiber.Map{"claimed": apiFloat(claimable), "balance": balance}))
}
