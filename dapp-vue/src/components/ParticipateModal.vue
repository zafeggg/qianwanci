<script setup>
/**
 * 千万次 DApp - 参与弹窗
 * 输入金额（限额校验）、快捷金额、预期收益展示、提交
 * 使用 Vant 数字键盘友好输入
 */
import { ref, computed, watch } from 'vue';
import { showToast } from 'vant';
import { useGameStore } from '../stores/game.js';
import { useWalletStore } from '../stores/wallet.js';
import { calcStaticProfit } from '../engine/game.js';
// 静态收益率以后端实际生效值为准（启动时经 /api/rules 同步）
import { GAME_RULES } from '../constants/config.js';
import { formatPercent } from '../utils/format.js';

const props = defineProps({
  show: { type: Boolean, default: false },
  /** 当前轮数据 { index, minLimit, maxLimit, asset } */
  round: { type: Object, required: true },
});
const emit = defineEmits(['update:show', 'success']);

const gameStore = useGameStore();
const walletStore = useWalletStore();

const amount = ref('');
const submitting = ref(false);

// 快捷金额（基于上下限取 4 档）
const quickAmounts = computed(() => {
  const min = props.round.minLimit || 10;
  const max = props.round.maxLimit || 100;
  return [
    Math.min(min, max),
    Math.round(min + (max - min) * 0.3),
    Math.round(min + (max - min) * 0.6),
    max,
  ].filter((v, i, arr) => arr.indexOf(v) === i);
});

// 预期收益（13%）
const expectedProfit = computed(() => {
  const val = Number(amount.value) || 0;
  return val > 0 ? calcStaticProfit(val) : 0;
});

// 打开时清空输入
watch(
  () => props.show,
  (val) => {
    if (val) amount.value = '';
  },
);

/** 提交参与 */
async function handleSubmit() {
  const val = Number(amount.value);
  if (!val || val <= 0) {
    showToast('请输入投入数量');
    return;
  }
  if (val < props.round.minLimit) {
    showToast(`低于最低限额 ${props.round.minLimit}`);
    return;
  }
  if (val > props.round.maxLimit) {
    showToast(`超出本轮上限 ${props.round.maxLimit}`);
    return;
  }

  submitting.value = true;
  try {
    await gameStore.participate({
      address: walletStore.address,
      roundId: props.round.index,
      amount: val,
      asset: props.round.asset || 'FIBO',
    });
    showToast(`投入成功：${val} ${props.round.asset || 'FIBO'}`);
    emit('update:show', false);
    emit('success');
  } catch (err) {
    showToast(err?.message || '参与失败');
  } finally {
    submitting.value = false;
  }
}

function close() {
  emit('update:show', false);
}
</script>

<template>
  <van-popup
    :show="props.show"
    position="bottom"
    round
    :style="{ paddingBottom: 'env(safe-area-inset-bottom)' }"
    @update:show="close"
  >
    <div class="participate-modal">
      <div class="participate-modal__header">
        <div class="participate-modal__title">参与第 {{ props.round.index }} 轮</div>
        <button class="participate-modal__close" @click="close">✕</button>
      </div>

      <!-- 输入区 -->
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
          <span>限额 {{ props.round.minLimit }} - {{ props.round.maxLimit }} 枚</span>
          <span class="text-sub">{{ props.round.asset || 'FIBO' }}</span>
        </div>
      </div>

      <!-- 快捷金额 -->
      <div class="modal-participate__quick">
        <button
          v-for="q in quickAmounts"
          :key="q"
          class="modal-participate__quick-btn"
          :class="{ 'is-active': Number(amount) === q }"
          @click="amount = String(q)"
        >
          {{ q }}
        </button>
      </div>

      <!-- 预期收益 -->
      <div class="modal-participate__profit">
        本轮成功预期收益：
        <b class="text-success num">+{{ expectedProfit.toFixed(2) }} 枚</b>
        <span class="text-mute">（{{ formatPercent(GAME_RULES.staticReturnRate, 0) }} 静态收益）</span>
      </div>

      <button class="btn btn--primary btn--block mt-md" :disabled="submitting" @click="handleSubmit">
        {{ submitting ? '提交中...' : '确认投入' }}
      </button>
    </div>
  </van-popup>
</template>

<style scoped>
.participate-modal { padding: 16px; }

.participate-modal__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
}

.participate-modal__title { font-size: 17px; font-weight: 700; }

.participate-modal__close {
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

.modal-participate__quick {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: var(--space-sm);
  margin-top: var(--space-md);
}

.modal-participate__quick-btn {
  padding: 8px 0;
  background: var(--color-bg-surface-2);
  border-radius: var(--radius-sm);
  font-size: 13px;
  color: var(--color-text-sub);
  border: 1px solid var(--color-border);
  cursor: pointer;
}

.modal-participate__quick-btn.is-active {
  border-color: var(--color-border-strong);
  color: var(--color-primary);
  background: var(--color-primary-dim);
}

.modal-participate__profit {
  margin-top: var(--space-md);
  padding: 12px;
  background: var(--color-primary-dim);
  border-radius: var(--radius-sm);
  font-size: 13px;
}
</style>
