# 千万次 DApp - Vue 3 + Vite 版

FIBONACCI 链上众筹 DApp 的 **Vue 3 重构版**（移动端钱包访问）。纯 HTML 版保留在 `F:/n_cifang-dapp/`，两版业务引擎共享。

## 技术栈

| 模块 | 选型 | 说明 |
|---|---|---|
| 框架 | Vue 3.5 (Composition API) | `<script setup>` 单文件组件 |
| 构建 | Vite 5 | dev 秒启 / 生产构建按路由分包 |
| 路由 | Vue Router 4 (hash 模式) | 钱包 WebView 兼容，视图懒加载 |
| 状态 | Pinia | wallet / game 两个 store |
| UI | Vant 4 | 移动端组件库（PopUp / Toast） |
| 链 | ethers 5.7.2 | 与纯 HTML 版一致，避免 v6 迁移风险 |

## 快速开始

```bash
npm install
npm run dev        # 开发（默认 3002，被占用自动顺延）
npm run build      # 生产构建 → dist/
npm run preview    # 本地预览构建产物
npm test           # 纯逻辑单测（Node 内置 node --test，无额外依赖，离线可跑）
```

手机调试：dev server 已开启 `host: true`，手机与电脑同一局域网，
访问 `http://<电脑IP>:3002`（钱包内 DApp 浏览器打开）。

## 目录结构

```
src/
├── api/index.js            # API 抽象层（真实后端 + mock 降级）
├── App.vue                 # 应用外壳（Header + TabBar + RouterView）
├── main.js                 # 入口（Pinia / Router / Vant 样式）
├── router/index.js         # hash 路由 + 懒加载
├── stores/
│   ├── wallet.js           # 钱包状态（连接/断开/事件同步）
│   └── game.js             # 游戏数据（轮次/资产/用户/矿机/价格）
├── services/
│   ├── wallet.js           # 钱包纯逻辑（EIP-6963 + 链切换 + 撤销授权）
│   └── mock.js             # Mock 数据源（localStorage 持久化）
├── engine/game.js          # 规则引擎（纯函数，两版共享）
├── constants/config.js     # 全部规则参数（两版共享）
├── utils/                  # format / time
├── styles/global.css       # 主题变量 + Vant 桥接 + 通用样式
├── components/
│   ├── WalletModal.vue     # 钱包选择弹窗（Vant Popup）
│   ├── ParticipateModal.vue# 参与弹窗（限额校验/预期收益）
│   ├── Countdown.vue       # 倒计时组件
│   └── ConnectGuard.vue    # 连接守卫（未连接展示引导）
└── views/
    ├── HomeView.vue        # 轮次看板
    ├── AssetsView.vue      # 我的资产/仓位
    ├── ReferralView.vue    # 动态收益/团队
    ├── MinerView.vue       # 矿机算力
    ├── WithdrawView.vue    # 提币（3% BOFI 手续费）
    └── RulesView.vue       # 玩法规则
```

## 与纯 HTML 版差异

| 项 | 纯 HTML 版 | Vue 版 |
|---|---|---|
| 页面渲染 | 手写 DOM / querySelector 更新 | 模板响应式自动更新 |
| 状态管理 | 手写发布-订阅 store | Pinia（devtools 可调试） |
| 组件复用 | 原生函数组件 | SFC 组件 + 插槽 |
| 移动端组件 | 手写弹窗/Toast | Vant Popup/Toast |
| 构建 | 零构建直接部署 | Vite 构建（dev 热更新） |
| 懒加载 | 无（全量加载） | 路由级 code-split |
| 业务引擎 | game.js/config.js | **完全相同（共享）** |

## 接口契约（与纯 HTML 版一致）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/rounds` | 轮次列表 |
| GET | `/api/rounds/current` | 当前轮详情 |
| GET | `/api/assets?address=` | 用户资产 |
| GET | `/api/user?address=` | 推荐/团队 |
| GET | `/api/miner?address=` | 矿机信息 |
| GET | `/api/prices` | 价格表 |
| POST | `/api/participate` | 参与 |
| POST | `/api/withdraw` | 提现 |
| POST | `/api/miner/claim` | 领取产出 |

统一响应 `{ code: 0, data, message }`。后端未部署时自动降级 mock（localStorage 持久化）。

## 验证记录（2026-08-31）

- [x] 生产构建成功（496 模块，视图按路由分包，主包 gzip 130KB）
- [x] Chrome headless 渲染：6 路由全部通过（首页轮次/倒计时/进度/TabBar）
- [x] 引擎单测回归：11/11（倒123 结算/动态档位/团队等级/手续费/矿机产出）
- [x] 钱包模块适配：services/wallet.js 改为纯逻辑，Pinia 持有状态

## FigBox 钱包集成（2026-09-01）

### 背景

FigBox 是 FIBO 主网官方手机钱包，与 MetaMask 存在两个关键差异：

1. **共享注入点**：FigBox 内置浏览器注入 `window.ethereum`。识别**完全依赖品牌标识**（isAlphawallet / isAlphaWallet 等），不依赖 `isMetaMask` 判断，旧版「isMetaMask 伪装比对」已取消（2026-09-02，FigBox 新版统一带 isAlphawallet 标识）。
2. **冷钱包场景**：桌面浏览器 / 外部浏览器中 FigBox 不注入任何 provider，但弹窗仍须固定展示 FigBox 入口。

### 识别与发现策略（services/wallet.js）

| 机制 | 说明 |
|---|---|
| `isFiboProvider(p)` | 强识别：7 个品牌标识（isAlphawallet / isAlphaWallet / isFibo / isFiboWallet / isFigbox / isFigBox / isFIGBOX）+ name 含 fibo/figbox。**不校验 isMetaMask**（2026-09-02 取消伪装比对，见下方策略调整） |
| `discoverWallets()` | 三通道发现并**按钱包身份去重**：EIP-6963 → window.ethereum（移动端无品牌特征兜底视为 FIGBOX）→ **FIGBOX 固定入口占位**（provider=null） |
| `resolveFigBoxProvider()` | 固定入口点击时解析真实 provider：与 GYT selectWallet('fibo') 一致，恒走 window.ethereum（检测与连接方式同 MetaMask，FigBox 与小狐狸共享注入点） |

### 连接调用链

```
WalletModal 点击 FigBox 入口（provider=null）
  → resolveFigBoxProvider()：window.ethereum 存在且 request 可用即返回（无注入则 toast 引导开启 DApp 注入）
  → walletStore.connect(provider, name)
  → connectWallet()：ensureChain(12306) → eth_requestAccounts → Web3Provider → 事件绑定
```

错误分支处理：

1. 无注入 → toast「未检测到 FIGBOX 钱包，请确认已在 FIGBOX 中开启 DApp 注入后重试」
2. 无品牌标识 → 不识别为 FigBox，按 isMetaMask 等常规特征归类（旧伪装引用比对已取消）
3. 链切换失败 → 现有 `ensureChain` 提示，可钱包内手动切换
4. 连接中 → 弹窗内独立 loading（`connectingName`），store 层 `connecting` 防重复点击

### FigBox 真机验证清单

| # | 场景 | 预期 |
|---|---|---|
| 1 | 桌面 Chrome 打开弹窗 | 钱包列表固定显示「FigBox」入口，无注入时点击提示开启 DApp 注入 |
| 2 | FigBox 内置浏览器打开 DApp | 入口识别为 FigBox 并正常连接，地址 / 余额展示正确 |
| 3 | FigBox 中连接后切走账户 | 全局事件同步，UI 地址更新 |
| 4 | FigBox 中断开授权 | 状态清理，重连需重新授权（EIP-2255 revoke + localStorage 标记） |
| 5 | MetaMask / TokenPocket 回归 | 两钱包连接、切换互不冲突，不出现重复入口 |
| 6 | 品牌标识识别 | 模拟 isAlphaWallet=true 的 FigBox provider，`detectWalletName` 返回 FigBox；纯 isMetaMask provider 返回 MetaMask 不误判 |
| 7 | 断网 / 拒绝授权 | toast 错误提示明确，弹窗可关闭重试 |

## 验证记录（2026-09-01）A 组桌面环境 + 深度边界检测

自动化浏览器（Chromium 152）+ mock provider 注入，全部通过，未发现缺陷：

| # | 场景 | 结果 |
|---|---|---|
| A1-1 | 桌面 Chrome 无注入打开弹窗 | ✅ 列表固定显示 FigBox 入口（仅 1 个） |
| A1-2 | 点击 FigBox（无注入） | ✅ toast「未检测到 FIGBOX 钱包…」，弹窗保持打开可重试 |
| A6 | 品牌标识识别（isAlphaWallet=true） | ✅ isFiboProvider=true，detectWalletName=FigBox，无 MetaMask 重复入口 |
| A6b | 对照组（纯 isMetaMask 无品牌标识） | ✅ 识别为 MetaMask，列表显示 MetaMask + FigBox 两入口 |
| A7 | 拒绝授权（eth_requestAccounts 抛 4001） | ✅ 错误 toast 明确，弹窗可关可重试，未误连 |
| 扩展 1 | 断网 / eth_chainId 网络错误 | ✅ 兜底提示「无法切换到 FIBONACCI 网络」 |
| 扩展 2 | 链切换失败（非 4902 错误） | ✅ 同上兜底，不崩溃 |
| 扩展 3 | 链添加流程（switch 抛 4902） | ✅ wallet_addEthereumChain 被调用，连接成功 |
| 扩展 4 | EIP-6963 广播 + window.ethereum 双通道 | ✅ 按身份去重，MetaMask 仅 1 条，FigBox 固定入口独立展示 |
| 扩展 5 | 断开（revokeProvider） | ✅ send wallet_revokePermissions + localStorage ncf_wallet_disconnected |
| 扩展 6 | 重连（flag 存在） | ✅ 先强制 revoke 再授权，成功后清除 flag |
| 扩展 7 | 账户切换事件 | ✅ accountsChanged → 全局事件 ncf:wallet-account-changed 同步新地址 |
| 扩展 8 | 连接成功全链路（UI） | ✅ chip 显示「MetaMask · 0xabc1...7890」，toast 成功，弹窗关闭 |

验证方式：agent-browser eval 内 `import('/src/services/wallet.js')` 函数级断言 + UI 点击链路。mock 脚本留存 `.workbuddy/verify/`（mock-figbox / mock-metamask / mock-reject）。

## Bug 修复记录（2026-09-01）

### 钱包弹窗显示 Base64 长串（真机反馈）

- **现象**：真实环境中 MetaMask / TP 条目名称区铺出一长串 Base64 字母，FigBox 正常
- **根因**：EIP-6963 `info.icon` 是钱包图标 data URI（`data:image/svg+xml;base64,...`，数千字符），`WalletModal.vue` 将其按文本 `{{ w.icon }}` 渲染
- **风险**：零资金风险，纯 UI 展示缺陷，不涉及签名 / 授权 / 交易
- **修复（方案 A）**：`isDataUri()` 判断，data URI 用 `<img class="wallet-option__logo-img">` 渲染真实钱包 logo，emoji 走文本；SVG 在 img 中不执行脚本，无 XSS 风险
- **验证**：复现场景（mock EIP-6963 带 data URI icon）通过：logo 无文本串、img 完整加载（naturalWidth=150）、FigBox emoji 不受影响；回归脚本留存 `.workbuddy/verify/mock-eip6963-icon.js`

## 策略调整记录（2026-09-02）

### 取消 FigBox 的 isMetaMask 伪装识别

- **变更**：`isFiboProvider()` 删除 isMetaMask 引用比对分支（window.figboxWallet 等 9 个别名对象比对），识别仅依赖品牌标识（isAlphawallet / isAlphaWallet / isFibo / isFigbox 等）+ name 兜底
- **依据**：FigBox 新版不伪装 isMetaMask，统一带 isAlphawallet 品牌标识；旧引用比对依赖全局专属对象，真机环境下不可靠
- **行为影响**：旧形态（isMetaMask=true + 无品牌标识）的 provider 不再识别为 FigBox，按 isMetaMask 归类为 MetaMask
- **验证**（agent-browser 函数级 + discoverWallets 集成级）：isAlphaWallet=true → fibo/FigBox ✅；isAlphaWallet + isMetaMask 双标识 → fibo（品牌优先）✅；纯 isMetaMask → metamask 不误判 ✅；isFibo=true → fibo ✅
- **回归脚本**：`mock-figbox.js` 已改为真实形态（isAlphaWallet=true，无 isMetaMask）

## PWA 与移动端交付（2026-09-30 新增）

需求原文要求「持有 Token 的用户可以**下载** N次方 DApp 参与」。当前交付形态与后续路径：

### 已交付：可安装 PWA（无需任何原生工具链）

| 文件 | 作用 |
|---|---|
| `public/manifest.webmanifest` | 应用清单：名称/图标/`display: standalone`/主题色；`start_url` 用相对路径（兼容 hash 路由与子路径部署） |
| `public/sw.js` | Service Worker：页面 **network-first**（离线回落外壳）、同源静态资源 **cache-first**、**`/api` 一律不拦截不缓存**（缓存余额/轮次等于给用户看假数据） |
| `public/icon-192.png` / `icon-512.png` / `icon-maskable-512.png` | 真实 PNG 图标（含 maskable 安全区） |
| `index.html` | `<link rel="manifest">`、`theme-color`、`apple-mobile-web-app-*`、`apple-touch-icon` |
| `src/main.js` | **仅生产构建**注册 SW（dev 下注册会缓存 dev 资源、热更新拿到旧文件）；注册失败仅告警，不影响启动 |

用户体验：手机浏览器打开站点 → 「添加到主屏幕」→ 独立窗口运行、有桌面图标、弱网/离线可打开外壳。

⚠ 三点注意：
1. **必须 HTTPS**（或 localhost）：Service Worker 与"添加到主屏幕"都要求安全上下文；
2. 升级缓存要改 `public/sw.js` 里的 `CACHE_VERSION`；
3. `VITE_API_BASE` 未配置时页面顶部会显示阻塞横幅（外壳能开、拿不到数据）—— 刻意设计，避免"看起来能用其实是假数据"。

### 后续（未交付）：原生 APK / IPA

需要额外工具链（本机没有），故未做：

```bash
# 1) 加壳（Capacitor 方案，需联网装依赖）
npm i -D @capacitor/cli && npm i @capacitor/core @capacitor/android
npx cap init 千万次 com.qianwanci.dapp --web-dir=dist
npx cap add android

# 0) 一次性安装打包工具链（需联网；本仓库未把 @capacitor/* 写进 package.json，
#    以免 CI 的 npm ci 因 lockfile 未同步而失败）
npm i -D @capacitor/cli @capacitor/core && npm i @capacitor/android
npx cap init 千万次 com.qianwanci.timi --web-dir dist   # 已提供 capacitor.config.json，可跳过

# 1) 生成图标/启动图（把 public/icon-512.png 转成各密度资源；需联网）
npx @capacitor/assets generate

# 2) 构建（需 JDK 17 + Android SDK + Gradle）
npm run build && npx cap sync android
cd android && ./gradlew assembleRelease     # 产物 app/build/outputs/apk/release/*.apk
```

另需 owner 决定：包名与签名密钥（keystore）归属、是否上架应用商店、是否需要推送/深链。
本项目用 hash 路由，WebView 下无需额外配置；若改 history 路由，需处理 `start_url` 与深链回退。

## 待优化

- [ ] Vant 按需引入（unplugin-vue-components），当前全量 CSS 209KB
- [ ] 原生 APK/IPA 壳（见上节，需 JDK/Android SDK）
- [x] ~~后端部署后设置 `VITE_API_BASE` 移除降级分支~~ → 2026-09-30 已改：mock 默认关闭、写操作永不 mock、未配置域名给阻塞横幅
- [ ] 移动端 1px 边框 / 暗色键盘优化
- [ ] 每轮"已投/剩余可投"额度展示（后端已有每轮每人累计上限校验，前端未展示剩余额）


## 前端单测（node:test，零新依赖）

`npm test` 会执行 `test/` 下的用例（Node 18+ 自带测试运行器，**不需要 vitest/jest 或任何联网安装**）：

| 文件 | 覆盖内容 |
|---|---|
| `test/engine.test.mjs` | 需求口径数值：每轮总额度 +30%、参与上限逐轮递增（100/110/130/150 → +20）、静态收益 13%、三进一出 N+3、倒1 全额退本 / 倒2倒3 扣半并按爆仓价折算算力、动态收益 2/5/10 门槛（1.5%/2%/2.5%）、F1/F2/F3 判定、团队 0.5%/1%/1.5%、提币 3% TM 手续费、算力三模式 |
| `test/utils.test.mjs` | 金额/地址/百分比/时间格式化的边界（NaN、Infinity、null、非法字符串一律兜底，不允许渲染成 `NaN`/`Invalid Date`） |


另外 `src/constants/rules.js` 负责在启动时拉取 `GET /api/rules`（后端**实际生效**的规则数值）并覆盖本地默认值 —— 页面展示与结算口径同源，`test/rules.test.mjs` 锁死该覆盖行为（共 23 项）。

这些用例已抓到两个真实缺陷并修复：`calcMinerOutput` 只认字符串 `id`（后端下发数值 `modeId` 时会静默回落"默认三倍出局"）、`formatPercent(NaN)` 渲染成 `NaN%`。
改动规则数值时请先跑 `npm test`。