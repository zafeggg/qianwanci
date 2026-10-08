<script setup>
/**
 * 千万次 DApp - 钱包选择弹窗
 * 发现可用钱包（EIP-6963 + window.ethereum 注入），列表选择后连接
 */
import { ref, watch, onMounted } from 'vue';
import { showToast } from 'vant';
import { useWalletStore } from '../stores/wallet.js';
import { discoverWallets, resolveFigBoxProvider } from '../services/wallet.js';
import { CHAIN_CONFIG } from '../constants/config.js';

const props = defineProps({
  show: { type: Boolean, default: false },
});
const emit = defineEmits(['update:show', 'connected']);

const walletStore = useWalletStore();
// 链名跟随配置（私链联调时不再是硬编码的 FIBONACCI）
const chainName = CHAIN_CONFIG.chainName;
const wallets = ref([]);
const scanning = ref(false);
const connectingName = ref('');

// 弹窗打开时扫描钱包
onMounted(() => {
  watch(
    () => props.show,
    async (val) => {
      if (val && wallets.value.length === 0) {
        scanning.value = true;
        try {
          wallets.value = await discoverWallets();
        } finally {
          scanning.value = false;
        }
      }
    },
  );
});

/** 选择钱包并连接 */
async function handleConnect(wallet) {
  if (walletStore.connecting) return;
  connectingName.value = wallet.name;
  try {
    // FigBox 固定入口（provider 为 null 占位）→ 解析真实 provider
    // 与 GYT dapp selectWallet('fibo') 一致：恒走 window.ethereum
    // （FigBox 与小狐狸共享注入点，内置浏览器中 window.ethereum 即 FigBox）
    let provider = wallet.provider;
    if (!provider && wallet.walletId === 'fibo') {
      provider = resolveFigBoxProvider();
      if (!provider) {
        showToast('未检测到 FIGBOX 钱包，请确认已在 FIGBOX 中开启 DApp 注入后重试');
        return;
      }
    }
    // 其他钱包无 provider（异常兜底）
    if (!provider) {
      showToast('未检测到钱包注入，请安装钱包后重试');
      return;
    }

    const result = await walletStore.connect(provider, wallet.name);
    if (result.ok) {
      // 连接成功 ≠ 登录成功：签名登录失败时明确告知（否则用户会以为凭据已到手）
      if (result.sessionActive) {
        showToast(walletStore.sessionReused ? '钱包连接成功（复用已有会话）' : '钱包连接并签名登录成功');
      } else {
        showToast(result.authError ? `已连接，签名登录失败：${result.authError}` : '已连接，但未完成签名登录');
      }
      emit('update:show', false);
      emit('connected', result.address);
    } else {
      showToast(result.error || '连接失败');
    }
  } finally {
    connectingName.value = '';
  }
}

/** 关闭 */
function close() {
  emit('update:show', false);
}

/** 重新签名（会话过期 / 首次签名被拒后的自救入口） */
async function handleReSign() {
  if (walletStore.signingIn) return;
  const r = await walletStore.signIn();
  if (r.ok) {
    showToast('已重新签名登录');
    emit('update:show', false);
  } else {
    showToast(r.error || '签名失败，请重试');
  }
}

/**
 * 断开钱包 / 退出登录
 * 修复前的缺口：store 里有 disconnect()，但界面上没有任何入口，
 * 用户既无法换钱包，也无法在会话异常后主动清掉本地票据。
 */
async function handleDisconnect() {
  try {
    await walletStore.disconnect();
    showToast('已断开钱包并清除登录会话');
  } catch (err) {
    showToast(err?.message || '断开失败');
  } finally {
    emit('update:show', false);
  }
}

/**
 * 判断钱包图标是否为 data URI（EIP-6963 广播的真图标，如 MetaMask/TP 的 SVG base64）
 * data URI 必须用 <img> 渲染；若按文本插值会铺出一长串 base64 字符（已线上踩坑）
 * 注意：SVG 在 <img> 中不执行脚本，无 XSS 风险
 */
function isDataUri(icon) {
  return typeof icon === 'string' && icon.startsWith('data:');
}
</script>

<template>
  <van-popup
    :show="props.show"
    position="bottom"
    round
    :style="{ maxHeight: '70vh', paddingBottom: 'env(safe-area-inset-bottom)' }"
    @update:show="close"
  >
    <div class="wallet-modal">
      <div class="wallet-modal__header">
        <div class="wallet-modal__title">{{ walletStore.connected ? '钱包' : '连接钱包' }}</div>
        <button class="wallet-modal__close" @click="close">✕</button>
      </div>

      <!-- 已连接：显示当前地址与登录态，并提供重新签名 / 断开入口 -->
      <div v-if="walletStore.connected" class="wallet-connected">
        <div class="wallet-connected__addr num">{{ walletStore.address }}</div>
        <div class="wallet-connected__state">
          <span :class="walletStore.sessionActive ? 'text-success' : 'text-danger'">
            {{ walletStore.sessionActive ? '已签名登录（会话有效）' : '未签名登录' }}
          </span>
          <span v-if="walletStore.sessionReused" class="text-sm text-mute">（复用已存会话）</span>
        </div>
        <p v-if="walletStore.authError" class="text-sm text-danger mt-sm">{{ walletStore.authError }}</p>
        <button
          class="btn btn--primary btn--block mt-md"
          :disabled="walletStore.signingIn"
          @click="handleReSign"
        >
          {{ walletStore.signingIn ? '签名中...' : (walletStore.sessionActive ? '重新签名' : '签名登录') }}
        </button>
        <button class="btn btn--ghost btn--block mt-sm" @click="handleDisconnect">断开钱包 / 退出登录</button>
      </div>

      <!-- 未连接：才展示钱包发现列表 -->
      <template v-if="!walletStore.connected">
      <!-- 扫描中 -->
      <div v-if="scanning" class="empty">
        <div class="empty__icon">⏳</div>
        <p>正在扫描钱包...</p>
      </div>

      <!-- 无钱包 -->
      <div v-else-if="wallets.length === 0" class="empty">
        <div class="empty__icon">📱</div>
        <p>未检测到钱包</p>
        <p class="text-sm text-mute mt-sm">
          请使用 DApp 内置浏览器访问，<br />或安装 MetaMask / TokenPocket 后刷新
        </p>
      </div>

      <!-- 钱包列表 -->
      <div v-else class="wallet-modal__list">
        <button
          v-for="w in wallets"
          :key="w.uid || w.rdns || w.name"
          class="wallet-option"
          :disabled="walletStore.connecting"
          @click="handleConnect(w)"
        >
          <div class="wallet-option__logo">
            <img
              v-if="isDataUri(w.icon)"
              :src="w.icon"
              :alt="w.name"
              class="wallet-option__logo-img"
            />
            <template v-else>{{ w.icon }}</template>
          </div>
          <div class="wallet-option__info">
            <div class="wallet-option__name">{{ w.name }}</div>
            <div class="wallet-option__desc">
              {{
                connectingName === w.name
                  ? '连接中...'
                  : w.walletId === 'fibo'
                    ? 'FIBO 主网钱包，请确认已开启 DApp 注入'
                    : w.rdns === 'injected'
                      ? '浏览器注入'
                      : w.rdns
              }}
            </div>
          </div>
        </button>
        <p class="text-sm text-mute text-center mt-md">
          首次连接需授权，请确认切换至 {{ chainName }} 网络
        </p>
      </div>
      </template>
    </div>
  </van-popup>
</template>

<style scoped>
.wallet-modal {
  padding: 16px;
}

.wallet-modal__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
}

.wallet-modal__title {
  font-size: 17px;
  font-weight: 700;
}

/* 已连接面板：地址 + 登录态 + 重新签名/断开 */
.wallet-connected {
  background: var(--color-bg-surface-2);
  border-radius: var(--radius-md);
  padding: 14px;
}
.wallet-connected__addr {
  font-size: 13px;
  word-break: break-all;
  color: var(--color-primary-strong);
}
.wallet-connected__state {
  margin-top: 6px;
  font-size: 13px;
}

.wallet-modal__close {
  width: 30px;
  height: 30px;
  border-radius: 50%;
  background: var(--color-bg-surface-2);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 16px;
  color: var(--color-text-mute);
  border: none;
  cursor: pointer;
}

.wallet-option {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px;
  background: var(--color-bg-surface-2);
  border-radius: var(--radius-md);
  margin-bottom: var(--space-sm);
  width: 100%;
  border: 1px solid transparent;
  cursor: pointer;
  transition: border-color var(--duration-fast);
  color: inherit;
  font-family: inherit;
}

.wallet-option:active { border-color: var(--color-border-strong); }
.wallet-option:disabled { opacity: 0.6; }

.wallet-option__logo {
  width: 40px;
  height: 40px;
  border-radius: 10px;
  background: var(--color-primary-dim);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 20px;
  flex-shrink: 0;
  overflow: hidden;
}

/* EIP-6963 真图标（data URI）填满 logo 容器 */
.wallet-option__logo-img {
  width: 100%;
  height: 100%;
  object-fit: contain;
}

.wallet-option__info { text-align: left; }
.wallet-option__name { font-weight: 600; font-size: 15px; }
.wallet-option__desc { font-size: 12px; color: var(--color-text-mute); }
</style>
