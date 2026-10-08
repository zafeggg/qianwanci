/**
 * 千万次 DApp - 路由配置
 * hash 模式（钱包内嵌 WebView 兼容），视图懒加载
 */

import { createRouter, createWebHashHistory } from 'vue-router';

const routes = [
  { path: '/', redirect: '/home' },
  {
    path: '/home',
    name: 'home',
    component: () => import('../views/HomeView.vue'),
    meta: { title: '轮次', tab: true },
  },
  {
    path: '/assets',
    name: 'assets',
    component: () => import('../views/AssetsView.vue'),
    meta: { title: '资产', tab: true },
  },
  {
    path: '/referral',
    name: 'referral',
    component: () => import('../views/ReferralView.vue'),
    meta: { title: '动态', tab: true },
  },
  {
    path: '/miner',
    name: 'miner',
    component: () => import('../views/MinerView.vue'),
    meta: { title: '挖矿', tab: true },
  },
  {
    path: '/withdraw',
    name: 'withdraw',
    component: () => import('../views/WithdrawView.vue'),
    meta: { title: '提币', tab: true },
  },
  {
    path: '/rules',
    name: 'rules',
    component: () => import('../views/RulesView.vue'),
    meta: { title: '规则' },
  },
];

const router = createRouter({
  history: createWebHashHistory(),
  routes,
  scrollBehavior: () => ({ top: 0 }),
});

// 路由切换后更新页面标题
router.afterEach((to) => {
  document.title = to.meta.title ? `千万次 - ${to.meta.title}` : '千万次';
});

export default router;
