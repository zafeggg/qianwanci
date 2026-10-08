<script setup>
/**
 * 千万次 DApp - 应用外壳
 * 顶部栏（标题 + 钱包 chip）+ 路由视图 + 底部 TabBar
 */
import { computed, onMounted, ref } from 'vue';
import { useRoute } from 'vue-router';
import { showToast } from 'vant';
import { useWalletStore } from './stores/wallet.js';
import { API_BASE_UNCONFIGURED, MOCK_ACTIVE } from './api/index.js';
import WalletModal from './components/WalletModal.vue';

const route = useRoute();
const walletStore = useWalletStore();

/**
 * 数据可信度横幅（不可关闭，必须显眼）
 * 背景：原实现后端不可用时会**静默**降级成本地模拟数据，用户看到的是正常界面与编造余额/
 * 收益（甚至"提取成功 + 随机 txHash"）。现在 mock 默认关闭，且开启时必须在界面上一眼可见。
 */
const apiBaseUnconfigured = API_BASE_UNCONFIGURED;
const mockActive = MOCK_ACTIVE;

/** 重新签名（会话过期 / 签名失败后的唯一自救入口） */
async function reSign() {
  if (walletStore.signingIn) return;
  const r = await walletStore.signIn();
  if (r.ok) showToast('已重新签名登录');
  else showToast(r.error || '签名失败，请重试');
}

// 当前路由标题（来自路由 meta）
const pageTitle = computed(() => route.meta.title || '轮次');

// 钱包 chip 显示文案
const walletChipText = computed(() => {
  if (!walletStore.connected) return '连接钱包';
  const addr = walletStore.address;
  const short = addr ? `${addr.slice(0, 6)}...${addr.slice(-4)}` : '';
  return walletStore.walletName ? `${walletStore.walletName} · ${short}` : short;
});

// 底部导航项
const tabs = [
  { path: '/home', label: '轮次', icon: '🎯' },
  { path: '/assets', label: '资产', icon: '💰' },
  { path: '/referral', label: '动态', icon: '🤝' },
  { path: '/miner', label: '挖矿', icon: '⛏️' },
  { path: '/withdraw', label: '提币', icon: '💸' },
];

// 钱包弹窗开关
const showWalletModal = ref(false);

onMounted(() => {
  // 监听钱包服务分发的全局事件（账户切换 / 断开）
  walletStore._listenGlobalEvents();
});
</script>

<template>
  <div class="app-shell">
    <!-- 顶部栏 -->
    <header class="app-header">
      <div class="app-header__title">
        <span class="app-header__logo">千万次</span>
        <span class="app-header__divider">·</span>
        <span class="app-header__page">{{ pageTitle }}</span>
      </div>
      <button class="wallet-chip" :class="{ 'wallet-chip--connected': walletStore.connected }" @click="showWalletModal = true">
        <span class="wallet-chip__dot"></span>
        <span>{{ walletChipText }}</span>
        <!-- 已连接但没拿到会话票（签名被拒/后端不可用）：显式提示 + 点击重新签名。
             修复前这里只是个静态徽标，没有任何"重新签名"入口，签名失败后用户只能断开重连。 -->
        <span
          v-if="walletStore.needsSignature"
          class="wallet-chip__badge wallet-chip__badge--action"
          @click.stop="reSign"
        >{{ walletStore.signingIn ? '签名中…' : '未签名 · 点此签名' }}</span>
      </button>
    </header>

    <!-- 全局数据可信度横幅（不可关闭）：配置缺失 / 演示数据都必须一眼可见 -->
    <div v-if="apiBaseUnconfigured" class="banner banner--danger">
      ⚠ 未配置后端接口地址（VITE_API_BASE）：当前无法与服务器通信，请部署前配置并重新构建
    </div>
    <div v-else-if="mockActive" class="banner banner--warn">
      🧪 演示数据（VITE_ENABLE_MOCK=true）：页面数字为本地模拟，**不代表真实账本**
    </div>

    <!-- 路由视图 -->
    <main class="page">
      <!--
        ⚠ 2026-09-21：原先用 <transition name="page-fade" mode="out-in"> 包裹 <component :is>，
        实测在无动画/无头环境下 **第一次路由切换之后新视图不再挂载（<main> 变空、只剩底部 tab）**：
        out-in 模式要等离场动画回调才会插入新视图，回调未触发就永久停在空态。
        逐页「整页加载」时一切正常，证明各视图本身没问题，问题只出在过渡机制。
        过渡是纯装饰，这里直接去掉以恢复可用性；如需恢复动画，请改用不依赖离场回调的写法
        （例如去掉 mode="out-in"，或改为对内容做 CSS 动画而非 <transition>）。
      -->
      <router-view />
    </main>

    <!-- 底部导航（含规则入口） -->
    <nav class="tabbar">
      <router-link
        v-for="tab in tabs"
        :key="tab.path"
        :to="tab.path"
        class="tabbar__item"
        :class="{ 'is-active': route.path === tab.path }"
      >
        <span class="tabbar__icon">{{ tab.icon }}</span>
        <span>{{ tab.label }}</span>
      </router-link>
      <router-link
        to="/rules"
        class="tabbar__item"
        :class="{ 'is-active': route.path === '/rules' }"
      >
        <span class="tabbar__icon">📖</span>
        <span>规则</span>
      </router-link>
    </nav>

    <!-- 钱包选择弹窗 -->
    <WalletModal v-model:show="showWalletModal" />
  </div>
</template>

<style scoped>
.app-shell {
  max-width: var(--page-max-width);
  margin: 0 auto;
  min-height: 100vh;
  padding-bottom: calc(var(--tabbar-height) + env(safe-area-inset-bottom));
  position: relative;
}

.app-header {
  position: sticky;
  top: 0;
  z-index: var(--z-header);
  height: var(--header-height);
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 var(--space-md);
  background: rgba(13, 11, 26, 0.86);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  border-bottom: 1px solid var(--color-border);
}

.app-header__title {
  font-size: 17px;
  font-weight: 700;
  letter-spacing: 1px;
  display: flex;
  align-items: center;
  gap: 6px;
}

.app-header__logo { color: var(--color-primary); }
.app-header__divider { color: var(--color-text-mute); }
.app-header__page { font-size: 14px; color: var(--color-text-sub); font-weight: 500; }

.wallet-chip {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  border-radius: var(--radius-full);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border);
  font-size: 12px;
  color: var(--color-text-sub);
  transition: border-color var(--duration-fast);
  cursor: pointer;
  max-width: 46vw;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.wallet-chip--connected {
  border-color: var(--color-border-strong);
  color: var(--color-primary-strong);
}

.wallet-chip__dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--color-text-mute);
  flex-shrink: 0;
}

.wallet-chip--connected .wallet-chip__dot {
  background: var(--color-success);
  box-shadow: 0 0 6px var(--color-success);
}

/* 未签名徽标：连接钱包只证明「能读这个地址」，签名登录才证明「地址是本人的」 */
.wallet-chip__badge {
  flex-shrink: 0;
  padding: 1px 5px;
  border-radius: var(--radius-full);
  background: var(--color-primary-dim);
  color: var(--color-primary-strong);
  font-size: 10px;
  line-height: 14px;
}

/* 可点击的徽标（重新签名入口） */
.wallet-chip__badge--action {
  cursor: pointer;
  border: 1px solid var(--color-primary);
  background: var(--color-primary-strong);
  color: #fff;
}

/* 全局横幅：配置缺失（红）与演示数据（黄）都是"数据不可信"级别，不可关闭 */
.banner {
  position: sticky;
  top: var(--header-height);
  z-index: var(--z-header);
  padding: 8px var(--space-md);
  font-size: 12px;
  line-height: 1.5;
  text-align: center;
}

.banner--danger {
  background: rgba(255, 77, 79, 0.16);
  border-bottom: 1px solid rgba(255, 77, 79, 0.5);
  color: #ffb3b3;
}

.banner--warn {
  background: rgba(250, 173, 20, 0.16);
  border-bottom: 1px solid rgba(250, 173, 20, 0.5);
  color: #ffe08a;
}

.tabbar {
  position: fixed;
  bottom: 0;
  left: 50%;
  transform: translateX(-50%);
  width: 100%;
  max-width: var(--page-max-width);
  height: calc(var(--tabbar-height) + env(safe-area-inset-bottom));
  padding-bottom: env(safe-area-inset-bottom);
  display: flex;
  background: rgba(19, 15, 40, 0.96);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  border-top: 1px solid var(--color-border);
  z-index: var(--z-tabbar);
}

.tabbar__item {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 3px;
  font-size: 11px;
  color: var(--color-text-mute);
  transition: color var(--duration-fast);
}

.tabbar__item.is-active { color: var(--color-primary); }
.tabbar__icon { font-size: 20px; line-height: 1; }

/* 页面切换过渡 */
.page-fade-enter-active,
.page-fade-leave-active {
  transition: opacity var(--duration-normal) var(--ease-standard),
    transform var(--duration-normal) var(--ease-standard);
}
.page-fade-enter-from { opacity: 0; transform: translateY(8px); }
.page-fade-leave-to { opacity: 0; transform: translateY(-4px); }
</style>
