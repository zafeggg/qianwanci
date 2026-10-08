package handler

/**
 * 千万次 DApp - /api 适配层（联调用纯 JSON 接口）
 *
 * 背景：前端（dapp-vue）契约是 REST 明文 JSON（src/api/index.js 的 9 个接口），
 *       后端 npower 原生路由（/home /wallet /self）走 RSA+AES 签名 + JWT + 验证码。
 *       本文件在后端直接挂 /api/* 纯 JSON 路由，内部复用现有 Manager/DB 逻辑，
 *       使前端零协议改造即可吃真实数据。响应封装 {code:0,message,data} 与前端约定一致。
 *
 * 注意：
 *  1. 本适配层不做 RSA/JWT 鉴权，仅限本地开发/内测；生产对外须再加一层鉴权或走原生路由。
 *  2. 涉及上链的操作（真实充值/广播提币/结算轮）仍依赖链节点与定时程序，本地为记账语义，
 *     返回的 txHash 为本地流水标识，上线前应替换为链上广播。
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
	"encoding/json"
	"fmt"
	"github.com/gofiber/fiber/v2"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"math"
	"strconv"
	"time"
)

/* ---------- 规则常量（与前端 config.js 保持一致） ---------- */
const (
	apiSettleOffset   = 3    // 三进一出结算偏移
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

func buildRoundItem(r model.ProjectRound, now time.Time) apiRoundItem {
	status, progress := apiRoundPhase(r, now)
	return apiRoundItem{
		Id: r.ID, Index: r.Round, Round: r.Round,
		Target: apiFloat(r.TargetVote), Raised: apiFloat(r.CurrentVote),
		Status: status, Phase: status, IsSuccess: status == "success", Progress: progress,
		StartAt: r.StartTime.UnixMilli(), EndAt: r.EndTime.UnixMilli(),
		Asset: r.Symbol, Symbol: r.Symbol,
	}
}

func buildCurrentRound(r model.ProjectRound, now time.Time) apiCurrentRound {
	return apiCurrentRound{
		apiRoundItem: buildRoundItem(r, now),
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
	out := make([]apiRoundItem, 0, len(rounds))
	for _, r := range rounds {
		out = append(out, buildRoundItem(r, now))
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
	for _, r := range rounds {
		if status, _ := apiRoundPhase(r, now); status == "open" {
			return c.Send(apiOk(buildCurrentRound(r, now)))
		}
	}
	// 无进行中轮：以最新轮兜底（沿用其上限配置）
	return c.Send(apiOk(buildCurrentRound(rounds[0], now)))
}

/* ================= 3. GET /api/assets?address= 用户资产 ================= */
type apiPosition struct {
	Id             string  `json:"id"`         // 仓位标识（vote id 字符串化）
	RoundIndex     uint    `json:"roundIndex"` // 参与轮序号
	Amount         float64 `json:"amount"`
	JoinTime       int64   `json:"joinTime"`       // 毫秒
	Status         string  `json:"status"`         // pending | settled（settled=本息已结算）
	Profit         float64 `json:"profit"`         // 已结算收益
	ExpectedProfit float64 `json:"expectedProfit"` // 若成功可获静态收益
	SettleRound    uint    `json:"settleRound"`    // 结算轮序号
}

type apiAssets struct {
	Balances       map[string]float64 `json:"balances"`
	Positions      []apiPosition      `json:"positions"`
	TotalValueUsd  float64            `json:"totalValueUsd"`
	TotalProfitUsd float64            `json:"totalProfitUsd"`
}

// apiVotePosition 由一条 vote 推导展示仓位（pending/settled 取决于结算轮是否已结束）
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
	address := c.Query("address")
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
	address := c.Query("address")
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
	Mode         string  `json:"mode"`         // default | fixed300 | tm
	Created      int64   `json:"created"`      // 最早账户创建时间
	PayoutTotal  float64 `json:"payoutTotal"`  // 已累计产出
	Claimable    float64 `json:"claimable"`    // 可领取
	PayoutTarget float64 `json:"payoutTarget"` // 三倍出局目标（U）
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
	address := c.Query("address")
	if address == "" {
		return c.Send(apiOk(apiMiner{}))
	}
	var powers []model.HashPower
	// 按创建时间倒序，首条为最新账户，其 mode 作为展示口径
	if err := a.DB.Table(model.HashPowerTable).Order("created_at DESC").Find(&powers, "address = ?", address).Error; err != nil {
		return c.Send(apiErr("查询挖矿失败"))
	}
	out := apiMiner{Mode: "default", Created: time.Now().UnixMilli()}
	for i, p := range powers {
		out.Hashrate += p.Power
		out.PayoutTotal += p.TotalReward
		out.Claimable += p.RemainReward
		if i == 0 {
			out.Mode = apiMinerModeName(p.Mode)
			out.Created = p.CreatedAt.UnixMilli()
		}
	}
	out.Hashrate = apiFloat(out.Hashrate)
	out.PayoutTotal = apiFloat(out.PayoutTotal)
	out.Claimable = apiFloat(out.Claimable)
	out.PayoutTarget = apiFloat(out.Hashrate * 3)
	return c.Send(apiOk(out))
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
	if req.FeeToken != enum.FeeSymbol {
		return c.Send(apiErr("手续费代币须为 " + enum.FeeSymbol))
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
	if point.Amount < withdrawDecimal {
		return c.Send(apiErr("余额不足"))
	}
	// 手续费：提币价值(U) × 3% / 手续费币价(U) → 所需 TM 数量
	feeAmount := req.Amount * apiTokenPrice(req.Asset) * apiFeeRate / apiTokenPrice(enum.FeeSymbol)
	feeDecimal := uint64(feeAmount * symbolDictValue(enum.FeeSymbol))
	var feePoint model.WalletPoint
	if err := a.DB.Table(model.WalletPointTable).First(&feePoint, "address = ? AND symbol = ?", req.Address, enum.FeeSymbol).Error; err != nil {
		return c.Send(apiErr("需要手续费 " + enum.FeeSymbol))
	}
	if feePoint.Amount < feeDecimal {
		return c.Send(apiErr("手续费 " + enum.FeeSymbol + " 不足"))
	}
	// 记账事务：扣主币 + 扣手续费 + 双流水（本地提币，未链上广播）
	txHash := fmt.Sprintf("local-withdraw-%d", time.Now().UnixMilli())
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := setWalletPointAmount(tx, point.ID, point.Amount-withdrawDecimal); err != nil {
			return err
		}
		if err := setWalletPointAmount(tx, feePoint.ID, feePoint.Amount-feeDecimal); err != nil {
			return err
		}
		// 提币流水 + 手续费流水（手续费 Type=Fee，修复笔误后的正确语义）
		if err := tx.Table(model.WalletTxTable).Create(makeWalletTx(req.Asset, wallet.Address, withdrawDecimal, enum.BlockOut, enum.BlockOutText, txHash)).Error; err != nil {
			return err
		}
		return tx.Table(model.WalletTxTable).Create(makeWalletTx(enum.FeeSymbol, wallet.Address, feeDecimal, enum.Fee, enum.FeeText, txHash)).Error
	})
	if err != nil {
		return c.Send(apiErr(err.Error()))
	}
	return c.Send(apiOk(fiber.Map{
		"txHash":    txHash,
		"amount":    apiFloat(req.Amount),
		"feeAmount": apiFloat(feeAmount),
		"feeToken":  enum.FeeSymbol,
		"balance":   apiFloat(float64(point.Amount-withdrawDecimal) / symbolDictValue(req.Asset)),
	}))
}

// makeWalletTx 构造一条 wallet_tx（本地记账流水）
func makeWalletTx(symbol, address string, amount uint64, txType uint, desc, hash string) *model.WalletTx {
	// 本地记账：出账目标即自身（链上提币上线后替换为真实目标地址）
	return &model.WalletTx{
		Symbol: symbol, From: address, To: address,
		Amount: amount, Desc: desc, Type: txType, Hash: hash, Success: 1,
	}
}

/* ================= 9. POST /api/miner/claim 领取挖矿产出（入账 FIBO） ================= */
func (a *ApiAdapter) ClaimMiner(c *fiber.Ctx) error {
	var req apiClaimReq
	if err := c.BodyParser(&req); err != nil {
		return c.Send(apiErr("参数错误"))
	}
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
