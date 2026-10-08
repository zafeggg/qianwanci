/**
 * 千万次：DApp 充值入口 + TM 合约接入 —— 端到端验证
 *
 * 验证目标：
 *   1. 后端 `GET /api/deposit/info` 下发完整的充值参数（收款地址/合约/精度/确认数），
 *      且**不来自 mock**（接口刻意不做降级，地址错了就是把钱转丢）。
 *   2. 前端资产页有「充值」入口，弹窗展示的收款地址与后端下发一致，
 *      并且**真的读了链上合约**（`balanceOf`）显示用户链上余额 —— 这就是「前端接入 TM 合约」。
 *   3. 真实充值链路：用户向收款地址转 TM → 出一个块 → `cmd/evmwatch` 扫链 → 本站余额增加。
 *   4. 幂等：重复扫链不会重复入账。
 *
 * ⚠ 为什么充值方不是 DApp 默认用户（0x7099…）：
 *   私链拓扑里 `Chain.WatchPool` 收款地址就是 0x7099…（链上与库里同一条钱包，见 etc.privchain.yml 注释）。
 *   若用它自己给自己转账，会被 evmwatch 的 `from == pool` 过滤按"非充值"跳过（防出金被重复当充值）。
 *   所以本脚本用 hardhat 账户[3] 作为充值方，池保持 0x7099…。
 *
 * 用法：node scripts/verify-deposit.mjs
 * 前置：hardhat 节点 :8545 且已部署 TM；npower :3000；vite dev :3002
 */
import { chromium } from 'playwright-core';
import { createRequire } from 'node:module';
import { execFile } from 'node:child_process';
import path from 'node:path';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const require = createRequire(path.join(repoRoot, 'dapp-vue', 'package.json'));
const { ethers } = require('ethers'); // dapp-vue 用 v5，与前端保持一致

const WEB_BASE = process.env.WEB_BASE || 'http://127.0.0.1:3002';
const API_BASE = process.env.API_BASE || 'http://127.0.0.1:3000';
const RPC = process.env.RPC || 'http://127.0.0.1:8545';
const API_KEY = process.env.API_KEY || 'privchain-apikey';
const REDIS_DB = process.env.REDIS_DB || '6';

// hardhat 内置账户：0=手续费池（持有全部初始发行）、3=本脚本的充值方
const ACCT0_PK = '0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80';
const DEPOSITOR_PK = '0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6';
const depositor = new ethers.Wallet(DEPOSITOR_PK);
const DEPOSIT_AMOUNT = 25; // 充值金额（TM）

let pass = 0, fail = 0;
function step(name, ok, detail = '') {
  if (ok) { console.log(`  PASS  ${name}`); pass++; }
  else { console.log(`  FAIL  ${name}${detail ? '  — ' + detail : ''}`); fail++; }
}
async function api(pathname, options = {}) {
  const res = await fetch(`${API_BASE}${pathname}`, {
    ...options,
    headers: { 'Content-Type': 'application/json', 'X-API-Key': API_KEY, ...(options.headers || {}) },
  });
  let body = null;
  try { body = await res.json(); } catch { /* 非 JSON */ }
  return { status: res.status, body };
}
async function siteBalanceTM(address) {
  const r = await api(`/api/assets?address=${address}`);
  return Number(r.body?.data?.balances?.TM ?? 0);
}
function run(cmd, args, opts = {}) {
  return new Promise((resolve) => {
    execFile(cmd, args, { cwd: repoRoot, env: { ...process.env, ...(opts.env || {}) }, ...opts }, (err, stdout, stderr) =>
      resolve({ err, stdout: String(stdout || ''), stderr: String(stderr || '') }));
  });
}

console.log('='.repeat(74));
console.log(`DApp 充值入口 + TM 合约接入 验证（充值方 ${depositor.address}）`);
console.log('='.repeat(74));

// ---------- 0. 充值信息接口 ----------
console.log('[0/5] 后端充值信息接口');
const h = await api('/health');
step('npower 可达', h.body?.data?.checks?.mysql === 'ok', JSON.stringify(h.body));
const info = await api('/api/deposit/info');
const d = info.body?.data || {};
step('GET /api/deposit/info 返回可用参数',
  info.body?.code === 0 && d.enabled === true && /^0x[0-9a-fA-F]{40}$/.test(d.address || '') && /^0x[0-9a-fA-F]{40}$/.test(d.contract || ''),
  JSON.stringify(d));
step('精度是 ERC20 位数（8），不是记账换算倍率（1e8）', Number(d.decimals) === 8, `decimals=${d.decimals}`);
step('确认数与链配置一致（私链 = 1）', Number(d.confirmations) === 1, `confirmations=${d.confirmations}`);

const token = new ethers.Contract(d.contract, [
  'function balanceOf(address) view returns (uint256)',
  'function decimals() view returns (uint8)',
  'function transfer(address,uint256) returns (bool)',
], new ethers.Wallet(DEPOSITOR_PK, new ethers.providers.JsonRpcProvider(RPC)));

// ---------- 1. 让充值方持有 TM（从手续费池定向发放，模拟用户自己买币） ----------
console.log('[1/5] 准备充值方余额');
let beforeBal = Number(ethers.utils.formatUnits(await token.balanceOf(depositor.address), 8));
if (beforeBal < DEPOSIT_AMOUNT) {
  const r = await run(process.execPath, ['scripts/privchain-chain.mjs', 'send-token'], {
    env: { PC_ARGS: JSON.stringify({ to: depositor.address, pk: ACCT0_PK, coins: '100' }) },
  });
  // ⚠ 不要只看子进程退出码：privchain-chain.mjs 内部维护 nonce 缓存，私链重置后会残留旧 nonce
  //   而报 "Nonce too high"，但转账往往已经成功。以**链上余额是否到账**为准。
  beforeBal = Number(ethers.utils.formatUnits(await token.balanceOf(depositor.address), 8));
  if (beforeBal < DEPOSIT_AMOUNT) {
    console.log('      发放输出：' + (r.stdout + r.stderr).trim().slice(0, 300));
  }
}
const chainBal = Number(ethers.utils.formatUnits(await token.balanceOf(depositor.address), 8));
step(`充值方链上余额 = ${chainBal} TM（> 0，够本次充值）`, chainBal >= DEPOSIT_AMOUNT, `chainBal=${chainBal}`);

// ---------- 2. 充值方在站点有账号（入账对象） ----------
console.log('[2/5] 充值方注册与本站余额基线');
const nonce = await api(`/api/auth/nonce?address=${depositor.address}`);
const sig = await depositor.signMessage(nonce.body.data.message);
const login = await api('/api/auth/login', {
  method: 'POST',
  body: JSON.stringify({ address: depositor.address, signature: sig }),
});
step('充值方签名登录（首登即注册）', login.body?.code === 0, JSON.stringify(login.body));
const siteBefore = await siteBalanceTM(depositor.address);
console.log(`      本站 TM 余额基线 = ${siteBefore}`);

// ---------- 3. 浏览器：资产页充值入口 + 链上余额展示 ----------
console.log('[3/5] DApp 资产页：充值入口与链上余额');
const browser = await chromium.launch({ channel: 'msedge', headless: true });
const ctx = await browser.newContext({ viewport: { width: 414, height: 896 }, deviceScaleFactor: 2 });
await ctx.exposeFunction('__testPersonalSign', async (hexMessage) =>
  depositor.signMessage(ethers.utils.arrayify(hexMessage)));
await ctx.addInitScript((addr) => {
  const listeners = {};
  const provider = {
    isMetaMask: true, name: 'TestWallet', _accounts: [addr],
    request: async ({ method, params }) => {
      switch (method) {
        case 'eth_chainId': return '0x7a69'; // 31337，与 .env.development 的 VITE_CHAIN_ID 一致
        case 'eth_requestAccounts':
        case 'eth_accounts': return provider._accounts;
        case 'personal_sign': return window.__testPersonalSign(params[0]);
        default: return null;
      }
    },
    on: (e, cb) => { (listeners[e] = listeners[e] || []).push(cb); },
    removeListener: (e, cb) => { listeners[e] = (listeners[e] || []).filter((f) => f !== cb); },
  };
  window.ethereum = provider;
}, depositor.address);

const page = await ctx.newPage();
await page.goto(WEB_BASE + '/#/assets', { waitUntil: 'domcontentloaded', timeout: 30000 });
await page.waitForFunction(() => !!window.__PINIA__, null, { timeout: 15000 }).catch(() => {});
await page.evaluate(async () => {
  const store = window.__PINIA__._s.get('wallet');
  await store.connect(window.ethereum, 'TestWallet');
});
await page.waitForTimeout(1500);
const hasDepositBtn = await page.evaluate(() => document.body.innerText.includes('充值'));
step('资产页出现「充值」入口', hasDepositBtn);

// 点开充值弹窗
await page.evaluate(() => {
  const btn = [...document.querySelectorAll('button')].find((b) => b.innerText.trim() === '充值');
  if (btn) btn.click();
});
await page.waitForTimeout(2500);
const modalText = await page.evaluate(() => document.body.innerText);
step('弹窗展示的收款地址与后端下发一致', modalText.includes(d.address), '未在页面文本中找到收款地址');
step('弹窗展示代币合约（接入 TM 合约的可见证据）', modalText.includes(d.contract.slice(0, 10)), '未找到合约前缀');
step(`弹窗展示了链上余额（应包含 ${chainBal}）`, modalText.includes(String(chainBal)), `期望包含 ${chainBal}`);
await page.screenshot({ path: path.join(repoRoot, '.workbuddy', 'shots', 'app-deposit.png'), fullPage: true });
await browser.close();

// ---------- 4. 真实充值：转账 → 出块 → evmwatch 入账 ----------
console.log('[4/5] 真实充值链路（转账 → 出块 → 扫链入账）');
// 4.1 清掉扫块进度：私链重置后块号归零，残留的高进度会让监听以为"已扫过"而永远不再入账
const pkey = `npower:evmwatch:lastblock:${d.symbol}:${ethers.utils.getAddress(d.address)}`;
const del = await run('redis-cli', ['-n', REDIS_DB, 'DEL', pkey]);
step('已清掉扫块进度键（避免链重置后漏扫）', !del.err, (del.stdout + del.stderr).trim());

// 4.2 充值方把 TM 转给收款地址（这就是「一键充值」按钮做的事）
const tx = await token.transfer(d.address, ethers.utils.parseUnits(String(DEPOSIT_AMOUNT), 8));
const receipt = await tx.wait();
step(`充值交易已上链（${DEPOSIT_AMOUNT} TM，tx ${tx.hash.slice(0, 12)}…）`, receipt?.status === 1, `status=${receipt?.status}`);

// 4.3 再出一个块（Confirmations=1：hardhat 只在有交易时出块，不出块就永远差 1 个确认）
const bump = await new ethers.Wallet(ACCT0_PK, new ethers.providers.JsonRpcProvider(RPC))
  .sendTransaction({ to: await new ethers.Wallet(ACCT0_PK).getAddress(), value: 0 });
await bump.wait();
step('已补出一个区块（满足 1 个确认）', true);

// 4.4 扫链入账
// ⚠ 必须显式 -fromBlock 1：进度键被清掉后，`-fromBlock 0`（默认值）表示"从最新块开始"，
//   会把刚发生的那笔充值直接跳过。这里要从创世扫，才能覆盖到本次交易。
const watchExe = path.join(repoRoot, 'houduan', 'TiMi', 'build', 'evmwatch.exe');
const watchArgs = ['-f', 'config/etc.privchain.yml', '-symbol', d.symbol, '-decimals', '8', '-fromBlock', '1', '-once'];
const watchDir = path.join(repoRoot, 'houduan', 'TiMi');
const watch = await run(watchExe, watchArgs, { cwd: watchDir });
step('evmwatch 扫链执行成功', !watch.err, (watch.stdout + watch.stderr).slice(-400));
const siteAfter = await siteBalanceTM(depositor.address);
step(`本站余额入账 +${DEPOSIT_AMOUNT}（${siteBefore} → ${siteAfter}）`,
  Math.abs(siteAfter - (siteBefore + DEPOSIT_AMOUNT)) < 0.0001, `实际 +${siteAfter - siteBefore}`);

// ---------- 5. 幂等 ----------
console.log('[5/5] 幂等：重复扫链不得重复入账');
await run(watchExe, watchArgs, { cwd: watchDir });
const siteAgain = await siteBalanceTM(depositor.address);
step('再扫一次余额不变（唯一索引幂等兜底）', Math.abs(siteAgain - siteAfter) < 0.0001,
  `第一次后=${siteAfter} 第二次后=${siteAgain}`);

console.log('='.repeat(74));
console.log(`结果：PASS=${pass} / FAIL=${fail}`);
console.log('='.repeat(74));

const shotDir = path.join(repoRoot, '.workbuddy', 'shots');
fs.mkdirSync(shotDir, { recursive: true });
fs.writeFileSync(path.join(shotDir, 'deposit-report.json'),
  JSON.stringify({ depositInfo: d, depositor: depositor.address, siteBefore, siteAfter, pass, fail }, null, 2), 'utf8');

process.exit(fail);
