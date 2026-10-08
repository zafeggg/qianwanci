package main

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core/impl"
	"com.fibonacci.crowd/crypto"
	"com.fibonacci.crowd/enum"
	"com.fibonacci.crowd/http/handler"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils"
	"encoding/json"
	"flag"
	"github.com/go-redis/redis/v8"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	jwtware "github.com/gofiber/jwt/v3"
	log "github.com/sirupsen/logrus"
	"net/http"
	"strings"
	"time"
)

func main() {
	path := flag.String("f", "/Users/eloise/Documents/GitHub/crowd/config/etc.yml", "-f 指定配置文件")
	flag.Parse()
	config.Init(*path) //mysql, redis, config file

	//配置体检（fail-closed，2026-09-30 新增）：
	///api 的唯一静态凭据 X-API-Key 会随前端包一起下发（VITE_API_KEY 编译进 JS），
	//因此 Mode=prod 时它必须存在（挡住扫描器），且 secretKey 不能是仓库里的示例值。
	if strings.EqualFold(strings.TrimSpace(config.EtcConfig.Mode), "prod") {
		if strings.TrimSpace(config.EtcConfig.ApiAuthKey) == "" {
			log.Fatal("配置体检失败：Mode=prod 时必须配置 apiAuthKey（/api 组鉴权），拒绝启动")
		}
		if key := strings.TrimSpace(config.EtcConfig.SecretKey); key == "" || key == "dsajflkadsjglafsdj" || key == "privchain-local-secret" {
			log.Fatal("配置体检失败：Mode=prod 时 secretKey 仍是示例/空值（JWT 可被伪造），拒绝启动")
		}
		//明文私钥纪律：yml 已入库，生产应改为环境变量注入（*Pool.PrivateKeyEnv / Burn.PrivateKeyEnv）
		if v := strings.TrimSpace(config.EtcConfig.KtoPool.Private); v != "" {
			log.Warnln("安全警告：KtoPool.Private 为 yml 明文私钥，生产请改用 privateKeyEnv 环境变量注入并轮换密钥")
		}
		if v := strings.TrimSpace(config.EtcConfig.TronPool.Private); v != "" {
			log.Warnln("安全警告：TronPool.Private 为 yml 明文私钥，生产请改用 privateKeyEnv 环境变量注入并轮换密钥")
		}
		if v := strings.TrimSpace(config.EtcConfig.Burn.EvmPrivate); v != "" {
			log.Warnln("安全警告：Burn.EvmPrivate 为 yml 明文私钥，生产请改用 privateKeyEnv 环境变量注入并轮换密钥")
		}
	}

	//启动期数据清理：必须在 AutoMigrate **之前**——ProjectRound 新增唯一索引
	//uk_project_round_no(project_id, round)，历史库存在重复轮号时建索引会直接失败（err 1062）
	impl.DedupeProjectRounds()

	if err := config.MysqlDBPool.AutoMigrate(
		model.Config{},
		model.Mining{},
		model.MiningExchange{},
		model.MineMachine{},
		model.MiningExchangeReward{},
		model.Notify{},
		model.ProjectRound{},
		model.Project{},
		model.Vote{},
		model.VoteReward{},
		model.Wallet{},
		model.WalletTx{},
		model.WalletPoint{},
		model.WalletHashCharge{},
		model.WalletTree{},
		model.HashPower{},             //N次方新增：爆仓算力矿机账户表
		model.FeeBurn{}); err != nil { //N次方新增：手续费销毁记录表
		log.Printf("automigrate table error: %v \n", err)
	}

	//迭代0：加载运行参数（config 表 params 列 + yml Params 段；需在 AutoMigrate 之后保证列存在）
	impl.LoadNpowerParams()
	impl.WatchNpowerParamsReload() //复盘 Wave-4：订阅参数变更广播，运维改参免重启生效
	model.WatchRiskChange()        //D 模块：订阅风控名单变更广播，封停/解封秒级生效

	walletHandler := handler.NewWalletHandler()
	homeHandler := handler.NewHomeHandler()
	selfHandler := handler.NewSelfHandler()
	miningHandler := handler.NewMiningMachineHandler()
	captchaHandler := handler.NewCaptchaHandler()
	hashPowerHandler := handler.NewHashPowerHandler() //N次方新增：算力矿机
	apiAdapter := handler.NewApiAdapter()             //千万次新增：/api 纯 JSON 适配层（前端联调）

	//真实客户端 IP（生产经 Nginx 反代）：只有 ProxyHeader 与 TrustedProxies **同时**配置才生效。
	//软配就是"可被伪造 IP 绕过限流"，比不配更危险 —— 因此半配时忽略并告警（见 config.Http 注释）。
	fiberCfg := fiber.Config{ErrorHandler: func(ctx *fiber.Ctx, err error) error {
		//custom error handler
		if err.Error() == "InvalidTokenWallet" {
			return ctx.Status(http.StatusUnauthorized).Send(utils.JsonFail("已在其他设备登录"))
		}

		return ctx.Send(utils.JsonFail(err.Error()))
	}}
	if hdr := strings.TrimSpace(config.EtcConfig.Http.ProxyHeader); hdr != "" {
		if len(config.EtcConfig.Http.TrustedProxies) == 0 {
			log.Warnln("配置警告：Http.ProxyHeader 已配置但 Http.TrustedProxies 为空 —— 为避免伪造 XFF 绕过限流，本次将忽略该头（请在 TrustedProxies 写入反代地址，如 127.0.0.1）")
		} else {
			fiberCfg.ProxyHeader = hdr
			fiberCfg.EnableTrustedProxyCheck = true
			fiberCfg.TrustedProxies = config.EtcConfig.Http.TrustedProxies
			log.WithFields(log.Fields{"proxyHeader": hdr, "trustedProxies": config.EtcConfig.Http.TrustedProxies}).
				Infoln("已启用可信反代：按 X-Forwarded-For 解析真实客户端 IP（限流按真实 IP 分桶）")
		}
	}
	app := fiber.New(fiberCfg)
	app.Use(recover.New())
	app.Use(logger.New())
	app.Use(cors.New())
	app.Use(handler.MetricsMiddleware())     //F 模块：HTTP 指标采集
	app.Use(handler.RateLimitMiddleware())   //D 模块：单 IP 限流（/health、/metrics 豁免）
	app.Use(handler.RiskGuardMiddleware())   //D 模块：黑/白名单校验（仅写请求）
	app.Use(handler.IdempotencyMiddleware()) //C 模块：请求级幂等（写方法 + Idempotency-Key 头）

	//F 模块：健康检查与指标（不鉴权，供探针/Prometheus；生产建议在 Nginx 限制 /metrics 来源）
	app.Get("/health", handler.Health)
	app.Get("/metrics", handler.MetricsText)
	stopMetrics := handler.StartMetricsCollector(30 * time.Second)
	//行情采样：为算力保底口径的「4 小时均价」持续积累样本（需求#8；样本不足时自动回落实时价）
	impl.StartPriceSampler(10*time.Minute, enum.FIBO, impl.FeeSymbol)
	defer stopMetrics()

	jwtWare := jwtware.New(jwtware.Config{
		SigningKey: []byte(config.EtcConfig.SecretKey),
		Filter: func(ctx *fiber.Ctx) bool {
			if strings.Contains(ctx.Request().URI().String(), "/home/version") ||
				strings.Contains(ctx.Request().URI().String(), "/wallet/registration") ||
				strings.Contains(ctx.Request().URI().String(), "/wallet/recover") {
				return true
			}
			return false
		},
	})

	captcha := app.Group("/captcha")
	captcha.Get("/getCaptcha", captchaHandler.GetCaptcha)
	captcha.Get("/verifyCaptcha", captchaHandler.VerifyCaptcha)
	captcha.Get("/show/:source", captchaHandler.GetCaptchaPng)

	//home 首页相关
	home := app.Group("/home").Use(jwtWare).Use(AesMiddleware())
	//首页
	home.Get("/", homeHandler.Home)
	//期详情
	home.Get("/project/:projectId", homeHandler.ProjectDetail)
	//轮详情
	home.Get("/round/:roundId", homeHandler.RoundDetail)
	//众筹列表
	home.Get("/voteList", homeHandler.VoteList)
	//系统消息
	home.Get("/message", homeHandler.Message)

	//wallet 钱包相关
	wallet := app.Group("/wallet").Use(jwtWare).Use(AesMiddleware())
	//计算手续费
	wallet.Get("/fee/:symbol/:amount", walletHandler.Fee)
	//钱包注册
	wallet.Post("/registration", walletHandler.Registration)
	//导入钱包
	wallet.Post("/recover", walletHandler.Recover)
	//钱包信息
	wallet.Get("/info", walletHandler.Info)
	//钱包转账明细
	wallet.Get("/tx/:symbol/:in", walletHandler.Tx)
	//提币
	wallet.Post("/withdraw", walletHandler.Withdraw)
	//修改密码
	wallet.Post("/modifyPwd", walletHandler.ModifyPwd)
	//导出私钥
	wallet.Post("/exportPri", walletHandler.ExportPri)
	//投入
	wallet.Post("/vote", walletHandler.Vote)

	//self 我的相关
	self := app.Group("/self").Use(jwtWare).Use(AesMiddleware())
	//我的主页
	self.Get("/", selfHandler.Home)
	//我的社区
	self.Get("/community", selfHandler.Community)
	//邀请码
	self.Get("/invitedCode", selfHandler.InvitedCode)

	//矿机 相关
	mineMachine := app.Group("/mining").Use(jwtWare).Use(AesMiddleware())
	//首页
	mineMachine.Get("/home", miningHandler.Home)
	//算力明细
	mineMachine.Get("/detail", miningHandler.Detail)
	//挖矿记录
	mineMachine.Get("/record", miningHandler.Record)
	//兑换
	mineMachine.Post("/exchange", miningHandler.Exchange)
	//提取
	mineMachine.Post("/withdraw", miningHandler.Withdraw)

	//算力矿机 相关（N次方新增：爆仓补偿折算算力，与 /mining 独立）
	hashPower := app.Group("/hashpower").Use(jwtWare).Use(AesMiddleware())
	//我的算力矿机（账户列表 + 汇总）
	hashPower.Get("/home", hashPowerHandler.Home)
	//提取矿机产出（FIBO）
	hashPower.Post("/withdraw", hashPowerHandler.Withdraw)

	//千万次新增：/api 适配层（纯 JSON），供前端 DApp 联调；apiAuthKey 非空时强制 X-API-Key 鉴权
	api := app.Group("/api", handler.ApiKeyMiddleware())
	//会话中间件必须注册在路由之前（Fiber 按注册顺序匹配，写在路由之后不会对既有路由生效）
	api.Use(handler.SessionMiddleware())
	api.Get("/rounds", apiAdapter.Rounds)
	api.Get("/rounds/current", apiAdapter.CurrentRound)
	api.Get("/assets", apiAdapter.Assets)
	api.Get("/user", apiAdapter.User)
	api.Get("/miner", apiAdapter.Miner)
	api.Get("/prices", apiAdapter.Prices)
	//规则数值快照：前端据此覆盖本地默认值，避免"页面写的比例"与"后端实际结算比例"分叉
	api.Get("/rules", apiAdapter.Rules)
	api.Post("/participate", apiAdapter.Participate)
	api.Post("/withdraw", apiAdapter.Withdraw)
	api.Post("/miner/claim", apiAdapter.ClaimMiner)
	//算力出局模式自选（需求#8「算力：选择其中一种方式」）：用户侧真实切换，需钱包签名会话
	api.Post("/miner/mode", apiAdapter.MinerMode)
	//充值信息（收款地址/代币合约/精度/确认数）：前端做充值引导或一键充值时需要；只读、不含私钥
	api.Get("/deposit/info", apiAdapter.DepositInfo)

	//千万次-Auth：钱包签名登录（自身不鉴权，读取可选 Bearer 会话；/api/auth/* 不受 EnforceSession 拦截）
	api.Get("/auth/nonce", apiAdapter.AuthNonce)
	api.Post("/auth/login", apiAdapter.AuthLogin)
	api.Get("/auth/me", apiAdapter.AuthMe)

	home.Get("/version", func(c *fiber.Ctx) error {
		type versionJson struct {
			Version  string `json:"version"`
			Download string `json:"download"`
		}

		cacheKey := "NpowerVersionJson"
		var err error
		vj := new(versionJson)
		versionCmd := config.RedisClient.Get(c.Context(), cacheKey)
		versionRsl, err := versionCmd.Result()
		switch err {
		case nil:
			if err = json.Unmarshal([]byte(versionRsl), vj); err != nil {
				log.Error("unmarshal version json error")
				return c.Send(utils.JsonFail("检测版本更新失败"))
			}

		case redis.Nil:
			var configs model.Config
			if err = config.MysqlDBPool.Table(model.ConfigTable).First(&configs).Error; err != nil {
				return c.Send(utils.JsonFail("读取配置失败"))
			}

			if configs.Version == "" || configs.Download == "" {
				return c.Send(utils.JsonFail("版本配置未找到"))
			}

			vj.Version = configs.Version
			vj.Download = configs.Download

			var marshalVj []byte
			if marshalVj, err = json.Marshal(vj); err != nil {
				log.Error("marshal version json error")
				return c.Send(utils.JsonFail("读取版本配置失败"))
			}

			statusCmd := config.RedisClient.Set(c.Context(), cacheKey, marshalVj, 0)
			if statusCmd.Err() != nil {
				return c.Send(utils.JsonFail("缓存失败"))
			}
		default:
			return c.Send(utils.JsonFail("读取配置缓存失败"))
		}

		return c.Send(utils.JsonOk(vj))
	})
	//token price
	impl.RefreshFIBO20PricePrice()

	log.Fatal(app.Listen(config.EtcConfig.HttpPort))
}

func AesMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		aesSignHeader := c.Get("Sign")
		aesSign, err := crypto.RsaDecryptToString([]byte(aesSignHeader))
		if err != nil {
			log.Error("ip: ", c.IPs(), ", aes sign err: ", err)
			return c.Send(utils.JsonFail("签名错误"))
		}

		c.Context().SetUserValue(enum.AesSignContextKey, aesSign)
		return c.Next()
	}
}
