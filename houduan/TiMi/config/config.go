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
	"strings"
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
	"TM":   1_0000_0000, //N次方需求#9/TM加速（3.4U/枚）：手续费币种或算力模式2可切 TM 时的精度
}

var SymbolIconDictionary = map[string]string{
	"USDT": "https://static.fibo.work/resource/FIBO/icon/usdt.png",
	"FIBO": "https://static.fibo.work/resource/FIBO/icon/fibo.png",
	"KTO":  "https://static.fibo.work/resource/FIBO/icon/kto.png",
	"BOFI": "https://static.fibo.work/resource/FIBO/icon/bofi.png",
	"FUSD": "https://static.fibo.work/resource/FIBO/icon/fusd.png",
	"TM":   "https://static.fibo.work/resource/FIBO/icon/fibo.png", //TM 图标待补充（暂用占位）
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
	// Mode 运行模式：dev/test 注册 /ops/test/* 等测试路由；prod 不注册（复盘 Wave-4 收敛）
	Mode string `yaml:"Mode"`
	// ApiAuthKey /api 适配层鉴权密钥（请求头 X-API-Key）。
	// 空 = 不校验（本地开发/内测）；生产必须设置（千万次-技术方案.md 风险表：/api 无鉴权为生产前必须项）。
	ApiAuthKey string `yaml:"apiAuthKey"`

	Mysql struct {
		Dns string `yaml:"Dns"`
	} `yaml:"Mysql"`

	Redis struct {
		Addr     string `yaml:"Addr"`
		Password string `yaml:"Password"`
		Db       int    `yaml:"Db"`
	} `yaml:"Redis"`

	LogPath string `yaml:"LogPath"`
	KtoPool struct {
		Address string `yaml:"Address"`
		Private string `yaml:"Private"`
		// PrivateKeyEnv 私钥环境变量名（非空且该环境变量有值时优先于 Private）。
		// 生产应只配环境变量/KMS，yml 的 Private 留空 —— 写进 yml 就会随 git 历史一起泄漏。
		PrivateKeyEnv string `yaml:"privateKeyEnv"`
	} `yaml:"KtoPool"`
	TronPool struct {
		Address       string `yaml:"Address"`
		Private       string `yaml:"Private"`
		PrivateKeyEnv string `yaml:"privateKeyEnv"`
	} `yaml:"TronPool"`

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
		// Chain 销毁通道："kto"（默认，历史行为，走 KTO gRPC）/ "evm"（EVM ERC20 的 burn(uint256)）。
		// 千万次的手续费币 TM 是 ERC20，生产/EVM 私链都必须用 evm 通道；
		// 默认 kto 是为了不改动存量 KTO 链部署的行为。
		Chain string `yaml:"Chain"`
		// EvmFrom 销毁出资地址（手续费池/热钱包），必须是 TM 的持有者。
		// 留空时回落到 TronPool.Address（本地配置里它被当作资金池地址复用）。
		EvmFrom string `yaml:"EvmFrom"`
		// EvmPrivate 销毁出资地址私钥（EVM 通道需要）。
		// 生产应由 KMS / 环境变量注入（PrivateKeyEnv），**不要写进 yml**（yml 已入库）。
		EvmPrivate string `yaml:"EvmPrivate"`
		// PrivateKeyEnv 私钥环境变量名（非空且该环境变量有值时优先于 EvmPrivate）
		PrivateKeyEnv string `yaml:"privateKeyEnv"`
	} `yaml:"Burn"`

	// Params 运行参数覆盖（迭代0 参数化，见 config/params.go；0/空 = 不覆盖，用代码默认值）
	Params NpowerParams `yaml:"Params"`

	// Ops 运维后台登录凭据（复盘 Wave-3：默认 blx / blxup2022city，建议部署时改 yml）
	Ops struct {
		Account  string `yaml:"Account"`
		Password string `yaml:"Password"`
	} `yaml:"Ops"`

	// Callback 充值回调配置（复盘 Wave-3）：Secret 非空时 trshash /callback 要求请求头
	// X-Callback-Key 一致；留空保持兼容但不安全（启动会 Warn 提示）。
	Callback struct {
		Secret string `yaml:"Secret"`
	} `yaml:"Callback"`

	// ==================== 生产加固配置（2026-09-18 新增）====================

	// Chain 链服务端点。原先 KTO gRPC 地址硬编码在 utils/kto/kto.go，
	// FIBO(EVM) 相关地址无配置位；生产部署必须可配，禁止把节点地址写进代码。
	Chain struct {
		KtoGrpcAddr   string `yaml:"KtoGrpcAddr"`   // KTO 链 gRPC 节点 host:port
		FiboRpcUrl    string `yaml:"FiboRpcUrl"`    // FIBO(EVM) JSON-RPC
		FiboChainId   int64  `yaml:"FiboChainId"`   // FIBO chainId（默认 12306）
		TmContract    string `yaml:"TmContract"`    // TM 代币合约地址（contracts 部署后填；空=未部署）
		Confirmations uint64 `yaml:"Confirmations"` // 充值到账确认数（B 模块充值监听用）

		// WatchToken / WatchPool 充值监听的默认目标（cmd/evmwatch 的命令行 -token/-pool 优先）。
		// ⚠ WatchPool 是**充值收款地址**，应当与 Withdraw.HotWalletPrivate 对应的出金热钱包**分离**：
		//   两者同址时，链上每笔出金都是 pool→用户，监听必须靠 from==pool 过滤才不会误当充值
		//   （已实现并验证，但分离地址才是生产口径）。
		WatchToken string `yaml:"WatchToken"`
		WatchPool  string `yaml:"WatchPool"`
		// DepositCreditTo 充值入账对象："" / "recipient"（默认）= 记到**收款地址**对应的账户；
		// "sender" = 记到**付款地址**对应的账户。这决定了充值模型：
		//   recipient —— 每个用户一个专属充值地址（用户从外部钱包/交易所提到自己地址）；
		//   sender    —— 所有人共用一个收款池（DApp 给所有用户展示同一个地址）。
		// ⚠ 用错会让「所有用户的充值都记到池子那一个账户」上，属资金归属错误。
		//   安全性：evmwatch 已过滤 from==pool / 自转 / mint，故 sender 口径不会把出金当充值。
		DepositCreditTo string `yaml:"DepositCreditTo"`
		// WatchStartBlock 充值监听的**起始块高**（首次运行且无进度时从这里开始扫）。
		// 生产应设为 TM 合约部署高度（或开始接收充值的高度）——否则只能靠 -fromBlock 命令行参数。
		WatchStartBlock uint64 `yaml:"WatchStartBlock"`
		// AllowStartFromLatest 仅联调用的逃生开关：无进度且未配起始块时，允许从"当前安全块"开始
		// （等于**放弃补扫历史充值**）。默认 false = fail-closed：拒绝启动并提示配置，
		// 避免上线后才发现"上线前的充值全都没入账"这种资金对不上的事故。
		AllowStartFromLatest bool `yaml:"AllowStartFromLatest"`
	} `yaml:"Chain"`

	// Idempotency 请求级幂等（C 模块，见 http/handler/idempotency.go）
	Idempotency struct {
		RequireKey bool `yaml:"RequireKey"` // true = 写请求必须带 Idempotency-Key 头（默认 false 兼容存量客户端）
		TTLSeconds int  `yaml:"TTLSeconds"` // 幂等记录保留时长（默认 600s）
	} `yaml:"Idempotency"`

	// Http 反向代理与真实客户端 IP（2026-09-30 新增）。
	//
	// 背景：生产按部署清单走 Nginx 反代（→ 127.0.0.1:3000）。fiber 默认取 `c.IP()` 的直连地址，
	// 反代之后它永远是 **Nginx 的地址** → 按 IP 限流退化成"全局单桶"（读 240/分、写 60/分），
	// 既会误伤全体用户，又让"单 IP 限流"名不副实（部署清单 §7 正是反代拓扑）。
	//
	// ⚠ 安全边界：只有**确实在可信反代之后**才能开启，否则客户端可自行伪造 XFF 绕过限流。
	//   因此要求 ProxyHeader 与 TrustedProxies **同时**配置才生效（见 cmd/main.go 的装配逻辑）；
	//   只配 ProxyHeader 会被忽略并打警告，避免"一配就变成可伪造 IP"。
	Http struct {
		// ProxyHeader 例如 "X-Forwarded-For"（fiber 的 ProxyHeader 配置项）
		ProxyHeader string `yaml:"ProxyHeader"`
		// TrustedProxies 允许携带该头的代理地址列表，建议只写本机回环（"127.0.0.1"）
		TrustedProxies []string `yaml:"TrustedProxies"`
	} `yaml:"Http"`

	// Auth 钱包签名登录（见 http/handler/auth.go）。0/空 = 用 applyDefaults 的兜底值。
	Auth struct {
		NonceTTLSeconds   int    `yaml:"NonceTTLSeconds"`   // 签名 nonce 有效期（默认 300s）
		SessionTTLSeconds int    `yaml:"SessionTTLSeconds"` // 会话票据有效期（默认 604800s = 7 天）
		ChainId           int64  `yaml:"ChainId"`           // 签名域 chainId（默认取 Chain.FiboChainId）
		Brand             string `yaml:"Brand"`             // 签名域品牌名（默认 "TiMi"）
		RequireInviteCode bool   `yaml:"RequireInviteCode"` // 新用户首登是否必须带邀请码（默认 false）
		EnforceSession    bool   `yaml:"EnforceSession"`    // true 时 /api 读接口也强制会话校验（默认 false；写接口始终强制）
		// AllowUnsignedWrite 仅联调开关：允许**无会话**的 /api 写请求沿用 body 里的地址。
		//
		// 为什么需要它：私链验证脚本用 SQL 直接造几百个"只有地址、没有私钥"的参与者，
		// 无法走钱包签名登录；关掉这个开关脚本就没法参投（生产 DApp 不受影响，它一定带会话票）。
		// 安全边界：Mode=prod 时**强制忽略**该开关（见 handler.authAllowUnsignedWrite），
		// 因此生产不可能因为误配它而重新打开"以任意地址操作钱包"的口子。
		AllowUnsignedWrite bool `yaml:"AllowUnsignedWrite"`
	} `yaml:"Auth"`

	// Withdraw 提现出金（B 模块）。**默认 Mode="ledger"（仅记账），与改造前行为完全一致**，
	// 出金上链是显式开启的能力，避免"改个配置就动真钱"。
	// ⚠ 待 owner 拍板：出金模式（热钱包自动 / 冷钱包人工 / 代付服务）与提现审批流程。
	Withdraw struct {
		// Mode: "ledger"（默认，仅记账）| "broadcast"（链上广播）
		Mode string `yaml:"Mode"`
		// ChainAsset 允许链上出金的资产符号（如 "FIBO" / "TM"）。
		// **broadcast 模式下必填**：不填则拒绝所有链上出金（fail-closed），
		// 避免"用 USDT 提现却转出 TM 合约"这类资产错配（2026-09-19 私链实测暴露）。
		ChainAsset string `yaml:"ChainAsset"`
		// HotWalletPrivate 出金热钱包私钥（仅 broadcast 模式需要）。
		// ⚠ **仅用于本地/私链联调**：yml 已入库，写进去等于随 git 历史泄漏。
		// 生产用 HotWalletKeyEnv 指定环境变量注入（见下），或改用 KMS/外部签名服务。
		HotWalletPrivate string `yaml:"HotWalletPrivate"`
		// HotWalletKeyEnv 出金热钱包私钥的**环境变量名**（推荐，仅 broadcast 模式需要）。
		// 设置且该环境变量非空时优先于 HotWalletPrivate —— 私钥不进 yml、不进 git 历史。
		// 例：HotWalletKeyEnv: "TITI_HOTWALLET_PRIVATE"
		// 值为 0x 前缀的十六进制私钥（有无 0x 均可，取用方自行 TrimPrefix）。
		HotWalletKeyEnv string `yaml:"HotWalletKeyEnv"`
		// TokenDecimals 出金代币精度（默认 8，与 TM/FIBO 记账精度一致）
		TokenDecimals uint8 `yaml:"TokenDecimals"`
		// TokenContract 出金代币合约地址（broadcast 模式必填）
		TokenContract string `yaml:"TokenContract"`
		// AssetContracts 多资产出金合约映射（可选）：资产符号 → ERC20 合约地址。
		// 配置后按提现资产选择合约，ChainAsset 只作为缺省/兜底；
		// 未命中映射的资产一律拒绝（绝不用别的代币顶替）。
		AssetContracts map[string]string `yaml:"AssetContracts"`
		// AssetDecimals 多资产精度映射（可选）：资产符号 → decimals。
		// 未配置时回落到 TokenDecimals。
		AssetDecimals map[string]uint8 `yaml:"AssetDecimals"`
	} `yaml:"Withdraw"`
}

// 链服务默认值（yml 未配置时兜底；生产必须在 yml 显式配置）
const (
	DefaultKtoGrpcAddr   = "106.12.94.134:8545" //历史节点，仅供本地/兼容兜底
	DefaultFiboRpcUrl    = "https://network.hzroc.art"
	DefaultFiboChainId   = int64(12306)
	DefaultConfirmations = uint64(12)
)

// 签名登录默认值（yml 未配置时兜底）
const (
	DefaultAuthNonceTTLSeconds   = 300    //5 分钟
	DefaultAuthSessionTTLSeconds = 604800 //7 天
	DefaultAuthBrand             = "TiMi"
)

// applyDefaults 填充未配置项的兜底值（空字符串/0 = 未配置）。
func applyDefaults() {
	c := &EtcConfig.Chain
	if c.KtoGrpcAddr == "" {
		c.KtoGrpcAddr = DefaultKtoGrpcAddr
	}
	if c.FiboRpcUrl == "" {
		c.FiboRpcUrl = DefaultFiboRpcUrl
	}
	if c.FiboChainId == 0 {
		c.FiboChainId = DefaultFiboChainId
	}
	if c.Confirmations == 0 {
		c.Confirmations = DefaultConfirmations
	}

	//签名登录兜底（ChainId 依赖上面的 Chain 兜底，故放在其后）
	a := &EtcConfig.Auth
	if a.NonceTTLSeconds <= 0 {
		a.NonceTTLSeconds = DefaultAuthNonceTTLSeconds
	}
	if a.SessionTTLSeconds <= 0 {
		a.SessionTTLSeconds = DefaultAuthSessionTTLSeconds
	}
	if a.ChainId == 0 {
		a.ChainId = c.FiboChainId
	}
	if a.Brand == "" {
		a.Brand = DefaultAuthBrand
	}
	//RequireInviteCode / EnforceSession 的 false 即约定的默认值，无需兜底
}

// HotWalletPrivateKey 取出生金热钱包私钥（原样返回，调用方按需 TrimPrefix("0x")）。
//
// 取值优先级：环境变量（名字由 Withdraw.HotWalletKeyEnv 指定）> yml 的 Withdraw.HotWalletPrivate。
// 这样生产可以把私钥只放在环境变量/KMS 里，yml 保持为空 —— yml 一旦写死就随 git 历史一起泄漏。
// 两者都为空时返回空串，调用方（broadcastWithdraw / hotWalletAddress / evmwatch 体检）按"未配置"处理。
func HotWalletPrivateKey() string {
	if EtcConfig == nil {
		return ""
	}
	if name := strings.TrimSpace(EtcConfig.Withdraw.HotWalletKeyEnv); name != "" {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
	}
	return strings.TrimSpace(EtcConfig.Withdraw.HotWalletPrivate)
}

// PoolPrivateKey 资金池私钥统一取值口：环境变量优先，yml 明文兜底（兼容本地联调）。
//
// 与 HotWalletPrivateKey 同一纪律：生产把私钥放环境变量/KMS，yml 留空。
// 仓库里历史 yml 已含明文私钥，部署前必须轮换（见 千万次-生产部署清单.md §3 密钥纪律）。
func PoolPrivateKey(envName, ymlValue string) string {
	if name := strings.TrimSpace(envName); name != "" {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
	}
	return strings.TrimSpace(ymlValue)
}

// KtoPoolPrivateKey KTO 资金池私钥（环境变量优先）
func KtoPoolPrivateKey() string {
	if EtcConfig == nil {
		return ""
	}
	return PoolPrivateKey(EtcConfig.KtoPool.PrivateKeyEnv, EtcConfig.KtoPool.Private)
}

// TronPoolPrivateKey TRON 资金池私钥（环境变量优先）
func TronPoolPrivateKey() string {
	if EtcConfig == nil {
		return ""
	}
	return PoolPrivateKey(EtcConfig.TronPool.PrivateKeyEnv, EtcConfig.TronPool.Private)
}

// BurnPrivateKey 手续费销毁出资私钥（环境变量优先）
func BurnPrivateKey() string {
	if EtcConfig == nil {
		return ""
	}
	return PoolPrivateKey(EtcConfig.Burn.PrivateKeyEnv, EtcConfig.Burn.EvmPrivate)
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

		//链服务端点兜底（空值填充默认，见 applyDefaults）
		applyDefaults()

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
