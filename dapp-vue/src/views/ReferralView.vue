<script setup>
/**
 * 千万次 DApp - 动态收益 / 团队页
 * 直推解锁层级 / 团队等级（F1/F2/F3）/ 伞下结构
 */
import { onMounted, computed } from 'vue';
import { useGameStore } from '../stores/game.js';
import { useWalletStore } from '../stores/wallet.js';
import ConnectGuard from '../components/ConnectGuard.vue';
import { formatNumber, shortAddress } from '../utils/format.js';
// 动态/团队比例取后端实际生效值（启动时经 /api/rules 同步，见 constants/rules.js）
import { REFERRAL_RULES, TEAM_RULES } from '../constants/config.js';

const gameStore = useGameStore();
const walletStore = useWalletStore();

/** 比例 → 百分比文案（0.015 → 1.5%，0.1 → 10%） */
const pct = (rate) => `${(Number(rate || 0) * 100).toFixed(Number(rate || 0) * 100 % 1 === 0 ? 0 : 1)}%`;

// 直推解锁档位：档位门槛仍按文档口径（2/5/10 个直推），比例来自后端；
// 文案由 tiers[].depths 动态拼装，避免"后端改了比例、页面还写 1.5%/2%/2.5%"。
const depthName = { 1: '第一代', 3: '第三代', 5: '第五代' };
const tierConfigs = computed(() =>
  (REFERRAL_RULES.tiers || []).map((tier) => ({
    need: tier.needDirect,
    desc: (tier.depths || [])
      .map((d) => `${depthName[d.depth] || `第${d.depth}代`} ${pct(d.rate)}`)
      .join(' + '),
  })),
);

// 团队等级配置：比例与门槛取后端实际生效值
const teamLevels = computed(() => {
  const current = gameStore.user.teamLevel;
  const lv = (name) => (TEAM_RULES.levels || []).find((x) => x.level === name) || {};
  const f1 = lv('F1');
  const f2 = lv('F2');
  const f3 = lv('F3');
  return [
    {
      id: 'F1',
      rate: `伞下总投资 ${pct(f1.rate)}`,
      cond: `直推 ${f1.needDirect ?? 10} 地址 · 伞下 ${f1.needUnderTreeActive ?? 30} 人同投一期 · 伞下累计 ${f1.needUnderTreeActive ?? 30} 人各成功至少 1 次`,
      locked: !current,
    },
    {
      id: 'F2',
      rate: `伞下总投资 ${pct(f2.rate)}`,
      cond: `伞下有 ${f2.needSubCount ?? 3} 个 F1 成员`,
      locked: current !== 'F2' && current !== 'F3',
    },
    {
      id: 'F3',
      rate: `伞下总投资 ${pct(f3.rate)}`,
      cond: `伞下有 ${f3.needSubCount ?? 3} 个 F2 成员`,
      locked: current !== 'F3',
    },
  ];
});

onMounted(() => {
  if (walletStore.connected) {
    gameStore.refreshUser(walletStore.address).catch((err) => console.error('[referral]', err));
  }
});
</script>

<template>
  <ConnectGuard>
    <!-- 动态收益总览 -->
    <div class="card card--highlight">
      <div class="card__title">
        动态收益
        <span class="tag tag--gold">直推解锁</span>
      </div>
      <div class="round-stats">
        <div class="round-stats__cell">
          <div class="round-stats__label">直推地址</div>
          <div class="round-stats__value num">{{ gameStore.user.directCount || 0 }}</div>
        </div>
        <div class="round-stats__cell">
          <div class="round-stats__label">动态收益</div>
          <div class="round-stats__value text-success num">$ {{ formatNumber(gameStore.user.referralIncome || 0) }}</div>
        </div>
        <div class="round-stats__cell">
          <div class="round-stats__label">团队收益</div>
          <div class="round-stats__value text-success num">$ {{ formatNumber(gameStore.user.teamIncome || 0) }}</div>
        </div>
      </div>

      <!-- 直推层级进度 -->
      <div class="mt-md">
        <div v-for="t in tierConfigs" :key="t.need" class="row">
          <span class="row__label">
            <span class="tag" :class="(gameStore.user.directCount || 0) >= t.need ? 'tag--success' : 'tag--warning'">
              {{ (gameStore.user.directCount || 0) >= t.need ? '已解锁' : `${gameStore.user.directCount || 0}/${t.need} 地址` }}
            </span>
            <span class="text-sm text-mute">{{ t.desc }}</span>
          </span>
          <span class="row__value" :class="(gameStore.user.directCount || 0) >= t.need ? 'text-success' : 'text-mute'">
            {{ (gameStore.user.directCount || 0) >= t.need ? '✓' : '🔒' }}
          </span>
        </div>
      </div>
    </div>

    <!-- 团队等级 -->
    <div class="card">
      <div class="card__title">团队等级</div>
      <div
        v-for="lv in teamLevels"
        :key="lv.id"
        class="card level-card"
        :class="lv.locked && gameStore.user.teamLevel !== lv.id ? 'level-card--locked' : ''"
      >
        <div class="level-card__badge">{{ lv.id }}</div>
        <div style="flex: 1">
          <div class="flex-between">
            <b>{{ lv.id }} 团队长</b>
            <span v-if="gameStore.user.teamLevel === lv.id" class="tag tag--success">当前等级</span>
          </div>
          <div class="text-sm text-sub mt-sm">{{ lv.rate }}</div>
          <div class="text-sm text-mute mt-sm">{{ lv.cond }}</div>
        </div>
      </div>
    </div>

    <!-- 伞下结构 -->
    <div class="card">
      <div class="card__title">
        伞下结构
        <span class="tag tag--info">总投入 $ {{ formatNumber(gameStore.user.underTreeTotalInvest || 0) }}</span>
      </div>
      <div v-if="!gameStore.user.underTree?.length" class="empty">
        <p>暂无伞下成员，快去推广吧</p>
      </div>
      <div v-else>
        <div v-for="(node, i) in gameStore.user.underTree" :key="i" class="tree-node">
          <div class="tree-node__avatar">{{ shortAddress(node.address, 2, 2) }}</div>
          <div style="flex: 1">
            <div class="flex-between">
              <b>{{ shortAddress(node.address) }}</b>
              <span class="tree-node__depth">第 {{ node.depth }} 代</span>
            </div>
            <div class="text-sm text-mute mt-sm">投入 {{ formatNumber(node.invest) }} · 成功 {{ node.success }} 次</div>
          </div>
        </div>
      </div>
    </div>
  </ConnectGuard>
</template>

<style scoped>
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

.level-card { display: flex; align-items: center; gap: var(--space-md); padding: 14px; }
.level-card__badge {
  width: 46px;
  height: 46px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-weight: 800;
  font-size: 18px;
  background: var(--color-primary-dim);
  color: var(--color-primary);
  flex-shrink: 0;
}
.level-card--locked .level-card__badge {
  background: var(--color-bg-surface-2);
  color: var(--color-text-mute);
}

.tree-node {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px;
  background: var(--color-bg-surface-2);
  border-radius: var(--radius-sm);
  margin-bottom: 6px;
}
.tree-node__avatar {
  width: 34px;
  height: 34px;
  border-radius: 50%;
  background: var(--color-bg-surface);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  color: var(--color-text-mute);
  flex-shrink: 0;
}
.tree-node__depth {
  font-size: 11px;
  padding: 1px 8px;
  border-radius: var(--radius-full);
  background: var(--color-primary-dim);
  color: var(--color-primary);
}
</style>
