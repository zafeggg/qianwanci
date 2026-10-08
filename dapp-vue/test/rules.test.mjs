/**
 * 千万次 DApp - 后端规则数值同步单测（node:test，无新依赖）
 *
 * 保护点：`/api/rules` 下发的是**后端实际生效**的结算参数，前端必须真的用它覆盖本地默认值，
 * 否则就回到"页面写 13%、后端可能已经改成别的值"的分叉状态（这正是本轮要消除的缺陷）。
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { applyBackendRules, rulesState } from '../src/constants/rules.js';
import { GAME_RULES, REFERRAL_RULES, TEAM_RULES, MINER_RULES, WITHDRAW_RULES, BRAND } from '../src/constants/config.js';

test('applyBackendRules：比例类字段被后端值覆盖', () => {
  const ok = applyBackendRules({
    staticRewardRate: 0.2,
    withdrawMinAmount: 150,
    settleOffset: 5,
    lossRate: 0.4,
    feeRate: 0.05,
    feeSymbol: 'TM',
    burnTarget: 6666,
    dynamicShardRate1: 0.02,
    dynamicShardRate3: 0.03,
    dynamicShardRate5: 0.04,
    teamLevels: [
      { level: 'F1', rate: 0.01, needDirect: 12, needUnderTreeActive: 40 },
      { level: 'F2', rate: 0.02, needDirect: 0, needUnderTreeActive: 0, needSubCount: 4 },
      { level: 'F3', rate: 0.03, needDirect: 0, needUnderTreeActive: 0, needSubCount: 5 },
    ],
    hashPowerBuff: 4,
    hashPowerMaxDays: 200,
    hashPowerPoolDailyRatio: 0.5,
  });

  assert.equal(ok, true);
  assert.equal(GAME_RULES.staticReturnRate, 0.2, '静态收益率应取后端值');
  assert.equal(GAME_RULES.settleOffset, 5, '结算偏移应取后端值（三进一出窗口）');
  assert.equal(GAME_RULES.penaltyRateForLastMinus, 0.4, '倒2倒3 扣除比例应取后端值');
  assert.equal(GAME_RULES.burnTarget, 6666);
  assert.equal(WITHDRAW_RULES.feeRate, 0.05, '提币手续费比例应取后端值');
  assert.equal(WITHDRAW_RULES.minAmount, 150, '最低起提额应取后端值（owner 2026-09-30 定为 100）');
  assert.equal(BRAND.feeToken.burnTarget, 6666, '手续费币销毁目标也要跟随（规则页/提币页读的是它）');

  // 动态收益各代比例
  for (const tier of REFERRAL_RULES.tiers) {
    for (const d of tier.depths) {
      assert.equal(d.rate, { 1: 0.02, 3: 0.03, 5: 0.04 }[d.depth], `第 ${d.depth} 代比例应取后端值`);
    }
  }
  // 团队等级：F1 取直推/同期门槛，F2/F3 取伞下个数（不能被 F1 的直推门槛覆盖）
  const f1 = TEAM_RULES.levels.find((x) => x.level === 'F1');
  const f2 = TEAM_RULES.levels.find((x) => x.level === 'F2');
  const f3 = TEAM_RULES.levels.find((x) => x.level === 'F3');
  assert.equal(f1.rate, 0.01);
  assert.equal(f1.needDirect, 12);
  assert.equal(f1.needUnderTreeActive, 40);
  assert.equal(f2.rate, 0.02);
  assert.equal(f2.needSubCount, 4);
  assert.equal(f2.needDirect, 0, 'F2 的直推门槛必须保持 0（否则等级判定口径被改坏）');
  assert.equal(f3.needSubCount, 5);

  // 算力：payoutMultiple/maxDays 是**模式对象**上的字段
  for (const mode of MINER_RULES.modes) {
    assert.equal(mode.payoutMultiple, 4);
    if (mode.maxDays) assert.equal(mode.maxDays, 200);
  }
  assert.equal(MINER_RULES.poolDailyRatio, 0.5);
  assert.equal(rulesState.loaded, true);
});

test('applyBackendRules：0/缺失字段不覆盖本地默认值（避免"后端没给就被清零"）', () => {
  const beforeStatic = GAME_RULES.staticReturnRate;
  const beforeFee = WITHDRAW_RULES.feeRate;
  applyBackendRules({ staticRewardRate: 0, feeRate: undefined, lossRate: null });
  assert.equal(GAME_RULES.staticReturnRate, beforeStatic, '0 不应覆盖（0 在本项目语义是"未设置"）');
  assert.equal(WITHDRAW_RULES.feeRate, beforeFee, 'undefined 不应覆盖');
});

test('applyBackendRules：非法入参返回 false 且不破坏规则对象', () => {
  const snapshot = JSON.stringify(GAME_RULES);
  assert.equal(applyBackendRules(null), false);
  assert.equal(applyBackendRules(undefined), false);
  assert.equal(applyBackendRules('nope'), false);
  assert.equal(JSON.stringify(GAME_RULES), snapshot, '非法入参不得改动规则对象');
});
