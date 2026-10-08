/**
 * 千万次前端页面真实数据验证脚本
 * 用 playwright-core 驱动系统 Edge（channel msedge），打开 dev server 首页，
 * 截图 + 提取首屏文本，验证页面渲染的是后端 /api 真实数据而非 mock。
 * mock 特征：第 3 轮 target=1690；真实后端（本地造数）：第 1 轮 target=1000 raised=0
 */
import { chromium } from 'playwright-core';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

// 仓库根（脚本位于 <root>/scripts/），所有输出路径相对此目录，避免写死盘符
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const shotDir = path.join(repoRoot, '.workbuddy', 'shots');
// 前端 dev server 端口（dapp-vue/vite.config.js server.port=3002，被占用会自动顺延）
const WEB_BASE = process.env.WEB_BASE || 'http://127.0.0.1:3002';
// 后端 /api 端口（houduan/TiMi/config/etc.local.yml HttpPort=:3000）
const API_BASE = process.env.API_BASE || 'http://127.0.0.1:3000';

const browser = await chromium.launch({
  channel: 'msedge',
  executablePath: undefined,
  headless: true,
});

const page = await browser.newPage({ viewport: { width: 400, height: 800 } });

// 捕获页面发起的真实后端请求，确认走了 /api 而非 mock
const apiCalls = [];
page.on('request', (req) => {
  if (req.url().startsWith(API_BASE + '/api')) apiCalls.push(req.url());
});
page.on('console', (msg) => {
  if (msg.text().includes('[api]')) console.log('CONSOLE:', msg.text().slice(0, 160));
});

await page.goto(WEB_BASE + '/', { waitUntil: 'networkidle', timeout: 30000 }).catch(() => {});
await page.waitForTimeout(2500);

const text = await page.evaluate(() => document.body.innerText.slice(0, 900));
console.log('=== 首屏文本 ===');
console.log(text);

console.log('=== 打到真实后端的请求 ===');
console.log(apiCalls.length ? apiCalls.join('\n') : '(无，页面可能走了 mock 或未请求)');

fs.mkdirSync(shotDir, { recursive: true });
await page.screenshot({ path: path.join(shotDir, 'home_real.png') });
console.log('screenshot saved:', path.join(shotDir, 'home_real.png'));
await browser.close();
