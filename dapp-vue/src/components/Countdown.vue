<script setup>
/**
 * 千万次 DApp - 倒计时组件
 * 接收结束时间戳，秒级刷新显示 天/时/分/秒
 */
import { ref, onMounted, onBeforeUnmount, computed } from 'vue';
import { splitDuration, pad2 } from '../utils/time.js';

const props = defineProps({
  /** 结束时间戳（ms） */
  endAt: { type: Number, required: true },
});

const now = ref(Date.now());
let timer = null;

onMounted(() => {
  timer = setInterval(() => {
    now.value = Date.now();
  }, 1000);
});

onBeforeUnmount(() => {
  if (timer) clearInterval(timer);
});

/** 拆分为 天/时/分/秒 */
const parts = computed(() => splitDuration(props.endAt - now.value));

/** 是否已结束 */
const expired = computed(() => now.value >= props.endAt);
</script>

<template>
  <div v-if="!expired" class="countdown">
    <div class="countdown__cell">
      <div class="countdown__num num">{{ pad2(parts.days) }}</div>
      <div class="countdown__unit">天</div>
    </div>
    <div class="countdown__cell">
      <div class="countdown__num num">{{ pad2(parts.hours) }}</div>
      <div class="countdown__unit">时</div>
    </div>
    <div class="countdown__cell">
      <div class="countdown__num num">{{ pad2(parts.minutes) }}</div>
      <div class="countdown__unit">分</div>
    </div>
    <div class="countdown__cell">
      <div class="countdown__num num">{{ pad2(parts.seconds) }}</div>
      <div class="countdown__unit">秒</div>
    </div>
  </div>
</template>

<style scoped>
.countdown {
  display: flex;
  justify-content: center;
  gap: var(--space-sm);
  margin-top: var(--space-md);
}

.countdown__cell {
  min-width: 52px;
  padding: 8px 10px;
  background: var(--color-bg-surface-2);
  border-radius: var(--radius-sm);
}

.countdown__num {
  font-size: 20px;
  font-weight: 700;
  color: var(--color-text-main);
}

.countdown__unit {
  font-size: 11px;
  color: var(--color-text-mute);
}
</style>
