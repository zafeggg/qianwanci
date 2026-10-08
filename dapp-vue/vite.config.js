/**
 * N次方 DApp - Vite 配置
 * - 移动端构建目标：es2015+（兼容钱包 WebView）
 * - 自定义端口与 host，便于手机局域网访问
 * - Vant 采用「全量样式 + 手动 app.use 注册组件」方案（见 src/main.js），
 *   后续可切换 unplugin-vue-components 按需引入优化体积
 * - 生产构建输出 dist/，可部署任意静态服务器
 */
import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import { fileURLToPath, URL } from 'node:url';

export default defineConfig({
  // 静态资源用**相对路径**（2026-09-30）：
  //  - 默认 base:'/' 只能部署在域名根目录，子路径（如 https://host/dapp/）会 404；
  //  - 移动端打包（Capacitor/WebView）对绝对路径也更敏感。
  // 路由是 hash 模式（#/home），相对路径不影响前端路由解析。
  base: './',
  plugins: [vue()],
  resolve: {
    alias: {
      // @ -> src 目录别名，便于深层 import
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    host: true,          // 监听 0.0.0.0，手机可通过局域网 IP 访问
    port: 3002,          // 与纯 HTML 版同端口习惯（3001 被占用，用 3002）
    strictPort: false,
  },
  build: {
    target: 'es2015',
    outDir: 'dist',
    chunkSizeWarningLimit: 1500, // ethers 较大，放宽告警阈值
  },
});
