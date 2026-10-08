/**
 * 千万次 DApp - 应用入口（Vue 3）
 * 初始化 Pinia / Router，挂载 App
 *
 * Vant 接入说明（手动注册方案）：
 * - 模板组件（van-popup / van-popup 等）通过 app.use 手动全局注册
 *   —— 若使用 unplugin-vue-components 自动按需，可移除手动注册
 * - 样式采用全量 vant/lib/index.css（简单可靠；后续可切按需引入优化体积）
 * - 函数式 API（showToast）随全量样式一并覆盖
 */

import { createApp } from 'vue';
import { createPinia } from 'pinia';
// Vant 组件全局注册（本项目用到的：Popup 底部弹窗）
import { Popup } from 'vant';
import App from './App.vue';
import router from './router/index.js';
// 启动时把后端"实际生效"的规则数值同步到本地规则对象（见文件末尾调用与 constants/rules.js）
import { loadBackendRules } from './constants/rules.js';

// Vant 全量样式（含 Popup / Toast / 按钮等）
import 'vant/lib/index.css';
import './styles/global.css';

const app = createApp(App);
const pinia = createPinia();
app.use(pinia);
// 暴露 pinia / router 实例：供自动化验证脚本（scripts/capture-pages.mjs）在页面内驱动路由与状态，
// 免去为测试单独开后门接口。仅页面内脚本可访问。
window.__PINIA__ = pinia;
app.use(router);
window.__ROUTER__ = router;
// 注册 Vant 组件（关键：未注册时 <van-popup> 会被当作未知元素渲染，弹窗内容将直接显示在页面上）
app.use(Popup);
app.mount('#app');

/**
 * 同步后端规则数值（2026-09-30 新增）
 *
 * 规则比例此前在前端硬编码一份、后端结算用另一份（可经 /ops/params 热改）→ 页面数字可能≠结算数字。
 * 这里启动时拉一次 `GET /api/rules`（后端实际生效值）覆盖本地默认值；**失败只告警不阻塞**，
 * 本地默认值继续兜底（页面仍可打开，只是可能显示的是默认口径）。
 * 详见 src/constants/rules.js。
 */
loadBackendRules()
  .then((ok) => {
    if (!ok) console.warn('[rules] 未使用后端规则数值，沿用本地默认值');
  })
  .catch((err) => console.warn('[rules] 规则数值同步异常（不影响使用）', err));

/**
 * Service Worker 注册（PWA：可安装到手机主屏 + 离线打开外壳）
 *
 * 只在**生产构建**且浏览器支持时注册：
 *  - dev 下注册会缓存 dev 资源，热更新后容易拿到旧文件（Vite 官方建议 dev 关闭 SW）
 *  - 注册失败只打警告，绝不能影响应用启动（部分内置浏览器/隐私模式不支持）
 * 缓存策略见 public/sw.js：/api 永不缓存，静态资源 cache-first，页面 network-first。
 */
if (import.meta.env.PROD && 'serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    navigator.serviceWorker
      .register('./sw.js')
      .catch((err) => console.warn('[pwa] Service Worker 注册失败（不影响使用）', err));
  });
}
