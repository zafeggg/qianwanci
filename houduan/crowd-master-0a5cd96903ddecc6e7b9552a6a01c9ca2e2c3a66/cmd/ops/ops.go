package main

import (
	"com.fibonacci.crowd/config"
	"com.fibonacci.crowd/http/handler"
	"com.fibonacci.crowd/utils"
	"flag"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	jwtware "github.com/gofiber/jwt/v3"
	log "github.com/sirupsen/logrus"
	"strings"
)

func main() {
	path := flag.String("f", "/Users/zhengjianfeng_1/go/src/crowd/config/ops.yml", "-f 指定配置文件")
	flag.Parse()
	config.Init(*path) //mysql, redis, config file

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
			if strings.Contains(ctx.Request().URI().String(), "/ops/sign") ||
				strings.Contains(ctx.Request().URI().String(), "/ops/data") {
				return true
			}
			return false
		},
	})

	opsHandler := handler.NewOpsHandler()

	//ops 运维
	self := app.Group("/ops").Use(jwtWare)
	self.Post("/sign", opsHandler.Sign)
	self.Post("/startProject", opsHandler.StartProject)
	self.Post("/setRound", opsHandler.SetRound)
	self.Post("/endRound", opsHandler.EndRound)

	self.Post("/batchCreateWallet", opsHandler.BatchCreateWallet)
	self.Post("/setLevel", opsHandler.SetLevel)
	self.Post("/initSupperWallet", opsHandler.SupperWallet)

	self.Post("/fullVote", opsHandler.FullVote)
	self.Post("/test/accounts", opsHandler.TestAccounts)
	self.Post("/test/charge", opsHandler.Charge)

	//数据分析
	self.Get("/data/zmy", opsHandler.GetDataZmy)
	self.Get("/data/zmy2", opsHandler.GetDataZmy2)

	log.Fatal(app.Listen(config.EtcConfig.HttpPort))
}
