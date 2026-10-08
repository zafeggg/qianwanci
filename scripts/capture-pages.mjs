/**
 * 千万次：前端 DApp 全部页面 + demo 演示页截图与真实数据核对（含「已连接钱包」状态）
 *
 * 用途：把需求文档里的每条规则在真实页面上核对一遍，证据（截图 + 页面文本 + 后端请求）落盘到 .workbuddy/shots。
 *
 * 两个关键做法（都是踩过坑总结的）：
 *  1) **同一文档内用 hash 切换**：整页 reload 会重置 Pinia，钱包页在 onMounted 时 connected=false，
 *     导致「连接了钱包但页面还是空」；hash 切换不重载文档，登录态得以保留。
 *  2) **先激活应用再置登录态**：钱包页只在 onMounted 且「已连接」时取数，所以先加载一次应用、
 *     把 wallet store 直接置为已连接（app 已在 main.js 暴露 window.__PINIA__），再切到目标路由触发取数。
 *
 * ⚠ 钱包 provider 是**测试桩**：最小 EIP-1193 provider，用于让页面进入「已连接」态并**真实完成签名登录**
 *   （personal_sign 由 Node 侧用 WALLET_PK 私钥签名，见 ctx.exposeFunction('__testPersonalSign')）。
 *   页面数据全部走真实后端 /api（每页都打印了后端请求，可据此判断是否降级到 mock）。
 *   桩里 eth_chainId 返回 0x3012（主网 12306），因为前端 constants/config.js 的 CHAIN_CONFIG 硬编码主网；
 *   该桩不参与任何真实 RPC。
 *
 * 用法：node scripts/capture-pages.mjs
 * 本机受限沙箱下 playwright 启动 Edge 会 spawn EPERM，需放宽权限运行。
 */
import { chromium } from 'playwright-core';
import { createRequire } from 'node:module';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const require = createRequire(path.join(repoRoot, 'dapp-vue', 'package.json'));
const { ethers } = require('ethers');

const shotDir = path.join(repoRoot, '.workbuddy', 'shots');
const WEB_BASE = process.env.WEB_BASE || 'http://127.0.0.1:3002';
const API_BASE = process.env.API_BASE || 'http://127.0.0.1:3000';
const DEMO_BASE = process.env.DEMO_BASE || 'http://127.0.0.1:3001';
const WALLET = process.env.WALLET || '0x70997970C51812dc3A010C7d01b50e0d17dc79C8';
// hardhat 账户[1]：与默认 WALLET 配对。覆盖 WALLET 时必须同时给 WALLET_PK，否则签名登录会失败
const WALLET_PK =
  process.env.WALLET_PK ||
  '0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d';
const signerWallet = new ethers.Wallet(WALLET_PK);

const PAGES = [
  { route: '/home', name: '首页（轮次/倒计时/参与）', file: 'app-home.png', marker: '本轮总额度' },
  { route: '/assets', name: '资产（余额/仓位）', file: 'app-assets.png', marker: '资产总估值' },
  { route: '/referral', name: '动态（团队/等级）', file: 'app-referral.png', marker: '直推解锁' },
  { route: '/miner', name: '挖矿（算力/产出）', file: 'app-miner.png', marker: '出局进度' },
  { route: '/withdraw', name: '提币（3% TM 手续费）', file: 'app-withdraw.png', marker: '手续费说明' },
  { route: '/rules', name: '规则说明', file: 'app-rules.png', marker: '众筹玩法' },
];

fs.mkdirSync(shotDir, { recursive: true });

const browser = await chromium.launch({ channel: 'msedge', headless: true });
const ctx = await browser.newContext({ viewport: { width: 414, height: 896 }, deviceScaleFactor: 2 });

await ctx.exposeFunction('__testPersonalSign', async (hexMessage) => {
  return signerWallet.signMessage(ethers.utils.arrayify(hexMessage));
});

await ctx.addInitScript((addr) => {
  const listeners = {};
  const provider = {
    isMetaMask: true,
    name: 'TestWallet',
    _accounts: [addr.toLowerCase()],
    request: async ({ method, params }) => {
      switch (method) {
        case 'eth_chainId':
          return '0x3012';
        case 'eth_requestAccounts':
        case 'eth_accounts':
          return provider._accounts;
        case 'personal_sign':
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
}, WALLET);

const page = await ctx.newPage();
const apiCalls = [];
const fallbacks = [];
page.on('request', (r) => {
  if (r.url().startsWith(API_BASE + '/api')) apiCalls.push(r.url().replace(API_BASE, ''));
});
page.on('console', (m) => {
  if (m.text().includes('降级 mock')) fallbacks.push(m.text().slice(0, 200));
});

// 1) 加载应用一次（此时停在首页）
await page.goto(WEB_BASE + '/#/home', { waitUntil: 'domcontentloaded', timeout: 30000 });
await page.waitForTimeout(2500);

// 2) 等 app/pinia 就绪后，走**真实连接 + 签名登录**流程进入已登录态
//    （不再直接写 connected=true：那样只覆盖「已连接」，拿不到会话票，截图里会挂「未签名」徽标）
const armed = await page.waitForFunction(() => !!window.__PINIA__, null, { timeout: 15000 }).then(() => true).catch(() => false);
if (!armed) {
  console.log('未拿到 window.__PINIA__，请确认 dapp-vue/src/main.js 已暴露 pinia，且 dev server 已热更新');
}
const loginState = await page.evaluate(async () => {
  const walletStore = window.__PINIA__?._s?.get('wallet');
  if (!walletStore) return null;
  const r = await walletStore.connect(window.ethereum, 'MetaMask');
  return { ...r, sessionActive: walletStore.sessionActive, token: !!walletStore.token, authError: walletStore.authError };
});
if (!loginState?.sessionActive) {
  console.log('⚠ 签名登录未成功，截图将显示「未签名」徽标：' + JSON.stringify(loginState));
} else {
  console.log('钱包已连接并完成签名登录（' + WALLET + '）');
}
await page.waitForTimeout(600);

const report = [];
for (const p of PAGES) {
  const before = apiCalls.length;
  // 用 router.push 切页：同一文档内导航，Pinia 登录态保留、视图重新 mount 触发取数。
  // （直接改 location.hash 只在首次生效，之后路由不再响应，实测踩过）
  await page.evaluate((route) => window.__ROUTER__.push(route), p.route);
  // 等页面真正渲染出内容（懒加载 chunk + onMounted 取数），最多 15s
  const ready = await page
    .waitForFunction((m) => document.body.innerText.includes(m), p.marker, { timeout: 15000 })
    .then(() => true)
    .catch(() => false);
  await page.waitForTimeout(800);
  if (!ready) console.log('  ⚠ 未等到内容标记 ' + p.marker + '，页面可能仍在加载');

  const text = await page.evaluate(() => document.body.innerText.replace(/\n{2,}/g, '\n').slice(0, 1500));
  const shot = path.join(shotDir, p.file);
  await page.screenshot({ path: shot, fullPage: true });

  const calls = [...new Set(apiCalls.slice(before))];
  report.push({ route: p.route, name: p.name, shot, calls, text });
  console.log(`\n===== ${p.name}  #${p.route} =====`);
  console.log(`后端请求: ${calls.length ? calls.join(' | ') : '(无)'}`);
  console.log(text.split('\n').filter((l) => l.trim()).slice(0, 22).join('\n'));
}

// demo 网关的两个页面（纯 HTML，与 DApp 无关）
for (const [label, url, file] of [
  ['demo 演示 DApp 页', DEMO_BASE + '/', 'demo-index.png'],
  ['demo 运营后台页', DEMO_BASE + '/admin.html', 'demo-admin.png'],
]) {
  await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 30000 }).catch(() => {});
  await page.waitForTimeout(2500);
  const text = await page.evaluate(() => document.body.innerText.replace(/\n{2,}/g, '\n').slice(0, 1000));
  const shot = path.join(shotDir, file);
  await page.screenshot({ path: shot, fullPage: true });
  report.push({ route: new URL(url).pathname, name: label, shot, calls: [], text });
  console.log(`\n===== ${label}  ${url} =====`);
  console.log(text.split('\n').filter((l) => l.trim()).slice(0, 16).join('\n'));
}

await browser.close();

console.log('\n===== mock 降级情况 =====');
console.log(fallbacks.length ? fallbacks.join('\n') : '(无降级，全部走真实后端)');

fs.writeFileSync(path.join(shotDir, 'page-report.json'), JSON.stringify(report, null, 2), 'utf8');
console.log('\n截图与文本报告目录:', shotDir);
