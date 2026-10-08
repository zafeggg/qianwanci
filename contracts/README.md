# 千万次（TiMi）链上合约工程

链：FIBONACCI（chainId 12306，RPC https://network.hzroc.art，浏览器 https://hzroc.art）

范围：仅 TM 手续费代币上链。玩法逻辑仍由后端 `houduan/TiMi` 承载（owner 2026-09-14 裁定）。

本工程与 GYT、ZYT 项目完全独立，仅共用 FIBO 链网络参数。

## 合约

| 合约 | 说明 |
|---|---|
| `contracts/TMToken.sol` | 手续费代币 TM，ERC20 + Burnable + Ownable + Pausable |

### 关键口径

| 项 | 值 | 依据 |
|---|---|---|
| 精度 | 8 位 | 对齐后端 `config.SymbolDictionary["TM"]=1e8` |
| 销毁下限 | 7777 枚，合约层强制 | 千万次技术方案 1.7 |
| 初始发行量 | 留空，部署后由 owner 一次性 `mintInitial` | 业务数值模型未定稿 |
| 发行硬顶 | 部署时传入（默认 100 亿枚），不可变 | 封住增发风险 |
| owner | 单一部署 EOA，禁止 renounceOwnership | 沿用 GYT/ZYT 资金治理口径 |

## 命令

```bash
npm install
npm run compile
npm run test

# ============ 本地私链（后端联调用，chainId 31337）============
# 终端 A：起持久节点（hardhat node）
npm run node:local
# 终端 B：部署 TM + 初始发行 + 生成 ABI/部署记录（幂等，可重复执行）
npm run prepare:localnode
# 打印账户地址与私钥（配置后端 etc.privchain.yml 用）
npm run accounts:localnode
# 节点重启后链上状态清空、合约地址会变，必须重跑 prepare 并同步后端配置

# 部署（主网，私钥走环境变量，不落盘）
export PRIVATE_KEY=0x...
npm run deploy:fibo

# 总量定稿后一次性发行
MINT_COINS=1000000 POOL_ADDRESS=0x... npx hardhat run scripts/mint-initial.js --network fibo

# 只读状态
npx hardhat run scripts/status.js --network fibo
```

> ⚠ 在受限沙箱（如 DSH）里 `hardhat run` 会报 `Error HH602: spawn EPERM`
> （其内部用 `child_process.fork`，需要 IPC 命名管道）。绕开方式：
> `node --require ./node_modules/hardhat/register ./scripts/prepare-local.js`（`package.json` 里的 `prepare:localnode`/`accounts:localnode` 已按此写法）。

部署记录写入 `deployments/<network>.json`，ABI 写入 `deployments/abi/`（`localhost.json` 已 gitignore：每次重启节点地址都会变）。

## 目录

```
contracts/TMToken.sol           合约源码
test/TMToken.test.js            单元测试
scripts/deploy-fibo.js          部署（主网/测试网/本地节点）
scripts/mint-initial.js         初始发行
scripts/status.js               只读状态
scripts/prepare-local.js        本地私链一键准备（部署+铸币+打印后端配置片段）
scripts/print-accounts.js       打印本地节点账户地址/私钥
deployments/                    部署记录与 ABI
```

## 待办

1. TM 初始发行总量与池分配方案定稿（合约 `mintInitial` 一次性铸币，**这一项不落地就不能上主网**）
2. FIBO 测试网部署验证
3. 后端 EVM 模块已在私链跑通（充值监听/出金广播/手续费销毁），主网广播仍待真实资金验证
4. ~~前端接入合约地址与 ABI、钱包签名登录~~ ✅ 已完成（2026-09-30 复核）：
   - 合约地址/精度/确认数由后端 `GET /api/deposit/info` 下发，前端不硬编码（`dapp-vue/src/services/token.js`）
   - 钱包签名登录（EIP-191 nonce → 签名 → 会话票）已接入，且 `/api` **写接口强制要求会话**
   - 主网合约地址仍未定（见待办 1）；合约单测 `test/TMToken.test.js`（30 用例）覆盖 7777 下限/burn/暂停/黑名单/所有权
5. 后端 burn 判据已改为读链上 `totalSupply`（与合约 `MIN_CIRCULATING` 同口径，2026-09-30）；上线前需用真实发行量复验一次"停烧点"
