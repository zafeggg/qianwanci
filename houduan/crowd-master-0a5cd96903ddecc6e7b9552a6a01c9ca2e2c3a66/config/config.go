package config

import (
	"context"
	"fmt"
	"github.com/go-redis/redis/v8"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"gorm.io/driver/mysql"
	_ "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
	"io"
	"io/ioutil"
	"log"
	"os"
	"sync"
)

var SymbolDictionary = map[string]float64{
	"":     1,
	"USDT": 1_000000,
	"FIBO": 1_0000_0000,
	"KTO":  1_000_0000_0000,
	"FUSD": 1_0000_0000,
	"BOFI": 1_0000_0000,
	"PCB":  1_0000_0000,
	"TM":   1_0000_0000, // TiMi 手续费币（enum.FeeSymbol），精度同 BOFI 取 1e8
}

var SymbolIconDictionary = map[string]string{
	"USDT": "https://static.fibo.work/resource/FIBO/icon/usdt.png",
	"FIBO": "https://static.fibo.work/resource/FIBO/icon/fibo.png",
	"KTO":  "https://static.fibo.work/resource/FIBO/icon/kto.png",
	"BOFI": "https://static.fibo.work/resource/FIBO/icon/bofi.png",
	"FUSD": "https://static.fibo.work/resource/FIBO/icon/fusd.png",
	// TM 图标占位：资源上传后替换为实际路径（沿用 static.fibo.work 目录规范）
	"TM": "https://static.fibo.work/resource/FIBO/icon/tm.png",
}

var (
	EtcConfig   *YmlConfig
	MysqlDBPool *gorm.DB
	RedisClient *redis.Client
	once        sync.Once
)

type YmlConfig struct {
	HttpPort  string `yaml:"HttpPort"`
	SecretKey string `yaml:"secretKey"`

	Mysql struct {
		Dns string `yaml:"Dns"`
	} `yaml:"Mysql"`

	Redis struct {
		Addr     string `yaml:"Addr"`
		Password string `yaml:"Password"`
		Db       int    `yaml:"Db"`
	} `yaml:"Redis"`

	TronNode struct {
		Address string `yaml:"Address"`
		ApiKey  string `yaml:"ApiKey"`
	} `yaml:"TronNode"`
	LogPath string `yaml:"LogPath"`
	KtoPool struct {
		Address string `yaml:"Address"`
		Private string `yaml:"Private"`
	} `yaml:"KtoPool"`
	TronPool struct {
		Address string `yaml:"Address"`
		Private string `yaml:"Private"`
	} `yaml:"TronPool"`
	BofiPool struct {
		Address string `yaml:"Address"`
		Private string `yaml:"Private"`
	} `yaml:"BofiPool"`

	MinePool struct{
		Address string `yaml:"Address"`
		Private string `yaml:"Private"`
	} `yaml:"MinePool"`

	TronNetwork struct {
		Host string `yaml:"Host"`
	} `yaml:"TronNetwork"`
	MiningJob struct {
		SyncPcb   string `yaml:"SyncPcb"`
		MiningPcb string `yaml:"MiningPcb"`
	} `yaml:"MiningJob"`
	// Burn 手续费销毁配置（N次方新增）：Address 为链上黑洞地址（不支持 burn 的链用黑洞地址 + 公开审计）
	Burn struct {
		Address string `yaml:"Address"`
	} `yaml:"Burn"`
}

func Init(path string) {
	once.Do(func() {
		var bs []byte
		var err error
		if bs, err = ioutil.ReadFile(path); err != nil {
			panic(fmt.Errorf("read path: %v config err: %v", path, err))
		}

		//读取配置文件
		if err = yaml.Unmarshal(bs, &EtcConfig); err != nil {
			panic(fmt.Errorf("unmarshal file to yaml err: %v", err))
		}

		fmt.Println("read config: ", EtcConfig)

		//初始化数据库
		if MysqlDBPool, err = gorm.Open(mysql.Open(EtcConfig.Mysql.Dns), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Info),
			NamingStrategy: schema.NamingStrategy{
				SingularTable: true,
			}}); err != nil {
			panic(fmt.Errorf("connect mysql err: %v", err))
		}

		//初始化redis
		if EtcConfig.Redis.Addr != "" {
			RedisClient = redis.NewClient(&redis.Options{
				Addr:     EtcConfig.Redis.Addr,
				Password: EtcConfig.Redis.Password,
				DB:       EtcConfig.Redis.Db,
			})
			if err = RedisClient.Ping(context.Background()).Err(); err != nil {
				panic(fmt.Errorf("connect redis err: %v", err))
			}
		}

		//配置日志
		writer2 := os.Stdout
		writer3, err := os.OpenFile(EtcConfig.LogPath, os.O_WRONLY|os.O_CREATE, 0755)
		if err != nil {
			log.Fatalf("create file log.txt failed: %v", err)
		}
		wr := io.MultiWriter(writer2, writer3)
		logrus.SetOutput(wr)
		logrus.SetLevel(logrus.DebugLevel)
	})
}
