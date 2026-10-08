/**
 * 千万次 DApp - 全局常量配置
 * 规则参数集中管理，业务引擎与页面均从此读取，避免魔法数字
 * 规则基准：TiMi(1).docx（新项目规则）；N次方玩法及规则.docx 仅借鉴（历史参考）
 */

/* 支持参与的资产列表（第一阶段） */
export const SUPPORTED_ASSETS = [
  { symbol: 'FIBO', name: 'FIBO', chain: 'FIBONACCI', decimals: 18 },
  { symbol: 'USDT', name: 'USDT', chain: 'FIBONACCI', decimals: 6 },
];

/* 手续费代币：TiMi（千万次）基准 = TM（TiMi(1).docx 第 9 条：提币 3% 手续费使用 TM 支付）
   裁决①（2026-09-04）：TM 手续费同样销毁，直至仅剩 7777 枚（与 N次方 BOFI 一致） */
export const FEE_TOKENS = {
  ncf: { symbol: 'BOFI', name: 'BOFI', priceUsd: 1.0, burnTarget: 7777 },
  timi: { symbol: 'TM', name: 'TM', priceUsd: 3.4, burnTarget: 7777 },
};

/**
 * 玩法规则参数
 * 来源核对（TiMi(1).docx 为基准，结算机制借鉴 N次方 docx 第 18/25-32 段，2026-09-02 owner 确认）：
 * - 总额度每轮递增 30%，起始数字可随意设定（TiMi 第 1 条：如 1000/5000）
 * - 每轮用户有最低与最高限额（TiMi 第 2 条；上限数值文档未给定，下表取自 N次方示例，链上/后端可覆盖）
 * - 静态收益 13%/轮（TiMi 第 3 条）
 * - 倒1 全额退回、倒2/倒3 扣 50% 折算算力（TiMi 第 5/7 条）
 * - 结算闭环（借鉴 N次方"三进一出"）：投 R 轮 → 第 R+3 轮结束强制结算本息；
 *   失败轮为倒1 全退；倒2/倒3 扣 50% 折算算力（金本位 3 倍，300 天兜底）
 * - 循环（TiMi 第 6 条"进入下一次循环"）：爆仓结算后新循环从 firstRoundTarget 重新起算
 */
export const GAME_RULES = {
  /* 循环起始总额度（TiMi：每循环起点可随意设定） */
  firstRoundTarget: 1000,
  /* 每轮总额度递增比例：30% */
  targetGrowthRate: 0.3,
  /* 参与下限 */
  minLimit: 10,
  /* 参与上限表：前 4 轮显式（N次方示例 100/110/130/150），其后每轮 +20 */
  maxLimitTable: [100, 110, 130, 150],
  /* 第 4 轮之后每轮上限增幅 */
  maxLimitStepAfterTable: 20,
  /* 静态收益：每轮成功 13% */
  staticReturnRate: 0.13,
  /* 结算偏移：投第 R 轮，第 R+3 轮结束时统一结算（本息秒到）
     规则来源裁定（owner 2026-09-08）：千万次（TiMi(1).docx）与 N次方 均有规定的，按千万次；
     千万次未明确的，按 N次方规则。TiMi(1).docx 未规定结算轮次（只有第5条倒1/倒2/倒3 的失败处理），
     故沿用 N次方"三进一出"：N次方-技术方案.md 1.2 —— 第 N 轮投入的资金在第 N+3 轮结束时统一结算。
     后端 core/impl MinRound=3（rewardRoundNum=currentRound-MinRound）同口径，改动须双边同改。 */
  settleOffset: 3,
  /* 倒1 退本比例（100% 本金） */
  refundRateForLast: 1,
  /* 倒2 / 倒3 扣币比例（50%） */
  penaltyRateForLastMinus: 0.5,
  /* 爆仓后是否开启新循环并从 firstRoundTarget 重新起算（TiMi 第 6 条） */
  cycleRestart: true,
  defaultRoundSeconds: 3600, // 基础轮次时长 1h，实际随市场活跃度调整
};

/**
 * 动态收益配置
 * 直推地址数解锁层级：
 * - 2 地址：第 1 代 1.5%
 * - 5 地址：第 1 代 1.5% + 第 3 代 2%
 * - 10 地址：第 1 代 1.5% + 第 3 代 2% + 第 5 代 2.5%
 */
export const REFERRAL_RULES = {
  tiers: [
    { needDirect: 2, depths: [{ depth: 1, rate: 0.015 }] },
    {
      needDirect: 5,
      depths: [
        { depth: 1, rate: 0.015 },
        { depth: 3, rate: 0.02 },
      ],
    },
    {
      needDirect: 10,
      depths: [
        { depth: 1, rate: 0.015 },
        { depth: 3, rate: 0.02 },
        { depth: 5, rate: 0.025 },
      ],
    },
  ],
};

/**
 * 团队等级配置
 * - F1：直推 10 地址 + 同期伞下参投 30 人 + 伞下历史累计 30 个地址各成功至少 1 次
 *   （成功口径与后端一致：任意期 success 轮参投即计，不限定"该期"，2026-09-02 后端定稿）
 * - F2：伞下有 3 个 F1
 * - F3：伞下有 3 个 F2
 * 奖励：F1 拿伞下总投资额 0.5%，F2 拿 1%，F3 拿 1.5%
 */
export const TEAM_RULES = {
  levels: [
    { level: 'F1', rate: 0.005, needDirect: 10, needUnderTreeActive: 30, needPromote: 0 },
    { level: 'F2', rate: 0.01, needDirect: 0, needUnderTreeActive: 0, needSubLevel: 'F1', needSubCount: 3 },
    { level: 'F3', rate: 0.015, needDirect: 0, needUnderTreeActive: 0, needSubLevel: 'F2', needSubCount: 3 },
  ],
};

/**
 * 算力 / 挖矿模式配置
 * 三种模式（文档第 8 条）：
 * 1. 默认三倍出局：累计产出 = 算力 x 3 后出局（币本位）
 * 2. 最长 300 天三倍出局：每日最低产出 = (算力 x 3) / 300 / 4小时均价
 * 3. 获取 TM（3.4U/枚），300 天三倍算力产出
 *
 * ⚠ modeId 是**后端契约字段**：POST /api/miner/mode 的 mode 取 0/1/2，
 *   GET /api/miner 回包也带 modeId。字符串 id 仅用于前端展示与 mock 兜底映射，
 *   两者必须一一对应，改动须与后端 http 层同改。
 */
export const MINER_RULES = {
  /* 切换模式时提交给后端的币种（后端 symbol 可选，缺省按 TM 计） */
  defaultSymbol: 'TM',
  modes: [
    {
      id: 'default',
      modeId: 0,
      name: '默认三倍出局',
      desc: '累计产出达到算力 x 3 即出局，不限制天数',
      payoutMultiple: 3,
      maxDays: null,
    },
    {
      id: 'fixed300',
      modeId: 1,
      name: '300天三倍出局',
      desc: '最长 300 天，每日最低产出 = (算力 x 3) / 300 / 4小时均价',
      payoutMultiple: 3,
      maxDays: 300,
    },
    {
      id: 'tm',
      modeId: 2,
      name: 'TM 兑换（3.4U/枚）',
      desc: '获取 TM 后 300 天三倍算力产出',
      payoutMultiple: 3,
      maxDays: 300,
      tmPriceUsd: 3.4,
    },
  ],
};

/**
 * 提币手续费
 * 提取时收取 3% 手续费，以手续费代币支付：
 * - 提取价值 100U 的币 → 需价值 3U 的手续费代币
 * - 按手续费代币市价折算所需数量
 */
export const WITHDRAW_RULES = {
  feeRate: 0.03,
  // 最低起提额（个数）：owner 2026-09-30 敲定 100 个起提；0 = 不限。
  // 实际值以后端 /api/rules 为准（启动时同步覆盖），这里只是不可用时的兜底。
  minAmount: 100,
};

/**
 * 链配置（默认 FIBONACCI 主网，可用环境变量覆盖）
 *
 * 为什么要可配：原先是写死的字面量，而 `services/wallet.js#ensureChain` 会**强制**把用户钱包
 * 切到这里的 chainId —— 私链/测试网联调时前端会一直把钱包往主网 12306 推，DApp 根本连不上。
 * 现在 `npm run dev` 用的 `.env.development` 指向本地私链（31337），
 * 生产构建不带这些变量即回落主网默认值，行为与改造前一致。
 *
 * 覆盖项（见 dapp-vue/.env.development）：
 *   VITE_CHAIN_ID / VITE_CHAIN_NAME / VITE_CHAIN_RPC / VITE_CHAIN_EXPLORER /
 *   VITE_CHAIN_NATIVE / VITE_CHAIN_DECIMALS
 */
const ENV = import.meta.env || {};
const chainId = Number(ENV.VITE_CHAIN_ID || 12306);
const explorerUrl = ENV.VITE_CHAIN_EXPLORER !== undefined ? ENV.VITE_CHAIN_EXPLORER : 'https://hzroc.art';

export const CHAIN_CONFIG = {
  chainId,
  chainIdHex: '0x' + chainId.toString(16),
  chainName: ENV.VITE_CHAIN_NAME || 'FIBONACCI',
  rpcUrl: ENV.VITE_CHAIN_RPC || 'https://network.hzroc.art',
  // 私链没有区块浏览器：置空时 wallet_addEthereumChain 不能带 blockExplorerUrls（空串会被钱包拒）
  explorerUrl,
  nativeSymbol: ENV.VITE_CHAIN_NATIVE || 'FIBO',
  nativeDecimals: Number(ENV.VITE_CHAIN_DECIMALS || 18),
};

/**
 * "获取 TM" 入口地址（可配置）
 *
 * TM 既是手续费币种（提现按 3% 收取），也是算力模式 2 的兑换币种，
 * 但**用户没法在 DApp 内自己获得 TM** —— 原先页面只说"需使用 TM 支付"，没有任何获取途径。
 * 这里给出一个可配置入口：填了 VITE_BUY_TM_URL 就在提币页显示"获取 TM"按钮（新窗口打开），
 * 没填则**整块隐藏**（不给假链接、不跳空白页）。目标通常是 DEX/CEX 的 TM 交易对。
 */
export const BUY_TM_URL = String(ENV.VITE_BUY_TM_URL || '').trim();
export const HAS_BUY_TM = BUY_TM_URL.length > 0;
/** 是否像真实链接（防呆：误填"待定"这类文字时不要把按钮放出来） */
export const BUY_TM_READY = /^https?:\/\//i.test(BUY_TM_URL);
/* 品牌配置：手续费代币按 TiMi（千万次）基准使用 TM */
export const BRAND = {
  name: '千万次',
  slogan: 'FIBONACCI 链上众筹',
  feeToken: FEE_TOKENS.timi,
  version: 'v0.1.0',
};

/**
 * 后端接口基地址
 *
 * ⚠ 为什么要有「占位常量」而不是一个看起来能用的默认域名：
 *   原实现默认 https://api.ncf.example.com，生产构建若忘了注入 VITE_API_BASE，
 *   请求会被静默发到一个**不属于本项目的域名**（example.com 是文档保留域），
 *   用户只看到「网络错误」，排查方向完全被带偏（以为是网络/后端挂了）。
 *   现在未配置就是「未配置」：常量只作显式占位，页面顶部显示「未配置后端接口地址」阻塞横幅，
 *   API 层对请求直接抛出可操作的原因。
 *
 * 覆盖方式：VITE_API_BASE（见 dapp-vue/.env.production、.env.example）
 */
export const API_BASE_PLACEHOLDER = 'https://api.ncf.example.com';

const rawApiBase = String(import.meta.env?.VITE_API_BASE || '').trim();

/** 实际使用的后端基地址：未注入时回落到占位常量（此时 API_BASE_IS_PLACEHOLDER 为 true） */
export const API_BASE = rawApiBase || API_BASE_PLACEHOLDER;

/**
 * 判断某个基地址是否仍属于「未配置」状态
 * 判定口径（任一命中即视为未配置）：
 * - 空值（已回落到 API_BASE_PLACEHOLDER）
 * - 等于显式占位常量
 * - 含 < >：.env.production 模板里的 `https://api.<你的域名>` 还没被替换
 * - 含 example.com / your-domain / 你的域名 等文档示例字眼
 * @param {string} [base] 待判定地址，缺省用当前 API_BASE
 * @returns {boolean}
 */
export function isPlaceholderApiBase(base = API_BASE) {
  const b = String(base || '').trim().toLowerCase();
  if (!b) return true;
  if (b === API_BASE_PLACEHOLDER.toLowerCase()) return true;
  if (b.includes('<') || b.includes('>')) return true;
  if (b.includes('example.com') || b.includes('your-domain') || b.includes('你的域名')) return true;
  return false;
}

/** 当前 API_BASE 是否未配置（界面据此显示阻塞横幅） */
export const API_BASE_IS_PLACEHOLDER = isPlaceholderApiBase();

/**
 * 演示数据（mock）开关 —— **构建期**决定，默认关闭
 *
 * ⚠ 语义变更（2026-09-27）：mock 从「请求失败自动降级」改为「显式开启才生效」。
 *   自动降级会让后端一抖动就切换到本地假数据：余额、收益、轮次进度全都变成编造值，
 *   而用户看到的是正常界面（甚至「提取成功 + 随机 txHash」）。默认必须是关闭的。
 *   开启后界面顶部常驻「演示数据（未连接后端）」横幅，且写接口依旧永不 mock。
 *
 * 开启方式：构建时设置 VITE_ENABLE_MOCK=true（见 .env.example）
 */
export const MOCK_ENABLED = String(import.meta.env?.VITE_ENABLE_MOCK || '').trim().toLowerCase() === 'true';

/**
 * /api 适配层鉴权密钥（请求头 X-API-Key）
 * - 与后端 yml apiAuthKey 配对：后端未设置时前端值可留空
 * - 生产环境部署时通过 VITE_API_KEY 注入，与后端密钥一致
 */
export const API_KEY = import.meta.env?.VITE_API_KEY || '';
