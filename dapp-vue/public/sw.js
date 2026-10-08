/**
 * 千万次 DApp - Service Worker（极简 PWA 缓存）
 *
 * 目标：让 DApp 在手机浏览器上「可安装」（添加到主屏幕），并在弱网/离线时至少能打开外壳。
 *
 * 缓存策略（刻意保守，避免"缓存把行情/余额冻住"这类事故）：
 *  - 导航请求（HTML）：network-first，失败回落缓存的 index.html（离线可开壳）
 *  - 同源静态资源（/assets/*.js|css、图标、manifest）：cache-first（内容哈希文件名，天然安全）
 *  - **/api 请求：完全不拦截、不缓存**（余额/轮次/行情必须实时；缓存会造成"假数据"）
 *
 * 版本升级：修改 CACHE_VERSION 即生成新缓存，activate 时清理旧缓存。
 * ⚠ 不改这里就不会更新缓存，因为文件名是哈希的、index.html 走 network-first。
 */

const CACHE_VERSION = 'ncf-v1';
const SHELL_CACHE = `${CACHE_VERSION}-shell`;

/** 外壳资源：缺失也不阻塞安装（install 用 allSettled） */
const SHELL_ASSETS = ['./', './index.html', './manifest.webmanifest', './icon-192.png', './icon-512.png'];

self.addEventListener('install', (event) => {
  event.waitUntil(
    (async () => {
      const cache = await caches.open(SHELL_CACHE);
      // 逐个 add，任一失败不影响其它（离线构建/图标缺失时仍能装上）
      await Promise.allSettled(SHELL_ASSETS.map((url) => cache.add(new Request(url, { cache: 'reload' }))));
      await self.skipWaiting();
    })(),
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      const keys = await caches.keys();
      await Promise.all(keys.filter((k) => !k.startsWith(CACHE_VERSION)).map((k) => caches.delete(k)));
      await self.clients.claim();
    })(),
  );
});

self.addEventListener('fetch', (event) => {
  const { request } = event;

  // 只处理 GET；POST/PUT（参投/提现/领取）一律直连网络
  if (request.method !== 'GET') return;

  const url = new URL(request.url);

  // 跨域请求（钱包 RPC、行情源等）不拦截：交给浏览器与钱包自己处理
  if (url.origin !== self.location.origin) return;

  // /api 一律直连，绝不缓存（缓存余额/轮次=给用户看假数据）
  if (url.pathname.startsWith('/api/')) return;

  // 页面导航：network-first，离线回落外壳
  if (request.mode === 'navigate') {
    event.respondWith(
      (async () => {
        try {
          const fresh = await fetch(request);
          const cache = await caches.open(SHELL_CACHE);
          cache.put('./index.html', fresh.clone());
          return fresh;
        } catch {
          const cache = await caches.open(SHELL_CACHE);
          return (await cache.match('./index.html')) || (await cache.match('./')) || Response.error();
        }
      })(),
    );
    return;
  }

  // 同源静态资源：cache-first + 后台回填
  event.respondWith(
    (async () => {
      const cache = await caches.open(SHELL_CACHE);
      const hit = await cache.match(request);
      if (hit) return hit;
      const fresh = await fetch(request);
      if (fresh && fresh.ok && fresh.type === 'basic') {
        cache.put(request, fresh.clone());
      }
      return fresh;
    })(),
  );
});
