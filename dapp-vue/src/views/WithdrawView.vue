<script setup>
/**
 * 千万次 DApp - 提币页
 * 3% 手续费（以 TM 支付），实时计算，余额校验
 */
import { ref, computed, onMounted } from 'vue';
import { showToast } from 'vant';
import { useRouter } from 'vue-router';
import { useGameStore } from '../stores/game.js';
import { useWalletStore } from '../stores/wallet.js';
import ConnectGuard from '../components/ConnectGuard.vue';
import { formatNumber, formatPercent } from '../utils/format.js';
import { calcWithdrawFee } from '../engine/game.js';
import { BRAND, WITHDRAW_RULES, BUY_TM_URL, BUY_TM_READY } from '../constants/config.js';
import { submitWithdraw } from '../api/index.js';

const router = useRouter();
const gameStore = useGameStore();
const walletStore = useWalletStore();

const feeToken = BRAND.feeToken;
// 「获取 TM」入口（未配置或非 http(s) 链接时隐藏，见 constants/config.js）
const buyTmReady = BUY_TM_READY;
const asset = ref('FIBO');
const amount = ref('');
const submitting = ref(false);

// 资产选项
const assetOptions = ['FIBO', 'USDT'];

// 当前资产余额
const balanceOf = (sym) => gameStore.assets.balances?.[sym] || 0;

/**
 * 手续费币实时单价（U）
 *
 * ⚠ 修复（2026-09-30）：原实现直接用前端常量 BRAND.feeToken.priceUsd（写死 3.4）
 * 报价，而后端 /api/withdraw 是按**实时行情**算实扣（apiTokenPrice，兜底同为 3.4）。
 * 行情偏离 3.4 时，页面显示的应付手续费与实际扣费不一致（可能前端校验通过、后端报
 * "手续费 TM 不足"）。现在优先用 /api/prices 下发的 TM 价，仅在拿不到行情时才用常量兜底，
 * 并在界面上标明价格来源。
 */
const feeTokenPrice = computed(() => Number(gameStore.prices?.[feeToken.symbol]) || feeToken.priceUsd);
const feePriceFromBackend = computed(() => Number(gameStore.prices?.[feeToken.symbol]) > 0);

// 手续费计算（实时）
const feeInfo = computed(() => {
  const val = Number(amount.value) || 0;
  if (val <= 0) return null;
  const tokenPrice = gameStore.prices[asset.value] || 0;
  const fee = calcWithdrawFee(val, tokenPrice, feeTokenPrice.value);
  return { ...fee, tokenPrice, valueUsd: val * tokenPrice };
});

/** 全部提取 */
function fillMax() {
  amount.value = String(balanceOf(asset.value));
}

/** 切换资产 */
function selectAsset(sym) {
  asset.value = sym;
  amount.value = '';
}

/** 提交提现 */
async function handleSubmit() {
  const val = Number(amount.value);
  if (!val || val <= 0) {
    showToast('请输入提取数量');
    return;
  }
  // 最低起提额（owner 2026-09-30 敲定 100 个起提；以后端 /api/rules 的值为准，0 = 不限）。
  // 前端先拦一道只是体验；后端同样校验（见 apiadapter.Withdraw），避免绕过前端提交。
  if (WITHDRAW_RULES.minAmount > 0 && val < WITHDRAW_RULES.minAmount) {
    showToast(`最低起提 ${formatNumber(WITHDRAW_RULES.minAmount, 0)} ${asset.value}`);
    return;
  }
  if (val > balanceOf(asset.value)) {
    showToast(`${asset.value} 余额不足`);
    return;
  }
  const fee = calcWithdrawFee(val, gameStore.prices[asset.value] || 0, feeTokenPrice.value);
  if (fee.feeTokenAmount > balanceOf(feeToken.symbol)) {
    showToast(`手续费 ${feeToken.symbol} 不足，需 ${formatNumber(fee.feeTokenAmount)}`);
    return;
  }

  submitting.value = true;
  try {
    await submitWithdraw({
      address: walletStore.address,
      asset: asset.value,
      amount: val,
      feeToken: feeToken.symbol,
    });
    showToast(`提取成功：${formatNumber(val)} ${asset.value}`);
    // 刷新资产后跳转资产页
    await gameStore.refreshAssets(walletStore.address);
    router.push('/assets');
  } catch (err) {
    showToast(err?.message || '提取失败');
  } finally {
    submitting.value = false;
  }
}

onMounted(() => {
  if (walletStore.connected) {
    // 拉取最新余额与价格（不依赖其他页面访问顺序）
    gameStore
      .refreshAssets(walletStore.address)
      .catch((err) => console.warn('[withdraw] 余额加载失败', err));
  }
});
</script>

<template>
  <ConnectGuard>
    <div class="card">
      <div class="card__title">提取资产</div>

      <!-- 资产选择 -->
      <div class="flex gap-sm mb-sm">
        <button
          v-for="sym in assetOptions"
          :key="sym"
          class="btn btn--sm"
          :class="asset === sym ? 'btn--primary' : 'btn--ghost'"
          @click="selectAsset(sym)"
        >
          {{ sym }}
        </button>
      </div>

      <!-- 金额输入 -->
      <div class="modal-participate__amount">
        <input
          v-model="amount"
          class="modal-participate__input"
          type="number"
          inputmode="decimal"
          placeholder="0"
          autocomplete="off"
        />
        <div class="modal-participate__limit">
          <span>可用余额 <b class="num">{{ formatNumber(balanceOf(asset)) }} {{ asset }}</b></span>
          <span v-if="WITHDRAW_RULES.minAmount > 0" class="text-sub">最低起提 {{ formatNumber(WITHDRAW_RULES.minAmount, 0) }} {{ asset }}</span>
          <button class="btn btn--sm btn--ghost" @click="fillMax">全部提取</button>
        </div>
      </div>

      <!-- 手续费明细 -->
      <div v-if="feeInfo" class="withdraw-info">
        <div class="row">
          <span class="row__label">提取价值</span>
          <span class="row__value num">$ {{ formatNumber(feeInfo.valueUsd) }}</span>
        </div>
        <div class="row">
          <span class="row__label">手续费（{{ formatPercent(WITHDRAW_RULES.feeRate, 0) }}）</span>
          <span class="row__value text-danger num">{{ formatNumber(feeInfo.feeTokenAmount) }} {{ feeToken.symbol }}</span>
        </div>
        <div class="row">
          <span class="row__label">账户 {{ feeToken.symbol }}</span>
          <span class="row__value num">{{ formatNumber(balanceOf(feeToken.symbol)) }}</span>
        </div>      </div>
      <div v-else class="withdraw-info">输入金额后显示手续费</div>

      <button class="btn btn--primary btn--block mt-md" :disabled="submitting" @click="handleSubmit">
        {{ submitting ? '提取中...' : '确认提取' }}
      </button>
    </div>

    <!-- 手续费说明 -->
    <div class="card">
      <div class="card__title">手续费说明</div>
      <div class="rules-body">
        <p>· 提币收取 <b class="text-gold">{{ formatPercent(WITHDRAW_RULES.feeRate, 0) }}</b> 手续费，需使用 {{ feeToken.symbol }} 支付</p>
        <p>· 例：提取价值 100U 的币 → 需价值 {{ formatNumber(100 * WITHDRAW_RULES.feeRate, 0) }}U 的 {{ feeToken.symbol }}</p>
        <p>· {{ feeToken.symbol }} 当前价：<b class="num">${{ formatNumber(feeTokenPrice) }}</b>
          <span class="text-sm text-mute">（{{ feePriceFromBackend ? '来自后端行情' : '后端行情不可用，按默认价估算' }}）</span>
        </p>
        <p v-if="feeToken.burnTarget">· {{ feeToken.symbol }} 收取后销毁，直至仅剩 {{ feeToken.burnTarget }} 枚</p>
        <!-- "获取 TM" 入口：仅在配置了 VITE_BUY_TM_URL 时显示（未配置则整块隐藏，不放假链接） -->
        <p v-if="buyTmReady">
          · 没有 {{ feeToken.symbol }}？
          <a class="link" :href="BUY_TM_URL" target="_blank" rel="noopener noreferrer">获取 {{ feeToken.symbol }} →</a>
        </p>
        <p v-else-if="BUY_TM_URL" class="text-mute">
          · "获取 {{ feeToken.symbol }}"入口已填写但格式不是 http(s) 链接，已隐藏（请检查 VITE_BUY_TM_URL）
        </p>
      </div>
    </div>
  </ConnectGuard>
</template>

<style scoped>
.modal-participate__input {
  width: 100%;
  background: var(--color-bg-surface-2);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 14px 16px;
  font-size: 22px;
  font-weight: 700;
  outline: none;
  transition: border-color var(--duration-fast);
  color: var(--color-text-main);
  font-family: inherit;
}
.modal-participate__input:focus { border-color: var(--color-border-strong); }

.modal-participate__limit {
  display: flex;
  justify-content: space-between;
  font-size: 12px;
  color: var(--color-text-mute);
  margin-top: 6px;
}

.withdraw-info {
  padding: 12px;
  background: var(--color-bg-surface-2);
  border-radius: var(--radius-sm);
  font-size: 13px;
  margin-top: var(--space-sm);
}

.rules-body { color: var(--color-text-sub); font-size: 13px; line-height: 1.8; }
.rules-body p { margin-bottom: 4px; }
</style>
