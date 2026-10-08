> 【历史参考】本文档为旧版 crowd-master（Npower）代码解析，其中第九节问题清单已在新基线修复（Invite 统一口径 / SetLevel / ChargePoint / Withdraw 流水类型），仅供追溯。

# crowd-master（Npower 众筹系统）代码解析报告

> 解析时间：2026-08-26 ｜ Go 1.17 ｜ 模块名 `com.fibonacci.crowd`

---

## 一、项目定位

这是一个部署在 **KTO / FIBO / TRON 三条链**上的**众筹游戏 + 算力挖矿**后端系统（Npower）。

- **众筹玩法**：项目（Project）分「期（Period）」，「期」内分「轮（Round）」。用户用代币参与每轮众筹，轮满目标即成功，按固定比例发静态收益并退还本金；轮不达标按规则退款（100% 或 50% + FUSD 积分补偿）。
- **挖矿玩法**：用户用 FUSD 积分兑换 PCB 算力，系统按个人算力占比分配每日 FIBO 挖矿奖励。
- **团队体系**：邀请码 + 钱包关系树（closure table），F1/F2/F3 三级等级与动态收益（分享收益 / 团队极差收益）。

## 二、技术栈

| 类别 | 选型 |
|---|---|
| 语言/版本 | Go 1.17 |
| HTTP 框架 | gofiber/fiber v2（主服务、运维服务、充值回调服务） |
| ORM | gorm + mysql（15 张表，AutoMigrate 自动建表） |
| 缓存/锁 | go-redis v8 + bsm/redislock（分布式锁） |
| 鉴权 | golang-jwt v4 + Redis token（单设备登录控制） |
| 加密 | RSA（请求签名密钥交换）+ AES-CBC（业务报文）+ bcrypt（密码） |
| 链交互 | KTO 链 gRPC（钱包/转账/查交易）；TRON 链 HTTP（USDT/TRX）；FIBO 主网 HTTP（挖矿接口） |
| 定时任务 | robfig/cron v3（round 轮次、mining 挖矿） |
| 协程池 | panjf2000/ants v2（结算、充值监听异步处理） |
| 日志 | sirupsen/logrus |

## 三、目录结构与模块职责

| 目录 | 职责 |
|---|---|
| `cmd/` | 7 个可执行程序入口（npower / round / trshash / collecting / mining / ops / manual） |
| `config/` | YAML 配置加载、MySQL/Redis 初始化、日志、币种精度字典 |
| `core/` | 业务接口定义 `intreface.go`（OnBoarding / Projecting / Rounding / Voting / Rewarding / Mining / WalletPointing） |
| `core/impl/` | 各接口的实现（钱包、轮次、投入、收益结算、挖矿、积分、价格） |
| `model/` | GORM 数据模型（15 张表）+ WalletModel 关系树查询 |
| `http/` | Fiber 路由（main.go 中注册）+ handler / req / resp |
| `enum/` | 常量枚举：状态机、等级、流水类型、币种、归集队列 |
| `crypto/` | RSA 加解密（硬编码公/私钥） |
| `utils/` | 通用工具 + `kto/`（KTO 链 gRPC 客户端）+ `tron/`（TRON 链 HTTP 客户端） |
| `kortho/` | KTO 链 SDK：ed25519 交易签名、Base58 地址、Merkle 树、函数式工具库 |

## 四、7 个可执行程序

| 程序 | 入口文件 | 作用 | 部署形态 |
|---|---|---|---|
| **npower** | `cmd/main.go` | 主服务：注册/登录/投入/提币/资产/矿机等全部用户 API；AutoMigrate 建表；价格定时刷新 | 常驻 HTTP（端口配置于 yml） |
| **round** | `cmd/round/round.go` | 轮次调度器：cron 每 2 分钟开始到期的轮；每 1 分钟结束到期的轮并异步结算 | 常驻定时任务 |
| **trshash** | `cmd/trshash/trshash.go` | 充值到账监听：轮询 KTO 浏览器接口 + TRON 链 API，发现新充值 hash 即入账；同时提供 `/callback` 接收波场回调 | 常驻 HTTP + 轮询 |
| **collecting** | `cmd/fundcollection/collecting.go` | 归集订阅：订阅 Redis 频道 `FundCollectionQueue`，把充值进用户钱包的代币转账归集到资金池地址 | 常驻消费者 |
| **mining** | `cmd/mining/mining.go` | 挖矿定时器：同步 PCB 到 FIBO 主网、拉取总算力、按算力占比发放每日 FIBO 奖励 | 常驻定时任务 |
| **ops** | `cmd/ops/ops.go` | 运维服务：项目/轮管理、批量建钱包、设等级、超级账号、数据统计 API | 常驻 HTTP |
| **manual** | `cmd/manualcollection/manual.go` | 手动归集工具：遍历全部钱包，把指定代币余额归集到资金池（一次性脚本） | 手动执行 |

> 另注：`cmd/fundcollection/u_collect.go` 为 USDT 归集扫描脚本，**未完成**（只检查了 TRX 余额，无实际转账逻辑）。

## 五、数据模型（15 张表）

| 表 | 模型 | 关键字段 | 说明 |
|---|---|---|---|
| `project` | Project | Period、Symbol、Status | 期（0未开始 1进行中 2停止） |
| `project_round` | ProjectRound | ProjectId、Round、TargetVote、MinVote/MaxVote、CurrentVote、StartTime/EndTime、Success、Status | 轮（0未开始 1进行中 2待结算 3已结束） |
| `vote` | Vote | RoundId、WalletId、IsAdmin、Level、Address、Amount、Count | 投入记录（超级账号 IsAdmin=true） |
| `vote_reward` | VoteReward | VoteId、DynamicTeam、DynamicShard、Static、Loss | 收益明细 |
| `wallet` | Wallet | Address/Private（KTO）、TronAddress/TronPrivate、Password、Sign、Level、Active、Admin、Code | 用户钱包（双链双地址） |
| `wallet_point` | WalletPoint | Address、Symbol、Amount(uint64) | 各币种余额（内部账本） |
| `wallet_tx` | WalletTx | From/To、Symbol、Amount、Hash、Success、Type | 资金流水（10+ 种类型） |
| `wallet_hash_charge` | WalletHashCharge | Address、Hash、Symbol | 充值 hash 去重表 |
| `wallet_tree` | WalletTree | Ancestor、Descendant、Distance | 邀请关系闭包表 |
| `mining` / `mining_exchange` / `mining_exchange_reward` | 挖矿三表 | 总/剩余收益、兑换记录（算力+膨胀系数+状态）、逐笔奖励 | 挖矿业务 |
| `mine_machine` | MineMachine | Fusd、Pcb、Name | 矿机模板 |
| `notify` / `config` | Notify / Config | 系统消息 / 版本配置 | 辅助 |

## 六、核心业务流程

### 1. 注册（WalletManager.Registration）
1. 同时创建 **TRON 钱包** 与 **KTO 钱包**（KTO 为主钱包）；
2. 密码 bcrypt 加盐；KTO 私钥用固定 `SECRET_KEY` AES 加密为 `Sign`（用于导入恢复）；
3. 校验邀请码 → 生成 8 位邀请码 → 建 `wallet` 记录 → `InviteWithTx` 写入关系树（复制父节点所有祖先记录 distance+1 + 自身节点）。

### 2. 充值入账（trshash → WalletManager.Charge）
1. trshash 轮询 KTO 浏览器接口（`GetTxsByAddr`，分页 100/页）与 TRON 链 API，比对 `wallet_hash_charge` 去重；
2. `Charge`：redislock 防并发 → 查/建 wallet_point → 事务内加余额、记 wallet_tx（入账）、写 hash 去重 → **发布归集消息到 Redis**（`FundCollectionQueue`）；
3. collecting 订阅消息：TRON 币先补 5 TRX 手续费（sleep 50s），再转 USDT 到资金池；KTO 币先补 2200 万 KTO 交易费，再转代币到资金池。

### 3. 投入众筹（VoteManager.Vote）
校验（轮状态=进行中、未过期、余额充足、非管理员受 min/max 限制、防止超募自动截断）→ 加锁（sync.Mutex）→ 事务：更新/创建 vote、轮进度 CurrentVote/Count、扣 wallet_point、记 wallet_tx。

### 4. 轮次结算（round → RoundManager.End → RewardManager）
- **先 100% 退还超级账号本金**（不参与收益分配）；
- **达标（CurrentVote ≥ TargetVote）**：对所有参与者 `Promote` 升级（F1=直推10人+伞下30人参与；F2=伞下3个F1；F3=伞下3个F2）→ 激活 → 结算前第 N-2 轮收益：
  - **静态收益** 13% 本金；
  - **动态分享** 一代 1.5%（需直推≥2人）、三代 2%（直推≥5人）、五代 2.5%（直推≥10人）；
  - **动态团队** 极差收益 0.5%/级（沿祖先路径按等级差发放）；
  - 同时 **100% 退还本金**，轮状态置为已结束；
- **不达标**：
  - 第 1、2 轮：全部 `FailedNormal`（100% 退还本金）；
  - 第 3 轮起：当前轮 100% 退，倒二、倒三轮 `Failed`（**50% 本金** + 等值 FUSD 积分补偿），项目停止。

### 5. 提币（WalletHandler.Withdraw）
校验密码/单笔上限（10000）/余额 → 手续费按 3% 折算成 **BOFI** 扣取 → Redis 限 24 小时 3 次 → 事务：扣 BOFI 手续费 + 扣币、KTO/TRON 上链转账、记两条流水。

### 6. 挖矿（mining 定时 + MiningManager）
1. 用户用 FUSD 兑换矿机 → `mining_exchange` 记录 PCB 算力与收益膨胀系数（默认 3 倍）；
2. mining 程序定时 `POST /syncCrowdPoolPcb` 同步众筹矿池 PCB → 定时 `GET /totalMiningPcb` 拿主网总算力 → 按「个人 PCB / 池 PCB / 主网总算力」比例分配每日 FIBO 奖励 `3498542274052 * 0.425`；
3. 收益累计超 FUSD×3 倍即停矿；用户提取最低 50 个 FIBO。

## 七、安全与并发设计

| 机制 | 实现 | 位置 |
|---|---|---|
| 报文加密 | `AesMiddleware`：请求头 `Sign`（前端用 RSA 公钥加密的 AES key）→ 服务端 RSA 私钥解密得 AES key → `crypto.AesDecrypt`（openssl 格式，前端 crypto-js 对应）解请求体 | cmd/main.go / crypto |
| 单设备登录 | JWT + Redis `Login_{walletId}` 比对，不匹配返回 401「已在其他设备登录」 | handler/common + utils/token |
| 余额并发安全 | `AddPointAmount` 乐观锁 CAS：`UPDATE ... SET amount=amount+X WHERE id=? AND amount=旧值`，失败重试 100 次 | core/impl/walletpoint.go |
| 充值幂等 | redislock 分布式锁 + wallet_hash_charge 唯一化 | wallet.go Charge |
| 投入串行化 | VoteManager / RewardManager 内 `sync.Mutex` | vote.go / reward.go |
| 金额精度 | 内部统一 `uint64` 最小单位，字典 `SymbolDictionary`（USDT=1e6、FIBO=1e8、KTO=1e12）换算 | config/config.go |

## 八、关键常量（core/impl/config.go）

| 常量 | 值 | 含义 |
|---|---|---|
| StaticRewardRate | 0.13 | 静态收益率 |
| DynamicShardReward1/3/5GenerateRate | 0.015 / 0.02 / 0.025 | 一代/三代/五代分享收益 |
| DynamicTeamRewardDiffRate | 0.005 | 团队极差收益/级 |
| LossRate | 0.5 | 倒二倒三轮损失比例 |
| MinRound | 2 | 最低玩法轮数 |
| FeeRate | 0.03 | 提币手续费率 |
| MiningRewardBuff | 3 | 挖矿收益上限倍数 |
| MiningWithdrawMinCount | 50 | 挖矿最低提取数 |
| F1DirectCount / F1VoteCount / F1ToF2Count / F2ToF3Count | 10 / 30 / 3 / 3 | 等级晋升条件 |
| RewardAddress / VoteAddress | 同一 KTO 地址 | 奖励转出 / 投入转入 |

## 九、值得注意的问题点（潜在 Bug / 风险）

1. **`cmd/fundcollection/u_collect.go` 是半成品**：只做了 TRX 余额检查（且判断方向存疑：`trxBalance > 5000000` 时 continue），没有实际转账逻辑。
2. **`ops.go SetLevel` 用 `pub_key` 字段查钱包**，但 `model.Wallet` 中不存在该字段（实为 Address），该接口会稳定失败。
3. **`wallet.go ChargePoint`**：已存在记录的分支用了 `Create` 而非 `Updates/Save`，可能报重复主键错误（该函数目前仅 ops 测试接口使用，主充值走 `AddPointAmount`，影响有限）。
4. **`wallet.go Withdraw` 流水类型笔误**：`wtxFee.Type = enum.BlockOut`，应为 `enum.Fee`；且 `wtx.Type`（提币流水）未被赋值，默认 0。
5. **RSA 私钥硬编码在 `crypto/rsa.go`** 源码中，泄露风险高，生产环境建议改为密钥文件/环境变量注入。
6. **`trshash` 中 KTO 转账 `amount == 22000000` 被跳过**（内部交易费补贴），依赖硬编码值。
7. **价格接口依赖第三方**（`bcone.vip`、`wallet.fibo.services`），不可用时静默返回 0 或 6.42，可能影响手续费与资产估值。
8. **挖矿每日奖励 `3498542274052 * 0.425` 硬编码在 `cmd/mining/mining.go`**，随产量调整需改代码重编译。
9. 各 yml 配置中默认路径为开发者本机（`/Users/...`），实际部署需通过 `-f` 指定配置文件。
10. `round.go Init`：首轮 `target` 必须传，否则报「请初始化第一轮目标数量」；非首轮 target 传 0 时自动按上一轮目标 ×1.3 递增。

## 十、运行与部署（来自 README）

| 程序 | 构建 | 运行 |
|---|---|---|
| 主程序 | `cd cmd && go build -o npower main.go` | `nohup ./npowerRun -f ./etc.yml > crowd.log 2>&1 &` |
| 轮次 | `cd cmd/round && go build -o round round.go` | `nohup ./roundRun -f ./round.yml > round.log 2>&1 &` |
| 充值监听 | `cd cmd/trshash && go build -o trshash trshash.go` | `nohup ./trshashRun -f ./trs.yml > trs.log 2>&1 &` |
| 归集 | `cd cmd/fundcollection && go build -o collecting collecting.go` | `nohup ./collecting -f ./collecting.yml > collecting.log 2>&1 &` |
| 挖矿 | `cd cmd/mining && go build -o mining mining.go` | `nohup ./miningRun -f ./mining.yml -net http://192.168.0.5:6000 > mining.log 2>&1 &` |
| 运维 | `cd cmd/ops && go build -o ops ops.go` | `nohup ./opsRun -f ./ops.yml > ops.log 2>&1 &` |
| 手动归集 | `cd cmd/manualcollection && go build -o manual manual.go` | `nohup ./manual -f ./manual.yml -symbol FIBO > manual.log 2>&1 &` |

> 均为 Linux amd64 交叉编译：`GO111MODULE=on CGO_ENABLED=0 GOOS=linux GOARCH=amd64`
