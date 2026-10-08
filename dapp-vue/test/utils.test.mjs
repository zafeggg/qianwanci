/**
 * 千万次 DApp - 展示层工具单测（node:test，无新依赖）
 *
 * 这些函数被所有页面用来格式化金额/地址/时间：显示错一位或出现 NaN/Infinity，
 * 用户会直接看到错误的资产数字。它们纯函数、无副作用，适合单测。
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { formatNumber, shortAddress, formatCompact, formatPercent, toDecimal, toWeiStr } from '../src/utils/format.js';
import { splitDuration, pad2, formatDateTime } from '../src/utils/time.js';

/* ============================== 金额格式化 ============================== */

test('formatNumber：千分位、精度、异常输入兜底', () => {
  assert.equal(formatNumber(1234.5678), '1,234.57', '默认两位小数并加千分位');
  assert.equal(formatNumber(1234.5678, 4), '1,234.5678');
  assert.equal(formatNumber(-1234.5), '-1,234.5', '负号与千分位同时正确');
  assert.equal(formatNumber('1234.5'), '1,234.5', '数字字符串应可格式化');
  // 非法数值统一显示 "--"（本文件既有约定），绝不能显示成 NaN/Infinity
  for (const bad of [NaN, Infinity, -Infinity, undefined, 'abc']) {
    assert.equal(formatNumber(bad), '--', `${String(bad)} 应显示 --`);
  }
  // null / 空串按"没有值 ⇒ 0"处理（Number(null)=0、Number('')=0），这是既有契约，显式钉住
  assert.equal(formatNumber(null), '0');
  assert.equal(formatNumber(''), '0');
});

test('shortAddress：缩略地址与异常输入', () => {
  assert.equal(shortAddress('0x70997970C51812dc3A010C7d01b50e0d17dc79C8'), '0x7099...79C8');
  assert.equal(shortAddress('0x123456', 2, 2), '0x...56');
  assert.equal(shortAddress(''), '', '空地址不应崩');
  assert.equal(shortAddress(null), '');
});

test('formatCompact：大数缩写', () => {
  assert.equal(formatCompact(999), '999');
  assert.equal(formatCompact(1500), '1.5K');
  assert.equal(formatCompact(2500000), '2.50M');
  assert.equal(formatCompact(NaN), '--');
});

test('formatPercent：比例转百分比 + 非法输入兜底', () => {
  assert.equal(formatPercent(0.13), '13.00%');
  assert.equal(formatPercent(0.015, 1), '1.5%');
  // 回归：曾渲染成 "NaN%"/"Infinity%"
  assert.equal(formatPercent(NaN), '--');
  assert.equal(formatPercent(Infinity), '--');
  assert.equal(formatPercent(undefined), '--');
});

/* ============================== 链上金额换算 ============================== */

test('toWeiStr / toDecimal：按精度换算（ERC20 8 位）', () => {
  // TM/FIBO 精度 8：1 枚 = 1e8 最小单位
  assert.equal(toWeiStr('1', 8), '100000000');
  assert.equal(toWeiStr('0.5', 8), '50000000');
  assert.equal(toWeiStr('', 8), '0', '空输入应为 0 而不是 NaN');
  assert.equal(toWeiStr(-1, 8), '0', '负数应兜底为 0（链上金额不允许为负）');
  // toDecimal 返回 number（见其 JSDoc）
  assert.equal(toDecimal(100000000, 8), 1);
  assert.equal(toDecimal('50000000', 8), 0.5);
  assert.equal(toDecimal('abc', 8), 0, '非法输入兜底为 0');
  // 18 位（EVM 原生币）也要正确
  assert.equal(toWeiStr('1', 18), '1000000000000000000');
});

/* ============================== 时间格式化 ============================== */

test('pad2 / splitDuration / formatDateTime', () => {
  assert.equal(pad2(7), '07');
  assert.equal(pad2(12), '12');

  const d = splitDuration(3661 * 1000); // 1 小时 1 分 1 秒
  assert.equal(d.hours, 1);
  assert.equal(d.minutes, 1);
  assert.equal(d.seconds, 1);

  // 负数（已结束）不得产出 NaN
  const neg = splitDuration(-1000);
  assert.ok(Number.isFinite(neg.hours) && Number.isFinite(neg.minutes) && Number.isFinite(neg.seconds));

  const text = formatDateTime(new Date('2026-09-30T05:06:07'));
  assert.match(text, /2026/);
  assert.match(text, /09/);
  assert.match(text, /30/);
  // 非法输入要有兜底（不能显示 Invalid Date）
  assert.ok(!/Invalid/i.test(formatDateTime('not-a-date')));
  assert.ok(!/Invalid/i.test(formatDateTime(null)));
});
