/**
 * 千万次 DApp - API 抽象层
 *
 * 职责：统一前后端数据契约，**默认只与真实后端通信**。
 *
 * 降级策略（重写，原「失败即降级 mock + localStorage 记忆」已移除）：
 * 1. 只打真实后端（API_BASE，见 constants/config.js）
 * 2. 任何失败（网络错误 / 超时 / HTTP 4xx-5xx / 业务 code!=0）都**原样抛出**，
 *    由调用方提示用户；绝不自动改判为 mock
 * 3. 仅当**构建期显式开启** VITE_ENABLE_MOCK=true 时，读接口才允许回落到本地演示数据，
 *    且界面顶部常驻「演示数据（未连接后端）」横幅（见 App.vue）
 * 4. 写接口（参投 / 提现 / 领取产出 / 切换算力模式）**任何情况下都不 mock**：
 *    写请求被 mock 会返回随机 txHash，用户以为「提取成功」而链上/账本什么都没发生
 * 5. 不再把 api 模式写入 localStorage：一次网络抖动不该永久污染后续所有请求
 *
 * 接口契约（与后端约定）：
 *   GET  /api/rounds             -> 轮次列表
 *   GET  /api/rounds/current     -> 当前轮详情
 *   GET  /api/assets?address=    -> 用户资产（余额+仓位）
 *   GET  /api/user?address=      -> 用户信息（推荐/团队）
 *   GET  /api/miner?address=     -> 挖矿信息（含 modeId/payoutTarget/targetCoin/dailyOutput/switchable）
 *   GET  /api/prices             -> 价格表
 *   POST /api/participate        -> 提交参与 {address, roundId, amount, asset}          [需会话]
 *   POST /api/withdraw           -> 提现 {address, asset, amount, feeToken}             [需会话]
 *   POST /api/miner/claim        -> 领取挖矿产出 {address}                              [需会话]
 *   POST /api/miner/mode         -> 切换算力模式 {address, symbol, mode:0|1|2}          [需会话]
 *   GET  /api/auth/nonce?address= -> 一次性 nonce + 待签原文（A 模块签名登录）
 *   POST /api/auth/login         -> {address, signature, inviteCode?, name?} -> {token, ...}
 *   GET  /api/auth/me            -> 当前会话钱包（需 Authorization: Bearer <token>）
 */

import { API_BASE, API_KEY, API_BASE_IS_PLACEHOLDER, MOCK_ENABLED } from '../constants/config.js';
import {
  mockFetchRounds,
  mockFetchCurrentRound,
  mockFetchAssets,
  mockFetchUser,
  mockFetchMiner,
  mockFetchPrices,
} from '../services/mock.js';

/**
 * 会话票（内存态）。设置后所有带鉴权的请求都会自动附加 `Authorization: Bearer <token>`。
 *
 * ⚠ 只允许绑定「当前已连接钱包」的会话：门票属于某个地址，若钱包切到别的地址仍带旧票，
 *   在 Auth.EnforceSession=true 的后端上会把请求归属到旧地址（越权口径）。
 *   因此 store 在钱包地址变化/断开时必须调用 setAuthToken('')。
 */
let authToken = '';
export function setAuthToken(token) {
  authToken = token || '';
}
export function getAuthToken() {
  return authToken;
}

/** 演示数据是否启用（构建期常量，供 UI 判断是否显示演示横幅；也可从 constants 直接读） */
export const MOCK_ACTIVE = MOCK_ENABLED;

/** 当前 API_BASE 是否未配置（供 UI 显示阻塞横幅） */
export const API_BASE_UNCONFIGURED = API_BASE_IS_PLACEHOLDER;

/**
 * 单次请求超时时间
 * 原为 4000ms —— 那是为了「后端没部署时快速降级到 mock」。现在不再降级，
 * 4s 在移动网络下会把正常请求误判为失败（写请求尤其危险：超时 ≠ 后端没执行），故放宽。
 */
const REQUEST_TIMEOUT = 15000;

/**
 * 统一 API 错误：携带后端业务码 / HTTP 状态，便于界面区分「未登录(401)」与业务失败
 * （T1 要求：401 必须可辨识并提示「请重新签名」，业务错误必须展示后端 message）
 */
export class ApiError extends Error {
  constructor(message, { code = null, status = null } = {}) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.status = status;
  }

  /** 是否为「会话失效 / 未登录」错误（HTTP 401 或后端 401 业务码） */
  get isAuthError() {
    return this.status === 401 || this.code === 401;
  }
}

/**
 * 广播「会话失效」全局事件
 * 用 window 事件而不是直接 import store：api 层是纯逻辑层，反向依赖 Pinia store 会形成循环 import。
 * 监听方见 stores/wallet.js（清空会话 → 界面显示「未签名 / 重新签名」）。
 */
function notifySessionExpired() {
  try {
    window.dispatchEvent(new CustomEvent('ncf:session-expired'));
  } catch {
    /* 非浏览器环境（单测/SSR）忽略 */
  }
}

/**
 * 通用请求封装（真实后端）
 * @param {string} path 接口路径（如 /api/rounds）
 * @param {object} options fetch 选项
 * @param {{withAuth?:boolean}} [opts] withAuth=false 时不带会话票（登录入口自身用）
 * @returns {Promise<any>} 接口数据
 * @throws {ApiError} 未配置基地址 / HTTP 非 2xx / 业务 code!=0
 */
async function requestReal(path, options = {}, opts = {}) {
  const withAuth = opts.withAuth !== false;

  // 未配置后端地址：直接抛出可操作的原因。
  // 否则浏览器会去打 api.<你的域名> 得到 DNS/TLS 错误，用户与运维都只会看到「网络错误」。
  if (API_BASE_IS_PLACEHOLDER) {
    throw new ApiError('未配置后端接口地址（VITE_API_BASE），请配置后重新构建', {
      code: 'E_API_BASE_UNCONFIGURED',
    });
  }

  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), REQUEST_TIMEOUT);
  try {
    const headers = {
      'Content-Type': 'application/json',
      ...(API_KEY ? { 'X-API-Key': API_KEY } : {}),
    };
    if (withAuth && authToken) headers.Authorization = `Bearer ${authToken}`;
    // options.headers 允许局部覆盖，但必须显式合并（原实现 `...options` 会整体替换 headers，
    // 把 X-API-Key / Authorization 一起丢掉）
    Object.assign(headers, options.headers || {});
    const { headers: _ignored, ...rest } = options;
    const res = await fetch(`${API_BASE}${path}`, {
      ...rest,
      headers,
      signal: controller.signal,
    });

    // 先解析 body：401/5xx 也可能带 JSON（后端统一 {code,message}），解析失败则用状态码兜底
    const data = await res.json().catch(() => null);

    // 会话失效：必须可辨识（isAuthError）并提示重新签名，绝不静默吞掉
    if (res.status === 401) {
      notifySessionExpired();
      throw new ApiError(data?.message || '登录已过期，请重新签名', { code: 401, status: 401 });
    }
    if (!res.ok) {
      throw new ApiError(data?.message || `请求失败（HTTP ${res.status}）`, { status: res.status });
    }
    // 后端统一包裹格式：{ code, data, message }
    if (data && data.code === 0) return data.data;
    if (data && data.code !== undefined) {
      // 业务错误：展示后端 message（后端会给出「没有可调整的算力账户」这类具体原因）
      throw new ApiError(data.message || '接口错误', { code: data.code, status: res.status });
    }
    return data;
  } finally {
    clearTimeout(timer);
  }
}

/**
 * 读请求入口：真实后端优先
 * 仅当显式开启演示模式（VITE_ENABLE_MOCK=true）时，才允许在真实后端不可用时返回本地演示数据。
 * @param {string} path 接口路径
 * @param {object} options fetch 选项
 * @param {Function} mockFn 演示数据函数 (address?) => data
 * @param {string} [address] 可选地址参数
 * @returns {Promise<any>} 数据
 */
async function requestRead(path, options, mockFn, address) {
  // 演示模式 + 未配置后端地址：直接给演示数据，不去打一个注定失败的域名（省掉一次无谓超时）
  if (MOCK_ENABLED && API_BASE_IS_PLACEHOLDER) return mockFn(address);

  try {
    return await requestReal(path, options);
  } catch (err) {
    // 登录态问题绝不兜底：即使用演示模式也要让「请重新签名」暴露出来
    if (err?.isAuthError) throw err;
    if (!MOCK_ENABLED) throw err;
    console.warn(
      `[api] 演示模式(VITE_ENABLE_MOCK=true)：${path} 真实后端不可用，改用本地演示数据`,
      err?.message,
    );
    return mockFn(address);
  }
}

/**
 * 生成幂等键（后端 Idempotency-Key）
 * 后端幂等中间件（http/handler/idempotency.go）在收到该头时：同 key 的首次请求正常执行，
 * 重复请求原样返回首次响应并带 `Idempotency-Replayed: true`，并发重复返回 409。
 * 用于防「网络超时后用户重复点击 → 重复参投/重复提现」这类资金事故（原先前端完全没发这个头）。
 */
function newIdempotencyKey() {
  try {
    if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID();
  } catch {
    /* 非安全上下文等场景走下面的兜底 */
  }
  // 兜底：时间戳 + 随机串（非密码学强度，仅用于请求去重）
  return `ncf-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

/**
 * 写请求入口：**永远只打真实后端**
 *
 * 宁可明确失败，也绝不用 mock 编造「提交成功 + 随机 txHash」——
 * 那会让用户以为参投/提现/领取已经发生，实际账本与链上什么都没变（T1 要修的核心风险）。
 * 每次写请求都带一个唯一的 Idempotency-Key：用户重复提交时后端只执行一次。
 * @param {string} path 接口路径
 * @param {string} method HTTP 方法
 * @param {object} body 请求体
 * @returns {Promise<any>} 后端返回的 data
 */
async function requestWrite(path, method, body) {
  return requestReal(path, {
    method,
    body: JSON.stringify(body),
    headers: { 'Idempotency-Key': newIdempotencyKey() },
  });
}

/* ================= 对外接口（读） ================= */

/** 轮次列表 */
export async function fetchRounds() {
  return requestRead('/api/rounds', {}, mockFetchRounds);
}

/** 当前轮详情 */
export async function fetchCurrentRound() {
  return requestRead('/api/rounds/current', {}, mockFetchCurrentRound);
}

/** 用户资产 */
export async function fetchAssets(address) {
  return requestRead(`/api/assets?address=${address}`, {}, mockFetchAssets, address);
}

/** 用户信息（推荐/团队） */
export async function fetchUser(address) {
  return requestRead(`/api/user?address=${address}`, {}, mockFetchUser, address);
}

/** 挖矿信息（含后端计算的 dailyOutput / payoutTarget / targetCoin / switchable） */
export async function fetchMiner(address) {
  return requestRead(`/api/miner?address=${address}`, {}, mockFetchMiner, address);
}

/** 价格表 */
export async function fetchPrices() {
  return requestRead('/api/prices', {}, mockFetchPrices);
}

/**
 * 规则数值快照（后端**实际生效**参数）
 *
 * 这个接口**不走 requestRead 的 mock 降级**：规则数值是"结算口径"的展示来源，
 * 用 mock 编造一套比例会正好造成本项目要消除的"页面数字 ≠ 结算数字"，
 * 因此失败就让调用方回退到本地默认值（见 constants/rules.js）。
 */
export async function requestRules() {
  return requestReal('/api/rules', { method: 'GET' });
}

/* ================= 对外接口（写） =================
 *
 * ⚠ 以下全部走 requestWrite：不参与 mock，失败即失败（见文件头第 4 条）。
 *   后端要求写请求带钱包签名会话（Authorization: Bearer），未登录返回 401。
 */

/** 提交参与 */
export async function submitParticipate(params) {
  return requestWrite('/api/participate', 'POST', params);
}

/** 提现 */
export async function submitWithdraw(params) {
  return requestWrite('/api/withdraw', 'POST', params);
}

/** 领取挖矿产出 */
export async function claimMiner(address) {
  return requestWrite('/api/miner/claim', 'POST', { address });
}

/**
 * 切换算力产出模式
 * @param {{address:string, symbol?:string, mode:0|1|2}} params
 *   mode: 0=默认三倍出局 | 1=最长300天三倍出局 | 2=TM 300天三倍
 *   后端要求 body.address 与会话地址一致；业务不满足时返回 code=600 + message
 * @returns {Promise<{mode:number, modeName:string, updatedAt:number}>}
 */
export async function submitMinerMode(params) {
  return requestWrite('/api/miner/mode', 'POST', params);
}

/* ================= 签名登录（A 模块） =================
 *
 * ⚠ 这三个接口**刻意不走 mock 降级**：登录是鉴权入口，降级成 mock 会返回一个假会话票，
 *   让页面看起来「已登录」而实际没有任何凭据 —— 与连接钱包必须拿到真实地址同理，失败就要失败。
 *   调用方（stores/wallet.js）会捕获异常、清空会话并提示用户。
 */

/** 取一次性 nonce 与待签原文（覆盖写入：重新获取即作废旧 nonce） */
export async function fetchAuthNonce(address) {
  return requestReal(`/api/auth/nonce?address=${encodeURIComponent(address)}`, {}, { withAuth: false });
}

/** 提交签名登录（地址未注册则自动注册，可选邀请码） */
export async function submitAuthLogin(payload) {
  return requestReal(
    '/api/auth/login',
    { method: 'POST', body: JSON.stringify(payload) },
    { withAuth: false },
  );
}

/** 当前会话对应的钱包信息（无有效会话 → 抛错，HTTP 401） */
export async function fetchAuthMe() {
  return requestReal('/api/auth/me', {}, { withAuth: true });
}

/* ================= 充值信息 =================
 *
 * ⚠ 同样**刻意不走 mock 降级**：它下发的是**收款地址**，降级成 mock 等于让用户把钱转到假地址。
 *   失败就让调用方提示「充值通道暂不可用」，绝不给任何兜底地址。
 */
export async function fetchDepositInfo() {
  return requestReal('/api/deposit/info', {}, { withAuth: false });
}
