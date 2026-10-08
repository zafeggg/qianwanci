package main

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/core/impl"
	"com.fibonacci.crowd/http/handler"
	"com.fibonacci.crowd/model"
	"com.fibonacci.crowd/utils"
	"flag"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	jwtware "github.com/gofiber/jwt/v3"
	log "github.com/sirupsen/logrus"
)

func main() {
	path := flag.String("f", "/Users/zhengjianfeng_1/go/src/crowd/config/ops.yml", "-f 指定配置文件")
	//-port 覆盖 yml 的 HttpPort：ops 与 npower 常共用同一份配置，
	//不覆盖就会抢同一个端口（私链联调时 npower 占 3000，ops 需要另起 5000）。
	portFlag := flag.String("port", "", "-port 覆盖监听端口（默认取 yml 的 HttpPort）")
	flag.Parse()
	config.Init(*path) //mysql, redis, config file
	listenAddr := config.EtcConfig.HttpPort
	if *portFlag != "" {
		listenAddr = *portFlag
	}

	//确保 config 表存在 params 列（npower 未先启动时 ops 也能独立维护运行参数）
	if err := config.MysqlDBPool.AutoMigrate(&model.Config{}); err != nil {
		log.WithFields(log.Fields{"err": err}).Warnln("自动迁移 config 表失败")
	}
	//E 模块：运维操作审计表（每次 ops 调用落一行，见 http/handler/ops_audit.go）
	if err := config.MysqlDBPool.AutoMigrate(&model.OpsAudit{}); err != nil {
		log.WithFields(log.Fields{"err": err}).Warnln("自动迁移 ops_audit 表失败（审计将无法落库）")
	}
	//D 模块：风控名单表（黑/白名单）
	if err := config.MysqlDBPool.AutoMigrate(&model.RiskAddress{}); err != nil {
		log.WithFields(log.Fields{"err": err}).Warnln("自动迁移 risk_address 表失败（风控名单不可用）")
	}
	impl.LoadNpowerParams() //迭代0：加载运行参数（等级门槛等）
	impl.WatchNpowerParamsReload()
	model.WatchRiskChange() //D 模块：订阅风控名单变更广播（ops 自身也会收到自己发的广播，无害）

	app := fiber.New(fiber.Config{ErrorHandler: func(ctx *fiber.Ctx, err error) error {
		//custom error handler
		return ctx.Send(utils.JsonFail(err.Error()))
	}})
	app.Use(recover.New())
	app.Use(cors.New())
	app.Use(logger.New())

	jwtWare := jwtware.New(jwtware.Config{
		SigningKey: []byte(config.EtcConfig.SecretKey),
		Filter: func(ctx *fiber.Ctx) bool {
			//⚠ 2026-09-30 修复鉴权绕过：原实现用 strings.Contains(ctx.Request().URI().String(), "/ops/data")，
			//而 URI().String() = FullURI()（含查询串）→ 任何人加 `?x=/ops/data` 就能跳过 JWT，
			//未认证调用 /ops/params、/ops/setLevel、/ops/endRound 等高危路由（已实测复现 200）。
			//改为按**精确路径**白名单放行。
			switch ctx.Path() {
			case "/ops/sign", "/ops/data/zmy", "/ops/data/zmy2":
				return true
			}
			return false
		},
	})

	opsHandler := handler.NewOpsHandler()

	//ops 运维（E 模块：挂操作审计中间件，17 条高危写路由全部留痕）
	self := app.Group("/ops", handler.OpsAuditMiddleware()).Use(jwtWare)
	self.Post("/sign", opsHandler.Sign)
	self.Post("/startProject", opsHandler.StartProject)
	self.Post("/setRound", opsHandler.SetRound)
	self.Post("/endRound", opsHandler.EndRound)

	self.Post("/batchCreateWallet", opsHandler.BatchCreateWallet)
	self.Post("/setLevel", opsHandler.SetLevel)
	self.Post("/initSupperWallet", opsHandler.SupperWallet)

	//迭代0：运行参数维护 + 算力矿机模式运营设置
	self.Get("/params", opsHandler.GetParams)
	self.Post("/params", opsHandler.SetParams)
	self.Post("/setHashPowerMode", opsHandler.SetHashPowerMode)

	//复盘 Wave-4：生产模式（Mode=prod）不注册测试/占位路由
	if config.EtcConfig.Mode != "prod" {
		self.Post("/fullVote", opsHandler.FullVote)
		self.Post("/test/accounts", opsHandler.TestAccounts)
		self.Post("/test/charge", opsHandler.Charge)
	}

	//数据分析
	self.Get("/data/zmy", opsHandler.GetDataZmy)
	self.Get("/data/zmy2", opsHandler.GetDataZmy2)

	//D/E 模块：风控名单管理 + 运维操作审计查询（同样受 JWT + 审计中间件保护）
	self.Post("/risk/add", opsHandler.RiskAdd)
	self.Post("/risk/remove", opsHandler.RiskRemove)
	self.Get("/risk/list", opsHandler.RiskList)
	self.Get("/audit/list", opsHandler.AuditList)

	log.Fatal(app.Listen(listenAddr))
}
