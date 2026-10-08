/**
 * 千万次 DApp - 规则引擎单测（node:test，**不引入任何新依赖**）
 *
 * 为什么用 node:test 而不是 vitest/jest：本机/CI 都不保证能装依赖（离线环境），
 * 而 Node 18+ 自带 `node --test`。这些模块都是纯逻辑（不碰 DOM、不碰 import.meta.env
 * 的必需字段），因此可以直接被 Node 导入执行。
 *
 * 覆盖重点：**需求口径**（每轮 +30%、上限逐轮递增、静态 13%、三进一出 N+3、
 * 倒1全退/倒2倒3扣半并折算算力、动态 2/5/10 门槛、团队 0.5/1/1.5%、提币 3% TM 手续费）——
 * 这些数字一旦被改错，应该在这里先红。
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  roundTarget,
  calcTarget,
  calcMaxLimit,
  calcStaticProfit,
  calcSettleRound,
  settlePosition,
  calcReferralIncome,
  getTeamLevel,
  calcTeamIncome,
  calcWithdrawFee,
  calcMinerOutput,
  deriveRoundStatus,
} from '../src/engine/game.js';

/* ============================== 轮次：总额度与限额 ============================== */

test('每轮总额度递增 30%（需求#1）', () => {
  // 需求/N次方示例：1000 → 1300 → 1690 → 2197
  assert.equal(calcTarget(1), 1000);
  assert.equal(calcTarget(2), 1300);
  assert.equal(calcTarget(3), 1690);
  assert.equal(calcTarget(4), 2197);
  // 纯等比公式（可指定起始值与增长率）
  assert.equal(roundTarget(1000, 0.3, 5), Math.round(1000 * 1.3 ** 4));
  assert.equal(roundTarget(5000, 0.3, 1), 5000);
});

test('参与下限不变、上限逐轮增加（需求#2/#3）', () => {
  // 前 4 轮取显式表（忠实文档 100/110/130/150）
  assert.deepEqual([1, 2, 3, 4].map(calcMaxLimit), [100, 110, 130, 150]);
  // 超出显式表后每轮 +20，且必须**严格递增**（需求：最高限额每轮增加一点点）
  assert.equal(calcMaxLimit(5), 170);
  assert.equal(calcMaxLimit(6), 190);
  for (let r = 2; r <= 12; r++) {
    assert.ok(calcMaxLimit(r) > calcMaxLimit(r - 1), `第 ${r} 轮上限必须大于上一轮`);
  }
});

/* ============================== 静态收益与三进一出 ============================== */

test('静态收益 13%（需求#3）', () => {
  assert.equal(calcStaticProfit(100), 13);
  assert.equal(calcStaticProfit(0), 0);
});

test('三进一出：投第 N 轮在第 N+3 轮结算（需求#14）', () => {
  assert.equal(calcSettleRound(1), 4);
  assert.equal(calcSettleRound(6), 9);
});

/* ============================== 失败结算：倒1 / 倒2倒3 ============================== */

test('倒1：全额退本、无收益（需求#6）', () => {
  const r = settlePosition({ roundIndex: 5, amount: 100 }, { failRound: 5, crashPriceUsd: 0.1 });
  assert.equal(r.status, 'refund');
  assert.equal(r.refundAmount, 100);
  assert.equal(r.lossAmount, 0);
  assert.equal(r.hashrate, 0);
  assert.equal(r.profit, 0);
});

test('倒2/倒3：扣一半 + 按爆仓价折算算力（需求#6/#7）', () => {
  // 需求#7 原文算例：参与 10 万 → 退回 5 万，币价 0.1U → 算力 50000×0.1 = 5000
  for (const failRound of [7, 8]) {
    const r = settlePosition({ roundIndex: 6, amount: 100000 }, { failRound, crashPriceUsd: 0.1 });
    assert.equal(r.status, 'penalty');
    assert.equal(r.refundAmount, 50000, '被扣一半 → 退还另一半');
    assert.equal(r.lossAmount, 50000);
    assert.equal(r.hashrate, 5000);
    assert.equal(r.profit, 0, '失败轮不产生静态收益');
  }
});

test('倒2/倒3 本息守恒：退还 + 被扣 = 本金', () => {
  for (const amount of [1, 7.5, 100, 12345.67]) {
    const r = settlePosition({ roundIndex: 3, amount }, { failRound: 4, crashPriceUsd: 3.4 });
    assert.ok(Math.abs(r.refundAmount + r.lossAmount - amount) < 1e-9, `本金必须守恒：${amount}`);
  }
});

test('非相邻轮次：正常本息结算（含 13% 收益）', () => {
  const r = settlePosition({ roundIndex: 1, amount: 100 }, { failRound: 9, crashPriceUsd: 0.5 });
  assert.equal(r.status, 'settled');
  assert.equal(r.profit, 13);
  assert.equal(r.refundAmount, 113);
});

/* ============================== 动态收益（2/5/10 门槛） ============================== */

test('动态收益按直推人数解锁 1/3/5 代（需求#4）', () => {
  const tree = [
    { depth: 1, totalInvest: 100 },
    { depth: 3, totalInvest: 100 },
    { depth: 5, totalInvest: 100 },
  ];
  // <2 个直推：不解锁任何层级
  assert.equal(calcReferralIncome(1, tree), 0);
  // 2 个：只有一代 1.5%
  assert.equal(calcReferralIncome(2, tree), 1.5);
  // 5 个：一代 1.5% + 三代 2%
  assert.equal(calcReferralIncome(5, tree), 3.5);
  // 10 个：再加五代 2.5%
  assert.equal(calcReferralIncome(10, tree), 6);
  // 直推够了但伞下没有对应代：只按实际存在的代计算
  assert.equal(calcReferralIncome(10, [{ depth: 1, totalInvest: 200 }]), 3);
});

/* ============================== 团队等级与团队收益 ============================== */

test('F1/F2/F3 判定（需求#7）', () => {
  // F1：直推 10 + 同期伞下 30 人 + 伞下 30 人各成功至少一次
  assert.equal(getTeamLevel({ directCount: 10, underTreeActive: 30, f1SuccessFlag: true }), 'F1');
  assert.equal(getTeamLevel({ directCount: 9, underTreeActive: 30, f1SuccessFlag: true }), null, '直推不足 10 不升 F1');
  assert.equal(getTeamLevel({ directCount: 10, underTreeActive: 29, f1SuccessFlag: true }), null, '伞下不足 30 不升 F1');
  assert.equal(getTeamLevel({ directCount: 10, underTreeActive: 30, f1SuccessFlag: false }), null, '缺"各成功一次"不升 F1');
  // F2：伞下 3 个 F1；F3：伞下 3 个 F2
  assert.equal(getTeamLevel({ directCount: 10, underTreeActive: 30, f1SuccessFlag: true, subF1Count: 3 }), 'F2');
  assert.equal(
    getTeamLevel({ directCount: 10, underTreeActive: 30, f1SuccessFlag: true, subF1Count: 3, subF2Count: 3 }),
    'F3',
  );
  assert.equal(getTeamLevel({}), null);
});

test('团队收益 0.5% / 1% / 1.5%（需求#6）', () => {
  assert.equal(calcTeamIncome('F1', 1000), 5);
  assert.equal(calcTeamIncome('F2', 1000), 10);
  assert.equal(calcTeamIncome('F3', 1000), 15);
  assert.equal(calcTeamIncome(null, 1000), 0, '无等级不产生团队收益');
  assert.equal(calcTeamIncome('F9', 1000), 0, '未知等级不产生收益');
});

/* ============================== 提币手续费 ============================== */

test('提币 3% 手续费、以 TM 支付（需求#9）', () => {
  // 提 100U 的币、TM=3.4U → 手续费价值 3U → 需 3/3.4 ≈ 0.8824 TM
  const fee = calcWithdrawFee(100, 1, 3.4);
  assert.ok(Math.abs(fee.feeValueUsd - 3) < 1e-9);
  assert.ok(Math.abs(fee.feeTokenAmount - 3 / 3.4) < 1e-9);
  // 手续费币无价（0）时不得产生 Infinity/NaN
  const zero = calcWithdrawFee(100, 1, 0);
  assert.equal(zero.feeTokenAmount, 0);
});

/* ============================== 算力产出 ============================== */

test('算力：模式 0 无天数限制，模式 1/2 按 300 天保底（需求#8）', () => {
  const m0 = calcMinerOutput(1000, 0.5, 'default');
  assert.equal(m0.totalPayout, (1000 * 3) / 0.5);
  assert.equal(m0.dailyMin, 0, '模式0 不设每日保底');

  const m1 = calcMinerOutput(1000, 0.5, 'fixed300');
  assert.equal(m1.totalPayout, (1000 * 3) / 0.5);
  assert.equal(m1.dailyMin, m1.totalPayout / 300);

  // ⚠ 回归：后端下发的是**数值** modeId，必须也能匹配（曾经只认字符串 id → 静默回落模式0）
  const byNumber = calcMinerOutput(1000, 0.5, 1);
  assert.equal(byNumber.mode.id, 'fixed300', '数值 modeId=1 必须匹配"300天三倍出局"');
  assert.equal(byNumber.dailyMin, m1.dailyMin);
  const modeTwo = calcMinerOutput(1000, 0.5, 2);
  assert.equal(modeTwo.mode.id, 'tm');
  assert.equal(modeTwo.mode.maxDays, 300);

  // 币价为 0 时不得出现 Infinity/NaN
  const zeroPrice = calcMinerOutput(1000, 0, 'default');
  assert.ok(Number.isFinite(zeroPrice.totalPayout));
});

/* ============================== 轮次状态推导 ============================== */

test('轮次状态推导：进行中 / 成功 / 失败', () => {
  const future = Date.now() + 60_000;
  const past = Date.now() - 60_000;
  assert.equal(deriveRoundStatus({ target: 1000, raised: 100, endAt: future }).phase, 'open');
  assert.equal(deriveRoundStatus({ target: 1000, raised: 1000, endAt: past }).phase, 'success');
  assert.equal(deriveRoundStatus({ target: 1000, raised: 999, endAt: past }).phase, 'failed');
  assert.equal(deriveRoundStatus({ target: 1000, raised: 0, endAt: past, status: 'failed' }).phase, 'failed');
  // 进度封顶 1，且 target=0 时不除零
  assert.equal(deriveRoundStatus({ target: 1000, raised: 99999, endAt: future }).progress, 1);
  assert.equal(deriveRoundStatus({ target: 0, raised: 5, endAt: future }).progress, 0);
});
