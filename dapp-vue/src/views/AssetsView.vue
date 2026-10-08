<script setup>
/**
 * 千万次 DApp - 我的资产页
 * 资产总览 / 币种余额 / 仓位列表（三进一出结算状态）
 */
import { onMounted, computed, ref } from 'vue';
import { useGameStore } from '../stores/game.js';
import { useWalletStore } from '../stores/wallet.js';
import ConnectGuard from '../components/ConnectGuard.vue';
import DepositModal from '../components/DepositModal.vue';
import { formatNumber } from '../utils/format.js';

const gameStore = useGameStore();
const walletStore = useWalletStore();

// 充值弹窗：链上转账 + 收款地址引导（原来完全没有充值入口，用户只能自己找地址转）
const showDeposit = ref(false);

// 资产总估值
const totalValue = computed(() => gameStore.assets.totalValueUsd || 0);
const totalProfit = computed(() => gameStore.assets.totalProfitUsd || 0);
// 待结算仓位数量
const pendingCount = computed(
  () => gameStore.assets.positions.filter((p) => p.status === 'pending').length,
);

// 余额条目（带美元估值）
const balanceEntries = computed(() =>
  Object.entries(gameStore.assets.balances || {}).map(([symbol, amount]) => ({
    symbol,
    amount,
    usd: amount * (gameStore.prices[symbol] || 0),
  })),
);

// 仓位状态映射
const positionStatusMap = {
  pending: { text: '待结算', cls: 'tag--info' },
  settled: { text: '已结算', cls: 'tag--success' },
  refund: { text: '已退本', cls: 'tag--warning' },
  penalty: { text: '扣半转算力', cls: 'tag--danger' },
};

/** 仓位卡片状态色条 */
function positionCardClass(status) {
  if (status === 'penalty') return 'position-card--liquidated';
  if (status === 'settled') return 'position-card--settled';
  return 'position-card--pending';
}

onMounted(() => {
  if (walletStore.connected) {
    gameStore.refreshAssets(walletStore.address).catch((err) => console.error('[assets]', err));
  }
});
</script>

<template>
  <ConnectGuard>
    <!-- 资产总览 -->
    <div class="card card--highlight asset-overview">
      <div class="text-sub">资产总估值</div>
      <div class="asset-overview__value num">$ {{ formatNumber(totalValue) }}</div>
      <div class="asset-overview__row">
        <div class="round-stats__cell">
          <div class="round-stats__label">累计收益</div>
          <div class="round-stats__value text-success num">$ {{ formatNumber(totalProfit) }}</div>
        </div>
        <div class="round-stats__cell">
          <div class="round-stats__label">待结算仓位</div>
          <div class="round-stats__value num">{{ pendingCount }}</div>
        </div>
        <div class="round-stats__cell">
          <div class="round-stats__label">FIBO 余额</div>
          <div class="round-stats__value num">{{ formatNumber(gameStore.assets.balances.FIBO ?? 0) }}</div>
        </div>
      </div>
    </div>

    <!-- 币种余额 -->
    <div class="card">
      <div class="card__title">币种余额</div>
      <div v-if="!balanceEntries.length" class="empty"><p>暂无余额</p></div>
      <div v-else>
        <div v-for="e in balanceEntries" :key="e.symbol" class="row">
          <span class="row__label">
            <b>{{ e.symbol }}</b>
            <span class="text-mute text-sm"> ≈ ${{ formatNumber(e.usd) }}</span>
          </span>
          <span class="row__value num">{{ formatNumber(e.amount) }}</span>
        </div>
      </div>
      <!-- 充值入口：余额不足时的第一步就是把币充进来 -->
      <button class="btn btn--primary btn--block mt-md" @click="showDeposit = true">充值</button>
    </div>

    <DepositModal
      v-model:show="showDeposit"
      @deposited="gameStore.refreshAssets(walletStore.address).catch(() => {})"
    />

    <!-- 仓位列表 -->
    <div class="card">
      <div class="card__title">
        我的仓位
        <span class="tag tag--info">三进一出</span>
      </div>
      <div v-if="!gameStore.assets.positions.length" class="empty">
        <p>暂无仓位，去首页参与吧</p>
      </div>
      <div v-else>
        <div
          v-for="p in [...gameStore.assets.positions].reverse()"
          :key="p.id"
          class="asset-list__item position-card"
          :class="positionCardClass(p.status)"
        >
          <div class="flex-between">
            <span class="text-sub">第 {{ p.roundIndex }} 轮 · 第 {{ p.settleRound }} 轮结算</span>
            <span class="tag" :class="(positionStatusMap[p.status] || positionStatusMap.pending).cls">
              {{ (positionStatusMap[p.status] || positionStatusMap.pending).text }}
            </span>
          </div>
          <div class="flex-between mt-sm">
            <span class="row__value num text-lg">{{ formatNumber(p.amount) }}</span>
            <span class="text-sub">
              预期收益
              <b class="num" :class="p.status === 'settled' ? 'text-success' : 'text-gold'">
                +{{ formatNumber(p.expectedProfit ?? p.profit) }}
              </b>
            </span>
          </div>
          <!-- 倒2/倒3：把"实际被扣多少"写清楚（后端从账本下发 loss），避免用户以为本金全退 -->
          <div v-if="p.status === 'penalty' && p.loss" class="text-sm text-danger mt-sm">
            已被扣 <b class="num">{{ formatNumber(p.loss) }}</b> 枚并折算为算力（倒2/倒3 规则），
            剩余 {{ formatNumber(p.amount - p.loss) }} 枚已退回余额
          </div>
        </div>
      </div>
    </div>
  </ConnectGuard>
</template>

<style scoped>
.asset-overview { text-align: center; padding: var(--space-lg) var(--space-md); }
.asset-overview__value { font-size: 34px; font-weight: 800; color: var(--color-primary-strong); }
.asset-overview__row {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: var(--space-sm);
  margin-top: var(--space-md);
}

.round-stats__cell {
  background: var(--color-bg-surface-2);
  border-radius: var(--radius-sm);
  padding: 10px 6px;
  text-align: center;
}
.round-stats__label { font-size: 11px; color: var(--color-text-mute); }
.round-stats__value { font-size: 16px; font-weight: 700; margin-top: 2px; }

.asset-list__item {
  background: var(--color-bg-surface-2);
  border-radius: var(--radius-md);
  padding: 12px;
  margin-bottom: var(--space-sm);
}

.position-card { border-left: 3px solid var(--color-border); }
.position-card--pending { border-left-color: var(--color-info); }
.position-card--settled { border-left-color: var(--color-success); }
.position-card--liquidated { border-left-color: var(--color-danger); }
</style>
