/**
 * 千万次：前端钱包签名登录（EIP-191）—— 页面级端到端验证
 *
 * 验证目标（需求 #17「单设备登录」在 DApp 侧的落地）：
 *   1. 前端 connect() 之后**真的**走了 /api/auth/nonce + personal_sign + /api/auth/login，
 *      拿到后端会话票（而不是像改造前那样只拿地址、把 address 直传接口）。
 *   2. 会话票被带到后续 /api 请求的 `Authorization: Bearer` 头上。
 *   3. 会话票与连接地址绑定：断开/切账户后旧票立刻作废（不再带 Authorization）。
 *   4. **绝不做假登录**：签名被篡改时 login 失败 → sessionActive=false、token 清空、不带票，
 *      且界面出现「未签名」徽标。
 *   5. 后端侧独立校验：/api/auth/me 能用该票换回同一个地址；他人私钥签的原文必须被拒。
 *   6. **会话身份优先（防冒用）**：带着 A 的会话票、把写接口 body 里的 address 改成 B，
 *      必须被拒绝；不带会话票时仍沿用 body（兼容存量调用方）。
 *
 * 为什么用真实浏览器 + 桩 provider：
 *   - 签名必须在页面里由 ethers 的 signer.signMessage 触发（走 personal_sign 链路），
 *     才能覆盖「前端把服务端原文交给钱包签」这条真实路径；
 *   - 桩 provider 只实现 EIP-1193 必要方法，personal_sign 通过 page.exposeFunction
 *     回调到 Node 用同一私钥签名 —— 等价于硬件钱包签名，但不依赖任何真实钱包插件。
 *
 * 用法：node scripts/verify-wallet-login.mjs
 * 前置：npower :3000、vite dev :3002（npm run dev）已启动。
 * 本机受限沙箱下 playwright 启动 Edge 会 spawn EPERM，需放宽权限运行。
 */
import { chromium } from 'playwright-core';
import { createRequire } from 'node:module';
import path from 'node:path';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const require = createRequire(path.join(repoRoot, 'dapp-vue', 'package.json'));
const { ethers } = require('ethers');

const WEB_BASE = process.env.WEB_BASE || 'http://127.0.0.1:3002';
const API_BASE = process.env.API_BASE || 'http://127.0.0.1:3000';
const API_KEY = process.env.API_KEY || 'privchain-apikey';

// hardhat 内置账户[9]：专用测试账户，与 privchain-multiteam.ps1 用的 0..4 不冲突
const PRIVATE_KEY =
  process.env.TEST_PRIVATE_KEY ||
  '0x2a871d0798f97d79848a013d4936a73bf4cc922c825d33c1cf7073dff6d409c6';
const OTHER_PRIVATE_KEY =
  '0x8b3a350cf5c34c9194ca85829a2df0ec3153be0318b5e2d3348e872092edffba'; // 账户[5]

const wallet = new ethers.Wallet(PRIVATE_KEY);
const ADDRESS = wallet.address.toLowerCase();

let pass = 0;
let fail = 0;
function step(name, ok, detail = '') {
  if (ok) {
    console.log(`  PASS  ${name}`);
    pass++;
  } else {
    console.log(`  FAIL  ${name}${detail ? '  — ' + detail : ''}`);
    fail++;
  }
}

async function api(pathname, options = {}) {
  const res = await fetch(`${API_BASE}${pathname}`, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      'X-API-Key': API_KEY,
      ...(options.headers || {}),
    },
  });
  let body = null;
  try {
    body = await res.json();
  } catch {
    /* 非 JSON 响应 */
  }
  return { status: res.status, body };
}

console.log('='.repeat(74));
console.log(`前端钱包签名登录验证（${ADDRESS}）`);
console.log('='.repeat(74));

// ---------- 0. 前置 ----------
console.log('[0/6] 前置检查');
const health = await api('/health');
step('npower 可达', health.body?.data?.checks?.mysql === 'ok', JSON.stringify(health.body));
let webOk = false;
try {
  const r = await fetch(WEB_BASE + '/', { signal: AbortSignal.timeout(8000) });
  webOk = r.ok;
} catch {
  webOk = false;
}
if (!webOk) {
  step('vite dev server 可达', false, `${WEB_BASE} 无响应，请先 cd dapp-vue && npm run dev`);
  process.exit(1);
}
step('vite dev server 可达', true);

// ---------- 1. 启动浏览器（注入桩 provider） ----------
console.log('[1/6] 启动浏览器并注入 EIP-1193 桩 provider');
const browser = await chromium.launch({ channel: 'msedge', headless: true });
const ctx = await browser.newContext({ viewport: { width: 414, height: 896 }, deviceScaleFactor: 2 });

// personal_sign 的真实签名在 Node 侧完成（页面里没有私钥）
await ctx.exposeFunction('__testPersonalSign', async (hexMessage) => {
  return wallet.signMessage(ethers.utils.arrayify(hexMessage));
});

await ctx.addInitScript((addr) => {
  const listeners = {};
  const provider = {
    isMetaMask: true,
    name: 'TestWallet',
    _accounts: [addr],
    // 'ok' = 正常签名；'bad' = 返回一个格式合法但身份不对的签名（模拟被篡改）
    request: async ({ method, params }) => {
      switch (method) {
        case 'eth_chainId':
          return '0x3012'; // 与 constants/config.js 的 CHAIN_CONFIG.chainIdHex 一致，avoid switch 流程
        case 'eth_requestAccounts':
        case 'eth_accounts':
          return provider._accounts;
        case 'personal_sign':
          if (window.__SIGN_MODE__ === 'bad') return '0x' + 'ab'.repeat(65);
          return window.__testPersonalSign(params[0]);
        case 'wallet_switchEthereumChain':
        case 'wallet_addEthereumChain':
        case 'wallet_revokePermissions':
          return null;
        default:
          return null;
      }
    },
    on: (evt, cb) => {
      (listeners[evt] = listeners[evt] || []).push(cb);
    },
    removeListener: (evt, cb) => {
      listeners[evt] = (listeners[evt] || []).filter((f) => f !== cb);
    },
  };
  window.ethereum = provider;
  window.__SIGN_MODE__ = 'ok';
}, ADDRESS);

const page = await ctx.newPage();
const apiCalls = [];
page.on('request', (r) => {
  if (r.url().startsWith(API_BASE + '/api')) {
    const headers = r.headers();
    apiCalls.push({
      url: r.url().replace(API_BASE, ''),
      method: r.method(),
      auth: headers.authorization ? 'Bearer' : '',
    });
  }
});

await page.goto(WEB_BASE + '/#/home', { waitUntil: 'domcontentloaded', timeout: 30000 });
const armed = await page
  .waitForFunction(() => !!window.__PINIA__, null, { timeout: 15000 })
  .then(() => true)
  .catch(() => false);
step('页面加载且 Pinia 已暴露', armed);
if (!armed) {
  await browser.close();
  process.exit(1);
}

// ---------- 2. 连接钱包（应自动完成签名登录） ----------
console.log('[2/6] 连接钱包 → 自动签名登录');
const connectResult = await page.evaluate(async () => {
  const store = window.__PINIA__._s.get('wallet');
  const r = await store.connect(window.ethereum, 'TestWallet');
  return {
    r,
    state: {
      connected: store.connected,
      address: store.address,
      sessionActive: store.sessionActive,
      token: store.token,
      walletId: store.walletId,
      inviteCode: store.inviteCode,
      registered: store.registered,
      authError: store.authError,
    },
  };
});
const st = connectResult.state;
step('钱包已连接', st.connected === true, JSON.stringify(connectResult.r));
step(
  '签名登录成功（sessionActive + 拿到会话票）',
  st.sessionActive === true && typeof st.token === 'string' && st.token.length >= 32,
  JSON.stringify(st),
);
step('后端返回 walletId 与邀请码', st.walletId > 0 && /^[A-Z0-9]{8}$/.test(st.inviteCode || ''), JSON.stringify(st));
step('连接地址与预期一致', st.address === ADDRESS, `store=${st.address} expect=${ADDRESS}`);

// 登录入口确实被调用过（证明不是「只连钱包」）
const loginFlow = apiCalls.map((c) => c.url);
step(
  '登录链路走了 /api/auth/nonce 与 /api/auth/login',
  loginFlow.some((u) => u.startsWith('/api/auth/nonce')) && loginFlow.some((u) => u.startsWith('/api/auth/login')),
  loginFlow.join(' | '),
);

// ---------- 3. 会话票被带到后续 /api 请求 ----------
console.log('[3/6] 会话票随 /api 请求下发 + 后端独立校验');
const before = apiCalls.length;
await page.evaluate(() => window.__ROUTER__.push('/assets'));
await page.waitForTimeout(2500);
const after = apiCalls.slice(before).filter((c) => c.method === 'GET');
step(
  '后续 /api 读请求带上了 Authorization: Bearer',
  after.length > 0 && after.every((c) => c.auth === 'Bearer'),
  after.map((c) => `${c.url} auth=${c.auth || '(无)'}`).join(' | ') || '(未捕获到 /api 请求)',
);

const me = await api('/api/auth/me', { headers: { Authorization: `Bearer ${st.token}` } });
step(
  '/api/auth/me 用该票换回同一地址',
  me.status === 200 && me.body?.code === 0 && String(me.body?.data?.address || '').toLowerCase() === ADDRESS,
  `HTTP ${me.status} ${JSON.stringify(me.body)}`,
);

// 他人私钥对同一原文签名 → 必须被拒（证明验签不是形式主义）
const nonce2 = await api(`/api/auth/nonce?address=${ADDRESS}`);
const otherWallet = new ethers.Wallet(OTHER_PRIVATE_KEY);
const forgedSig = await otherWallet.signMessage(nonce2.body.data.message);
const forged = await api('/api/auth/login', {
  method: 'POST',
  body: JSON.stringify({ address: ADDRESS, signature: forgedSig }),
});
step('他人私钥的签名被拒绝', forged.body?.code !== 0, JSON.stringify(forged.body));

// nonce 一次性：重放刚用过的 nonce 也必须失败
const nonce3 = await api(`/api/auth/nonce?address=${ADDRESS}`);
const goodSig = await wallet.signMessage(nonce3.body.data.message);
const first = await api('/api/auth/login', {
  method: 'POST',
  body: JSON.stringify({ address: ADDRESS, signature: goodSig }),
});
const replay = await api('/api/auth/login', {
  method: 'POST',
  body: JSON.stringify({ address: ADDRESS, signature: goodSig }),
});
step(
  'nonce 一次性：首次成功、重放被拒',
  first.body?.code === 0 && replay.body?.code !== 0,
  `first=${JSON.stringify(first.body)} replay=${JSON.stringify(replay.body)}`,
);

// ---------- 4. 签名失败绝不产生假会话 ----------
console.log('[4/6] 负向：签名被篡改时不得留下假会话');
const badResult = await page.evaluate(async () => {
  const store = window.__PINIA__._s.get('wallet');
  window.__SIGN_MODE__ = 'bad'; // 让桩 provider 返回错误签名
  const r = await store.signIn();
  const state = {
    sessionActive: store.sessionActive,
    token: store.token,
    authError: store.authError,
    needsSignature: store.needsSignature,
  };
  window.__SIGN_MODE__ = 'ok';
  return { r, state };
});
step(
  '签名失败 → sessionActive=false 且 token 清空',
  badResult.state.sessionActive === false && badResult.state.token === '',
  JSON.stringify(badResult.state),
);
step('签名失败 → 记录错误原因', !!badResult.state.authError, JSON.stringify(badResult.state));

const beforeBad = apiCalls.length;
await page.evaluate(() => window.__ROUTER__.push('/referral'));
await page.waitForTimeout(2000);
const badCalls = apiCalls.slice(beforeBad).filter((c) => c.method === 'GET');
step(
  '无会话时不带 Authorization（不退化成「假装已登录」）',
  badCalls.every((c) => c.auth === ''),
  badCalls.map((c) => `${c.url} auth=${c.auth || '(无)'}`).join(' | ') || '(未捕获到 /api 请求)',
);

const badge = await page.evaluate(() => document.body.innerText.includes('未签名'));
step('界面显示「未签名」徽标', badge === true);

// ---------- 5. 重新签名 + 断开作废 ----------
console.log('[5/6] 重新签名可恢复会话；断开钱包立即作废');
const resign = await page.evaluate(async () => {
  const store = window.__PINIA__._s.get('wallet');
  const r = await store.signIn();
  return { r, sessionActive: store.sessionActive, token: store.token };
});
step('重新签名后会话恢复', resign.sessionActive === true && resign.token.length >= 32, JSON.stringify(resign.r));

const afterDisconnect = await page.evaluate(async () => {
  const store = window.__PINIA__._s.get('wallet');
  await store.disconnect(window.ethereum);
  return { connected: store.connected, sessionActive: store.sessionActive, token: store.token };
});
step(
  '断开钱包 → 连接态与会话票一起清空',
  afterDisconnect.connected === false &&
    afterDisconnect.sessionActive === false &&
    afterDisconnect.token === '',
  JSON.stringify(afterDisconnect),
);

// ---------- 6. 会话身份优先（防冒用） ----------
// 背景：/api 写接口原先只认 body 里的 address。带着自己的会话票、把 body 换成别人的地址，
// 就能提走/领走别人的资产。改为「有会话票时以会话地址为准，且不一致直接拒绝」。
console.log('[6/6] 会话身份优先：带会话改 body 地址必须被拒');
const OTHER_ADDRESS = new ethers.Wallet(OTHER_PRIVATE_KEY).address.toLowerCase();

// 上一节最后断开了钱包 → 会话与 signer 都被清空，这里重新「连接+签名登录」拿一张有效票
const relogin = await page.evaluate(async () => {
  const store = window.__PINIA__._s.get('wallet');
  const r = await store.connect(window.ethereum, 'TestWallet');
  return { ok: r.ok, token: store.token, sessionActive: store.sessionActive };
});
step('重新连接并签名登录拿到有效会话（供冒用测试使用）', relogin.ok === true && relogin.sessionActive === true, JSON.stringify(relogin));
const sessionToken = relogin.token;

// 让对照地址 B 先成为一个**真实存在**的钱包，这样"被拒"的原因才唯一指向身份校验
const nonceB = await api(`/api/auth/nonce?address=${OTHER_ADDRESS}`);
const sigB = await new ethers.Wallet(OTHER_PRIVATE_KEY).signMessage(nonceB.body.data.message);
const loginB = await api('/api/auth/login', {
  method: 'POST',
  body: JSON.stringify({ address: OTHER_ADDRESS, signature: sigB }),
});
step('对照账号 B 已注册（便于区分「身份不符」与「钱包不存在」）', loginB.body?.code === 0, JSON.stringify(loginB.body));

async function participate(bodyAddr, token) {
  return api('/api/participate', {
    method: 'POST',
    headers: token ? { Authorization: `Bearer ${token}` } : {},
    // roundId 用一个不存在的轮次：只要没被「会话不一致」拦下，就会走到「该轮不存在」
    body: JSON.stringify({ address: bodyAddr, roundId: 999999, amount: 1, asset: 'TM' }),
  });
}
const spoof = await participate(OTHER_ADDRESS, sessionToken);
step(
  '带 A 的会话、body 写 B 的地址 → 被拒（请求地址与登录会话不一致）',
  spoof.body?.code !== 0 && String(spoof.body?.message || '').includes('会话'),
  JSON.stringify(spoof.body),
);
const selfCall = await participate(ADDRESS, sessionToken);
step(
  '带 A 的会话、body 也是 A → 不放行「身份不符」这条错误（按正常业务逻辑处理）',
  !String(selfCall.body?.message || '').includes('会话'),
  JSON.stringify(selfCall.body),
);
const noSession = await participate(OTHER_ADDRESS, '');
step(
  '不带会话票时仍沿用 body（兼容存量前端与脚本直连）',
  !String(noSession.body?.message || '').includes('会话'),
  JSON.stringify(noSession.body),
);

await browser.close();

console.log('='.repeat(74));
console.log(`结果：PASS=${pass} / FAIL=${fail}`);
console.log('='.repeat(74));

// 证据落盘（供人工复核）
const shotDir = path.join(repoRoot, '.workbuddy', 'shots');
fs.mkdirSync(shotDir, { recursive: true });
fs.writeFileSync(
  path.join(shotDir, 'wallet-login-report.json'),
  JSON.stringify({ address: ADDRESS, pass, fail, apiCalls }, null, 2),
  'utf8',
);

process.exit(fail);
