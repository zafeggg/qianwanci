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
)

func main() {
	path := flag.String("f", "/Users/eloise/Documents/GitHub/crowd/config/etc.yml", "-f 指定配置文件")
	flag.Parse()
	config.Init(*path) //mysql, redis, config file

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
		model.HashPower{}, //N次方新增：爆仓算力矿机账户表
		model.FeeBurn{}); err != nil { //N次方新增：手续费销毁记录表
		log.Printf("automigrate table error: %v \n", err)
	}

	walletHandler := handler.NewWalletHandler()
	homeHandler := handler.NewHomeHandler()
	selfHandler := handler.NewSelfHandler()
	miningHandler := handler.NewMiningMachineHandler()
	captchaHandler := handler.NewCaptchaHandler()
	hashPowerHandler := handler.NewHashPowerHandler() //N次方新增：算力矿机
	apiAdapter := handler.NewApiAdapter()             //千万次新增：/api 纯 JSON 适配层（前端联调）

	app := fiber.New(fiber.Config{ErrorHandler: func(ctx *fiber.Ctx, err error) error {
		//custom error handler
		if err.Error() == "InvalidTokenWallet" {
			return ctx.Status(http.StatusUnauthorized).Send(utils.JsonFail("已在其他设备登录"))
		}

		return ctx.Send(utils.JsonFail(err.Error()))
	}})
	app.Use(recover.New())
	app.Use(logger.New())
	app.Use(cors.New())

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

	//hashpower 算力矿机 相关（N次方新增：爆仓补偿折算算力，与 /mining 独立）
	hashPower := app.Group("/hashpower").Use(jwtWare).Use(AesMiddleware())
	//我的算力矿机（账户列表 + 汇总）
	hashPower.Get("/home", hashPowerHandler.Home)
	//提取矿机产出（FIBO）
	hashPower.Post("/withdraw", hashPowerHandler.Withdraw)

	//千万次新增：/api 适配层（纯 JSON、无 RSA/JWT 中间件），供前端 DApp 联调
	api := app.Group("/api")
	api.Get("/rounds", apiAdapter.Rounds)
	api.Get("/rounds/current", apiAdapter.CurrentRound)
	api.Get("/assets", apiAdapter.Assets)
	api.Get("/user", apiAdapter.User)
	api.Get("/miner", apiAdapter.Miner)
	api.Get("/prices", apiAdapter.Prices)
	api.Post("/participate", apiAdapter.Participate)
	api.Post("/withdraw", apiAdapter.Withdraw)
	api.Post("/miner/claim", apiAdapter.ClaimMiner)

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
