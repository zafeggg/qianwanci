<script setup>
/**
 * 千万次 DApp - 玩法规则页
 * 完整玩法展示，内容由配置驱动，与业务引擎保持一致
 */
import { computed, onMounted } from 'vue';
import { GAME_RULES, REFERRAL_RULES, TEAM_RULES, WITHDRAW_RULES, BRAND } from '../constants/config.js';
import { calcTarget, calcMaxLimit } from '../engine/game.js';
import { MINER_RULES } from '../constants/config.js';
import { useGameStore } from '../stores/game.js';
import { formatNumber, formatPercent } from '../utils/format.js';

const feeToken = BRAND.feeToken;
const gameStore = useGameStore();

/**
 * 当前轮的真实参数（下限/上限以**后端**为准）
 *
 * ⚠ 修复（2026-09-30）：本页原先只用前端常量渲染示例表（下限恒显示 10、上限按 +20 递增），
 * 而真实轮次的 min/max 由后端下发（例如私链实测 min=1、max=110000），两套数字互相矛盾，
 * 用户会以为页面写的就是当前规则。现在把"示例值"与"当前真实值"分开呈现。
 */
const liveRound = computed(() => gameStore.round);

// 由引擎生成前 4 轮示例表（**仅示例**：口径见 constants/config.js 与 engine/game.js）
const roundRows = [1, 2, 3, 4].map((i) => ({
  round: i,
  target: calcTarget(i),
  max: calcMaxLimit(i),
  min: GAME_RULES.minLimit,
}));

// 团队等级表：比例与门槛都取后端实际生效值（启动时经 /api/rules 同步到 TEAM_RULES）
const pct = (rate) => `${(Number(rate || 0) * 100).toFixed(rate * 100 % 1 === 0 ? 0 : 1)}%`;
const teamRows = computed(() => {
  const lv = (name) => (TEAM_RULES.levels || []).find((x) => x.level === name) || {};
  const f1 = lv('F1');
  const f2 = lv('F2');
  const f3 = lv('F3');
  return [
    {
      level: 'F1',
      rate: `伞下投资 ${pct(f1.rate)}`,
      cond: `直推 ${f1.needDirect ?? 10} 地址 + 伞下 ${f1.needUnderTreeActive ?? 30} 人同投一期 + 伞下累计 ${f1.needUnderTreeActive ?? 30} 人各成功至少 1 次`,
    },
    {
      level: 'F2',
      rate: `伞下投资 ${pct(f2.rate)}`,
      cond: `伞下有 ${f2.needSubCount ?? 3} 个 F1`,
    },
    {
      level: 'F3',
      rate: `伞下投资 ${pct(f3.rate)}`,
      cond: `伞下有 ${f3.needSubCount ?? 3} 个 F2`,
    },
  ];
});

// 拉取当前轮真实参数（进本页即刷新，失败不阻塞页面展示）
onMounted(() => {
  gameStore.refreshRound().catch((err) => console.warn('[rules] 当前轮参数加载失败', err));
});
</script>

<template>
  <div>
    <!-- 众筹玩法 -->
    <div class="rules-section">
      <div class="rules-section__title">🎯 众筹玩法</div>
      <div class="card">
        <div class="rules-body">
          <p>· 每轮总额度在上轮基础上 <b class="text-gold">递增 30%</b>，成功后自动开启下一轮</p>
          <p>· 每轮设有限时（基础 1 小时，随市场活跃度调整）</p>
          <p>· 参与下限固定，上限逐轮递增；<b>每轮可多次投入</b></p>
          <p>· 轮次无上限，最终必有一轮失败（爆仓），触发倒1/倒2/倒3 结算</p>
        </div>
      </div>
    </div>

    <!-- 当前轮真实参数（以后端为准） -->
    <div class="rules-section">
      <div class="rules-section__title">📍 当前轮真实参数（以后端为准）</div>
      <div class="card">
        <div v-if="liveRound && liveRound.index" class="rules-body">
          <p>· 第 <b class="text-gold">{{ liveRound.index }}</b> 轮 ｜ 总额度 <b class="num">{{ formatNumber(liveRound.target) }}</b> 枚</p>
          <p>· 参与额度：<b class="num">{{ formatNumber(liveRound.minLimit) }} - {{ formatNumber(liveRound.maxLimit) }}</b> 枚</p>
          <p>· 已筹集 <b class="num">{{ formatNumber(liveRound.raised) }}</b> 枚 ｜ 结束时间：{{ liveRound.endAt ? new Date(liveRound.endAt).toLocaleString() : '—' }}</p>
        </div>
        <div v-else class="rules-body text-mute">暂未取到当前轮数据（下方示例表仅用于说明规则，不是真实数值）</div>
      </div>
    </div>

    <!-- 轮次示例 -->
    <div class="rules-section">
      <div class="rules-section__title">📊 轮次示例（前 4 轮）</div>
      <div class="card">
        <p class="text-sm text-mute mb-sm">
          ⚠ 下表是<b>规则示例</b>（按 1000 起始额度与前端示例参数推导），
          <b>不是当前轮的真实数值</b>——真实下限/上限请以上方「当前轮真实参数」为准。
        </p>
        <table class="rules-table">
          <thead>
            <tr><th>轮次</th><th>总额度</th><th>参与额度</th><th>时间</th></tr>
          </thead>
          <tbody>
            <tr v-for="r in roundRows" :key="r.round">
              <td>第 {{ r.round }} 轮</td>
              <td class="num">{{ r.target.toLocaleString() }} 枚</td>
              <td>{{ r.min }} - {{ r.max }} 枚</td>
              <td>1h</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 静态收益与结算 -->
    <div class="rules-section">
      <div class="rules-section__title">💰 静态收益 & 结算</div>
      <div class="card">
        <div class="rules-body">
          <p>· 参与的轮次成功 → 获得 <b class="text-success">{{ formatPercent(GAME_RULES.staticReturnRate, 0) }} 静态收益</b>（本息秒到）</p>
          <p>· <b class="text-gold">三进一出</b>：参与第 N 轮，第 N+{{ GAME_RULES.settleOffset }} 轮结束时必结算</p>
          <p>· 倒1（失败轮）：<b class="text-success">全额退本</b>，无损失</p>
          <p>· 倒2 / 倒3：扣除 {{ formatPercent(GAME_RULES.penaltyRateForLastMinus, 0) }}，按爆仓价折算为<b class="text-info">算力</b>（挖矿产出）</p>
        </div>
      </div>
    </div>

    <!-- 动态收益 -->
    <div class="rules-section">
      <div class="rules-section__title">🤝 动态收益</div>
      <div class="card">
        <table class="rules-table">
          <thead><tr><th>直推地址</th><th>解锁收益</th></tr></thead>
          <tbody>
            <tr v-for="t in REFERRAL_RULES.tiers" :key="t.needDirect">
              <td>{{ t.needDirect }} 个</td>
              <td>{{ t.depths.map((d) => `第${d.depth}代 ${(d.rate * 100).toFixed(1)}%`).join(' + ') }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 团队奖励 -->
    <div class="rules-section">
      <div class="rules-section__title">🏆 团队奖励</div>
      <div class="card">
        <table class="rules-table">
          <thead><tr><th>等级</th><th>奖励</th><th>晋升条件</th></tr></thead>
          <tbody>
            <tr v-for="t in teamRows" :key="t.level">
              <td><b class="text-gold">{{ t.level }}</b></td>
              <td>{{ t.rate }}</td>
              <td>{{ t.cond }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 算力 / 挖矿 -->
    <div class="rules-section">
      <div class="rules-section__title">⛏️ 算力 / 挖矿</div>
      <div class="card">
        <div class="rules-body">
          <p v-for="m in MINER_RULES.modes" :key="m.id">
            · <b>{{ m.name }}</b>：{{ m.desc }}
          </p>
          <p class="mt-sm text-mute">· 算力 = 倒2/倒3 被扣数量 × 爆仓价（U 计价）</p>
          <p class="text-mute">· 金本位三倍出局：币价上涨时更快回本</p>
        </div>
      </div>
    </div>

    <!-- 提币手续费 -->
    <div class="rules-section">
      <div class="rules-section__title">💸 提币手续费</div>
      <div class="card">
        <div class="rules-body">
          <p>· 提币收取 <b class="text-danger">{{ formatPercent(WITHDRAW_RULES.feeRate, 0) }} 手续费</b>，使用 {{ feeToken.symbol }} 支付</p>
          <p v-if="WITHDRAW_RULES.minAmount > 0">· 最低起提 <b>{{ formatNumber(WITHDRAW_RULES.minAmount, 0) }}</b> 个（低于该数量不予受理）</p>
          <p>· 例：提取价值 100U 的币 → 需价值 {{ formatNumber(100 * WITHDRAW_RULES.feeRate, 0) }}U 的 {{ feeToken.symbol }}</p>
          <p v-if="feeToken.burnTarget">· {{ feeToken.symbol }} 手续费全部销毁，直至仅剩 {{ feeToken.burnTarget }} 枚</p>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.rules-section { margin-bottom: var(--space-md); }

.rules-section__title {
  font-size: 15px;
  font-weight: 700;
  color: var(--color-primary);
  margin-bottom: var(--space-sm);
  display: flex;
  align-items: center;
  gap: 6px;
}

.rules-body {
  color: var(--color-text-sub);
  font-size: 13px;
  line-height: 1.8;
}
.rules-body p { margin-bottom: 4px; }

.rules-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.rules-table th,
.rules-table td {
  padding: 8px 6px;
  border-bottom: 1px solid var(--color-border);
  text-align: left;
}
.rules-table th {
  color: var(--color-text-mute);
  font-weight: 500;
  font-size: 12px;
}
</style>
