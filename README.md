# 千万次（TiMi）· N 次方众筹 DApp

链上众筹（三进一出）+ 算力矿机 + 动态/团队奖励的完整交付：Go 后端（Fiber + GORM + go-ethereum）、
Vue 3 DApp（含可安装 PWA）、Solidity 合约（TM 通缩代币）、私链端到端回归脚本与生产部署清单。

> 本文件只做"入口导航"。**需求逐条对照**与**修复记录**见 [docs/需求完成情况.md](docs/需求完成情况.md)，
> **上线操作**见 [houduan/TiMi/千万次-生产部署清单.md](houduan/TiMi/千万次-生产部署清单.md)，
> **参数与运维**见 [houduan/TiMi/千万次-迭代0参数化与运维说明.md](houduan/TiMi/千万次-迭代0参数化与运维说明.md)，
> **技术方案**见 [houduan/TiMi/千万次-技术方案.md](houduan/TiMi/千万次-技术方案.md)。

## 目录结构

| 路径 | 内容 |
|---|---|
| `houduan/TiMi/` | Go 后端：`cmd/`（npower 主服务、round 结算器、evmwatch 充值监听、hashpower 算力产出、burn 手续费销毁、trshash 打款、recon 对账、ops 运维后台）、`core/`（业务实现与规则参数）、`http/handler/`、`config/` |
| `dapp-vue/` | 用户端 DApp（Vue 3 + Vite + Pinia + vant + ethers v5），含 `test/` 纯逻辑单测与 PWA 资源 |
| `contracts/` | TM 代币等 Solidity 合约 + hardhat 单测（`npx hardhat test`） |
| `scripts/` | 私链端到端回归：`privchain-e2e.ps1`（全链路）、`privchain-fix-verify.ps1`（缺陷回归）、`privchain-{multiteam,f2f3,hashpower,withdraw-limits,hotwallet-env}.ps1`、`verify-{wallet-login,deposit}.mjs`（浏览器端到端）、`capture-pages.mjs`（页面截图与文本核对） |
| `scripts/export-secrets.ps1` · `scripts/purge-git-history.ps1` | 密钥治理：把 yml 里的明文私钥迁到**未跟踪**的 `config/secrets*.env`（yml 改为 `privateKeyEnv` 环境变量注入）；清 git 历史（重建模式，含安全门禁与"工作区之外"备份，已在克隆上完整演练） |
| `docs/` | 需求原件（`千万次-需求原件.txt` / `N次方-玩法规则原件.txt`）、`需求完成情况.md`（逐条对照 + 修复记录）、**`项目学习与需求沟通指南.md`（不懂代码也能看懂 + 怎么提需求）** |

## 快速自检（不需要链和数据库）

```bash
make check        # 后端 go build ./... + go vet ./... + go test ./...（集成用例自动跳过）
make frontend     # 前端 npm test（node:test，23 项规则口径与格式兜底）+ npm run build
make bintest      # 交叉编译 Linux 静态二进制到 houduan/TiMi/build/linux/
make e2e-help     # 私链端到端的前置与命令提示
```

离线环境说明：Go 依赖已在仓库内 `.gomodcache`（`Makefile` 默认 `GOFLAGS=-p=1 GOPROXY=off` 使用它）；
前端单测使用 Node 内置测试运行器，**不需要联网安装任何依赖**；生产构建需要联网安装前端依赖。

## 私链端到端（需要 hardhat 节点 + MySQL + Redis）

```bash
# 1) 起链（终端 A 常驻）
cd contracts && npm run node:local

# 2) 部署合约并准备私链数据
cd contracts && npm run prepare:localnode

# 3) 起服务：npower(:3000) / ops(:5000)，配置用 houduan/TiMi/config/etc.privchain.yml
#    （另可起 demo 演示网关 :3001 与前端 :3002：见 houduan/TiMi/README.md、dapp-vue/README.md）
houduan/TiMi/build/npower.exe -f houduan/TiMi/config/etc.privchain.yml
houduan/TiMi/build/ops.exe    -f houduan/TiMi/config/etc.privchain.yml -port :5000

# 4) 跑回归
powershell -File scripts/privchain-e2e.ps1            # 充值→入账→幂等→出金→销毁→轮次结算→对账
powershell -File scripts/privchain-fix-verify.ps1     # 45 项缺陷回归（鉴权/额度/倒2倒3/参数语义/evmwatch）
```

当前回归基线：`privchain-e2e.ps1` 全绿、`privchain-fix-verify.ps1` **45 PASS / 0 FAIL**、
`multiteam` 30/0、`f2f3` 22/0、`withdraw-limits` 13/0、`hotwallet-env` 6/0、`hashpower` 17/0（3 项占比断言在非独占算力池下显式 SKIP）、
`verify-wallet-login` 23/0、`verify-deposit` 16/0、`go test` 全绿、前端 `npm test` 23/0。

## 规则数值的单一来源

页面上的比例（静态收益 13%、提现手续费 3%、动态 1.5%/2%/2.5%、团队 0.5%/1%/1.5%、三进一出 N+3、
算力三倍/300 天、通缩目标 7777）都由后端 GET /api/rules 下发**实际生效值**，前端启动时同步覆盖本地默认值。
改参数只需改后端（`/ops/params` 或 `core/impl/config.go`），页面自动跟随；`null` 删键即回到代码默认值。

## 上线前必读

1. [生产部署清单](houduan/TiMi/千万次-生产部署清单.md) 的 §3（配置核查）、§3.1（密钥纪律）、§9（上线检查清单）；
2. **密钥**：仓库跟踪的 yml 里仍有历史明文私钥，上线前必须轮换并改为环境变量注入
   （`KtoPool.privateKeyEnv` / `TronPool.privateKeyEnv` / `Burn.privateKeyEnv` / `Withdraw.HotWalletKeyEnv` / `NPOWER_AES_SECRET_KEY`）；
3. **充值监听起始块**：`Chain.WatchStartBlock` 必须填（否则 evmwatch fail-closed 拒绝启动）；
4. **反代**：`Http.ProxyHeader` + `Http.TrustedProxies` 同时配置，限流才按真实客户端 IP 分桶；
5. **owner 待拍板项**：部署清单 §12（TM 主网地址与发行量、出金模式与审批、提现额度数值、`Auth.EnforceSession` 等）。
