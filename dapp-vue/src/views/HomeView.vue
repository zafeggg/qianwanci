<script setup>
/**
 * 千万次 DApp - 首页（轮次看板）
 * 当前轮次 / 倒计时 / 进度条 / 参与弹窗 / 轮次历史
 */
import { onMounted, onBeforeUnmount, ref, computed } from 'vue';
import { showToast } from 'vant';
import { useGameStore } from '../stores/game.js';
import { useWalletStore } from '../stores/wallet.js';
import Countdown from '../components/Countdown.vue';
import ParticipateModal from '../components/ParticipateModal.vue';
import { formatNumber, formatPercent } from '../utils/format.js';
// 规则数值：显示后端实际生效值（启动时经 /api/rules 同步，见 constants/rules.js）
import { GAME_RULES } from '../constants/config.js';

const gameStore = useGameStore();
const walletStore = useWalletStore();

// 参与弹窗开关
const showParticipate = ref(false);
// 轮询定时器
let pollTimer = null;

// 进度百分比（0-100）
const progressPercent = computed(() => Math.round(gameStore.roundProgress * 100));
// 当前轮状态文案
const roundBadgeText = computed(() => {
  const r = gameStore.round;
  const statusMap = { open: '进行中', success: '已成功', failed: '已失败' };
  return `第 ${r.index} 轮 · ${statusMap[r.status] || r.status}`;
});
// 参与按钮文案
const participateBtnText = computed(() => {
  if (!gameStore.roundOpen) return '本轮已结束，等待下一轮';
  return '参与本轮';
});

// 加载初始数据
async function load() {
  try {
    await gameStore.refreshRound(walletStore.connected ? walletStore.address : '');
  } catch (err) {
    console.error('[home] 加载失败', err);
    showToast('加载失败，请检查网络');
  }
}

onMounted(() => {
  load();
  // 30s 轮询刷新当前轮
  pollTimer = setInterval(load, 30_000);
});

onBeforeUnmount(() => {
  if (pollTimer) clearInterval(pollTimer);
});

/** 参与按钮：未连接则提示，已连接则打开弹窗 */
function handleParticipateClick() {
  if (!walletStore.connected) {
    showToast('请先连接钱包');
    return;
  }
  showParticipate.value = true;
}
</script>

<template>
  <div>
    <!-- 加载骨架 -->
    <div v-if="gameStore.loading || !gameStore.round.target" class="card">
      <div class="skeleton" style="height: 120px; border-radius: 16px"></div>
    </div>

    <!-- 轮次主卡 -->
    <div v-else class="card card--highlight round-hero">
      <div class="round-hero__badge">{{ roundBadgeText }}</div>
      <div class="round-hero__target num">{{ formatNumber(gameStore.round.target, 0) }}</div>
      <div class="round-hero__label">本轮总额度（枚）</div>

      <!-- 倒计时 -->
      <Countdown v-if="gameStore.roundOpen && gameStore.round.endAt > Date.now()" :end-at="gameStore.round.endAt" />

      <!-- 进度条 -->
      <div class="progress">
        <div class="progress__bar">
          <div class="progress__fill" :style="{ width: progressPercent + '%' }"></div>
        </div>
        <div class="progress__meta">
          <span class="text-sub">已筹 <b class="num">{{ formatNumber(gameStore.round.raised) }}</b></span>
          <span class="num">{{ formatPercent(gameStore.roundProgress, 0) }}</span>
        </div>
      </div>

      <!-- 轮次数据 -->
      <div class="round-stats">
        <div class="round-stats__cell">
          <div class="round-stats__label">单轮收益</div>
          <div class="round-stats__value text-success num">{{ formatPercent(GAME_RULES.staticReturnRate, 0) }}</div>
        </div>
        <div class="round-stats__cell">
          <div class="round-stats__label">参与下限</div>
          <div class="round-stats__value num">{{ formatNumber(gameStore.round.minLimit, 0) }}</div>
        </div>
        <div class="round-stats__cell">
          <div class="round-stats__label">参与上限</div>
          <div class="round-stats__value num">{{ formatNumber(gameStore.round.maxLimit, 0) }}</div>
        </div>
      </div>
    </div>

    <!-- 参与按钮（吸底） -->
    <div class="participate-btn-wrap">
      <button class="btn btn--primary btn--block" :disabled="!gameStore.roundOpen" @click="handleParticipateClick">
        {{ participateBtnText }}
      </button>
    </div>

    <!-- 轮次历史 -->
    <div class="card">
      <div class="card__title">
        轮次记录
        <span class="tag tag--gold">{{ gameStore.round.asset }}</span>
      </div>
      <div v-if="!gameStore.roundHistory.length" class="empty"><p>暂无轮次记录</p></div>
      <div v-else>
        <div v-for="r in [...gameStore.roundHistory].reverse()" :key="r.id" class="round-list__item">
          <div>
            <div class="round-list__name">第 {{ r.id }} 轮</div>
            <div class="round-list__meta">
              {{ formatNumber(r.raised, 0) }} / {{ formatNumber(r.target, 0) }} 枚
            </div>
          </div>
          <span v-if="r.phase === 'success'" class="tag tag--success">成功</span>
          <!--
            失败轮要区分倒1 / 倒2倒3（后端 failType 只对"已结算的失败轮"下发）：
              倒1   = 全额退本（无损）
              倒2/3 = 扣一半 + 折算算力（有损，但按爆仓价折成算力继续产出）
            修复前这里对所有失败轮都写"爆仓 倒1"，倒2/倒3 的用户会以为自己的本金全额退回了。
          -->
          <span v-else-if="r.phase === 'failed' && r.failType === 'penalty'" class="tag tag--danger">爆仓 倒2/3 · 扣半转算力</span>
          <span v-else-if="r.phase === 'failed' && r.failType === 'refund'" class="tag tag--danger">爆仓 倒1 · 全额退本</span>
          <span v-else-if="r.phase === 'failed'" class="tag tag--danger">爆仓（结算中）</span>
          <span v-else class="tag tag--info">进行中</span>
        </div>
      </div>
    </div>

    <!-- 参与弹窗 -->
    <ParticipateModal
      v-model:show="showParticipate"
      :round="gameStore.round"
      @success="load"
    />
  </div>
</template>

<style scoped>
.round-hero {
  text-align: center;
  padding: var(--space-lg) var(--space-md);
  position: relative;
  overflow: hidden;
}

.round-hero__badge {
  display: inline-block;
  padding: 4px 14px;
  border-radius: var(--radius-full);
  background: var(--color-primary-dim);
  color: var(--color-primary);
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 2px;
  margin-bottom: var(--space-sm);
}

.round-hero__target {
  font-size: 40px;
  font-weight: 800;
  color: var(--color-primary-strong);
  line-height: 1.2;
}

.round-hero__label {
  color: var(--color-text-mute);
  font-size: 12px;
  margin-top: 2px;
}

.progress {
  margin-top: var(--space-md);
}

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

.progress__fill::after {
  content: '';
  position: absolute;
  right: -2px;
  top: 50%;
  transform: translateY(-50%);
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: var(--color-primary-strong);
  box-shadow: 0 0 10px var(--color-primary);
}

.progress__meta {
  display: flex;
  justify-content: space-between;
  margin-top: 6px;
  font-size: 12px;
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

.participate-btn-wrap {
  padding: var(--space-md);
  position: sticky;
  bottom: calc(var(--tabbar-height) + env(safe-area-inset-bottom) + 8px);
  z-index: 10;
}

.round-list__item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 0;
  border-bottom: 1px solid var(--color-border);
}

.round-list__item:last-child { border-bottom: none; }
.round-list__name { font-weight: 600; }
.round-list__meta { font-size: 12px; color: var(--color-text-mute); }

.skeleton {
  border-radius: var(--radius-sm);
  background: linear-gradient(
    90deg,
    var(--color-bg-surface) 25%,
    var(--color-bg-surface-2) 37%,
    var(--color-bg-surface) 63%
  );
  background-size: 400% 100%;
  animation: skeleton-loading 1.4s ease infinite;
}

@keyframes skeleton-loading {
  0% { background-position: 100% 50%; }
  100% { background-position: 0 50%; }
}
</style>
