<script setup>
/**
 * 千万次 DApp - 挖矿 / 算力页
 * 算力总览 / 三倍出局进度 / 产出模式切换 / 领取产出
 */
import { onMounted, computed } from 'vue';
import { showToast } from 'vant';
import { useGameStore } from '../stores/game.js';
import { useWalletStore } from '../stores/wallet.js';
import ConnectGuard from '../components/ConnectGuard.vue';
import { formatNumber } from '../utils/format.js';
import { MINER_RULES, MOCK_ENABLED } from '../constants/config.js';
import { calcMinerOutput } from '../engine/game.js';

const gameStore = useGameStore();
const walletStore = useWalletStore();

// 产出模式列表
const minerModes = MINER_RULES.modes;

// 当前算力
const hashrate = computed(() => gameStore.miner.hashrate || 0);

/**
 * 每日产出 / 三倍出局目标
 *
 * ⚠ 修复（2026-09-30）：原实现用前端公式（engine/game.js#calcMinerOutput，价格缺失时按 0.5 兜底）
 * 自己重算，与后端 cmd/hashpower 的结算口径（按算力占比 + 模式1/2 保底 + 4h 均价近似）可以不一致，
 * 用户看到的"每日产出/目标"和实际到账是两套数。现在一律用后端下发的 dailyOutput / targetCoin。
 * 仅当显式开启演示模式（无后端）时才回退到本地公式。
 */
const dailyOutput = computed(() =>
  MOCK_ENABLED ? calcMinerOutput(hashrate.value, gameStore.prices.FIBO || 0.5, gameStore.miner.modeId).dailyMin : gameStore.miner.dailyOutput,
);
const targetCoin = computed(() =>
  MOCK_ENABLED ? calcMinerOutput(hashrate.value, gameStore.prices.FIBO || 0.5, gameStore.miner.modeId).totalPayout : gameStore.miner.targetCoin,
);

// 出局进度（已产出 / 目标）
const payoutProgress = computed(() => {
  const target = targetCoin.value;
  if (!target) return 0;
  return Math.min(100, (gameStore.miner.payoutTotal / target) * 100);
});

/**
 * 切换产出模式（真实请求）
 * 修复前这里只写本地 state —— 页面显示"当前"变了，后端与链上什么都没发生（假交互）。
 * 现在调 POST /api/miner/mode；后端只允许调整"尚未产出且未出局"的账户，失败按后端原因提示。
 */
async function selectMode(modeId) {
  if (gameStore.miner.switching) return;
  if (modeId === gameStore.miner.modeId) return;
  if (!walletStore.sessionActive) {
    showToast('请先完成钱包签名登录');
    return;
  }
  if (!gameStore.miner.switchable) {
    showToast('已产生收益或已出局的算力账户不可修改模式');
    return;
  }
  try {
    await gameStore.switchMinerMode({
      address: walletStore.address,
      symbol: '', // 空 = 该地址全部币种的算力账户一起切换（后端口径）
      mode: modeId,
    });
    showToast('产出模式已更新');
  } catch (err) {
    showToast(err?.message || '切换失败');
  }
}

/** 领取产出 */
async function handleClaim() {
  const claimable = gameStore.miner.claimable || 0;
  if (!(claimable > 0)) {
    showToast('暂无可领取产出');
    return;
  }
  try {
    const { claimMiner } = await import('../api/index.js');
    const result = await claimMiner(walletStore.address);
    showToast(`领取成功 +${formatNumber(result.claimed)} FIBO`);
    gameStore.refreshMiner(walletStore.address);
    gameStore.refreshAssets(walletStore.address);
  } catch (err) {
    showToast(err?.message || '领取失败');
  }
}

onMounted(() => {
  if (walletStore.connected) {
    gameStore.refreshMiner(walletStore.address).catch((err) => console.error('[miner]', err));
  }
});
</script>

<template>
  <ConnectGuard>
    <!-- 算力总览 -->
    <div class="card card--highlight">
      <div class="text-sub">当前算力（U 计价）</div>
      <div class="asset-overview__value num">{{ formatNumber(hashrate) }}</div>

      <div class="round-stats">
        <div class="round-stats__cell">
          <div class="round-stats__label">每日产出</div>
          <div class="round-stats__value text-success num">{{ dailyOutput ? formatNumber(dailyOutput) : '--' }}</div>
        </div>
        <div class="round-stats__cell">
          <div class="round-stats__label">已累计产出</div>
          <div class="round-stats__value num">{{ formatNumber(gameStore.miner.payoutTotal) }}</div>
        </div>
        <div class="round-stats__cell">
          <div class="round-stats__label">出局进度</div>
          <div class="round-stats__value num">{{ Math.round(payoutProgress) }}%</div>
        </div>
      </div>

      <!-- 三倍出局进度 -->
      <div class="progress">
        <div class="progress__bar">
          <div class="progress__fill" :style="{ width: payoutProgress + '%' }"></div>
        </div>
        <div class="progress__meta">
          <span class="text-sub">已产出 <b class="num">{{ formatNumber(gameStore.miner.payoutTotal) }} 枚</b></span>
          <span class="num">目标 {{ targetCoin ? formatNumber(targetCoin) + ' 枚' : '--' }}</span>
        </div>
      </div>

      <button class="btn btn--primary btn--block mt-md" :disabled="!(gameStore.miner.claimable > 0)" @click="handleClaim">
        {{ gameStore.miner.claimable > 0 ? `领取产出（可领 ${formatNumber(gameStore.miner.claimable)} 枚）` : '暂无可领取产出' }}
      </button>
    </div>

    <!-- 产出模式 -->
    <div class="card">
      <div class="card__title">
        产出模式
        <span class="tag tag--info">金本位三倍</span>
      </div>
      <div
        v-for="mode in minerModes"
        :key="mode.id"
        class="miner-mode"
        :class="{
          'is-active': gameStore.miner.modeId === mode.id,
          'is-disabled': !gameStore.miner.switchable || gameStore.miner.switching,
        }"
        @click="selectMode(mode.id)"
      >
        <div class="miner-mode__title">
          <span>{{ mode.name }}</span>
          <span v-if="gameStore.miner.modeId === mode.id" class="tag tag--success">当前</span>
        </div>
        <div class="text-sm text-mute mt-sm">{{ mode.desc }}</div>
      </div>
      <!-- 可切换性说明：后端只允许调整"尚未产出且未出局"的账户（防挖到一半改模式套利） -->
      <p class="text-sm text-mute mt-sm">
        {{ gameStore.miner.switchable
          ? '可随时切换：切换后按新模式计算后续产出（已产生收益或已出局的账户不可修改）'
          : '当前账户已产生收益或已出局，产出模式不可修改' }}
      </p>
      <p v-if="!walletStore.sessionActive" class="text-sm text-mute mt-sm">
        切换需要钱包签名登录（防他人代改）
      </p>
    </div>

    <!-- 算力说明 -->
    <div class="card">
      <div class="card__title">算力说明</div>
      <div class="rules-body">
        <p>· 倒2 / 倒3 被扣的 50% 币，按爆仓时价格折算为算力</p>
        <p>· 算力 = 被扣数量 × 爆仓价（U 计价）</p>
        <p>· 产出为金本位：币价上涨时更快拿回三倍收益</p>
      </div>
    </div>
  </ConnectGuard>
</template>

<style scoped>
.asset-overview__value {
  font-size: 34px;
  font-weight: 800;
  color: var(--color-primary-strong);
}

.round-stats {
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

.progress { margin-top: var(--space-md); }
.progress__bar {
  height: 12px;
  border-radius: var(--radius-full);
  background: var(--color-bg-surface-2);
  overflow: hidden;
  position: relative;
}
.progress__fill {
  height: 100%;
  border-radius: var(--radius-full);
  background: linear-gradient(90deg, var(--color-primary), var(--color-primary-strong));
  transition: width 0.6s var(--ease-standard);
  position: relative;
}
.progress__meta {
  display: flex;
  justify-content: space-between;
  margin-top: 6px;
  font-size: 12px;
}

.miner-mode {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: var(--space-md);
  margin-bottom: var(--space-sm);
  cursor: pointer;
  transition: border-color var(--duration-fast);
}
.miner-mode.is-active {
  border-color: var(--color-border-strong);
  background: var(--color-primary-dim);
}
/* 不可切换（已产出/已出局或请求中）：视觉上明确变灰，避免"点了没反应"的误解 */
.miner-mode.is-disabled {
  opacity: 0.55;
  cursor: not-allowed;
}
.miner-mode__title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-weight: 600;
}

.rules-body { color: var(--color-text-sub); font-size: 13px; line-height: 1.8; }
.rules-body p { margin-bottom: 4px; }
</style>
