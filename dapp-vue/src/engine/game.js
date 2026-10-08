/**
 * 千万次 DApp - 核心业务引擎（纯函数，无 DOM / 无副作用）
 *
 * 所有玩法规则集中实现于此，页面与接口层只负责调用与展示。
 * 规则基准：TiMi(1).docx（TiMi = 千万次）；结算闭环借鉴 N次方玩法及规则.docx（2026-09-02 owner 确认）。
 *
 * 核心概念：
 * - 轮次(round)：每轮有总额度 target、已筹集 raised、开始/结束时间
 * - 仓位(position)：用户在某轮投入的一笔资金，携带结算状态
 * - 三进一出（借鉴 N次方）：投 R 轮 → 第 R+3 轮结束时强制结算本息（13% 静态收益，本息秒到）
 * - 倒位清算（TiMi 第 5 条 + N次方 25-32 段）：失败轮 K = 倒1（参与者全退本金）；
 *   K-1/K-2 轮仓位 = 倒2/倒3（扣 50%，折算为固定算力：算力 = 扣币量 x 爆仓价，U 计价，金本位 3 倍，300 天兜底）
 * - 循环（TiMi 第 6 条）：爆仓结算后进入下一次循环，从 firstRoundTarget 重新起算（GAME_RULES.cycleRestart）
 */

import {
  GAME_RULES,
  REFERRAL_RULES,
  TEAM_RULES,
  MINER_RULES,
  WITHDRAW_RULES,
} from '../constants/config.js';

/**
 * 计算指定轮次的总额度（每轮递增 30%）
 * @param {number} roundIndex 轮次序号（从 1 开始）
 * @returns {number} 该轮总额度
 */
export function calcTarget(roundIndex) {
  const { firstRoundTarget, targetGrowthRate } = GAME_RULES;
  // 等比数列：target_n = firstRoundTarget * (1 + rate)^(n-1)
  return roundTarget(firstRoundTarget, targetGrowthRate, roundIndex);
}

/** 纯等比公式（与 calcTarget 分离，便于测试） */
export function roundTarget(first, rate, index) {
  return Math.round(first * Math.pow(1 + rate, index - 1));
}

/**
 * 计算用户单轮参与上限
 * 前 4 轮取显式表（忠实文档 100/110/130/150），其后每轮 +20
 * @param {number} roundIndex 轮次序号（从 1 开始）
 * @returns {number} 上限
 */
export function calcMaxLimit(roundIndex) {
  const { maxLimitTable, maxLimitStepAfterTable } = GAME_RULES;
  if (roundIndex <= maxLimitTable.length) {
    return maxLimitTable[roundIndex - 1];
  }
  // 超出显式表：在表末值基础上按步进累加
  const last = maxLimitTable[maxLimitTable.length - 1];
  const extra = (roundIndex - maxLimitTable.length) * maxLimitStepAfterTable;
  return last + extra;
}

/**
 * 计算单笔投入的静态收益（复利）
 * @param {number} amount 投入数量
 * @returns {number} 成功后可获得的静态收益
 */
export function calcStaticProfit(amount) {
  return amount * GAME_RULES.staticReturnRate;
}

/**
 * 判断用户某轮投入的结算轮次（三进一出）
 * 参与 R 轮，第 R + settleOffset 轮结束时必结算
 * @param {number} joinRound 参与轮次
 * @returns {number} 结算轮次
 */
export function calcSettleRound(joinRound) {
  return joinRound + GAME_RULES.settleOffset;
}

/**
 * 结算一台份仓位（核心状态机）
 * @param {object} position 仓位 { roundIndex, amount, joinTime }
 * @param {object} context 结算上下文
 * @param {number} context.failRound 失败的轮次（-1 表示无失败，正常结算）
 * @param {number} context.crashPriceUsd 爆仓价（U 计价，用于算力折算）
 * @param {number} context.settleRound 当前结算轮次
 * @returns {object} 结算结果
 *   { status, refundAmount, lossAmount, hashrate, profit }
 *   status: 'refund' 退本 | 'penalty' 扣半 | 'settled' 正常本息结算
 */
export function settlePosition(position, context) {
  const { failRound, crashPriceUsd = 0 } = context;
  const joinRound = position.roundIndex;
  const amount = position.amount;

  // 场景A：所在轮次就是失败轮（倒1）→ 全额退本，无收益
  if (failRound === joinRound) {
    return {
      status: 'refund',
      refundAmount: amount,
      lossAmount: 0,
      hashrate: 0,
      profit: 0,
    };
  }

  // 场景B：所在轮次是失败轮前一轮（倒2）或前两轮（倒3）→ 扣 50%，折算算力
  const isLast2 = failRound - 1 === joinRound;
  const isLast3 = failRound - 2 === joinRound;
  if (isLast2 || isLast3) {
    const loss = amount * GAME_RULES.penaltyRateForLastMinus;
    // 算力 = 被扣数量 x 爆仓价（U 计价），见 TiMi 文档第 7 条
    const hashrate = loss * crashPriceUsd;
    return {
      status: 'penalty',
      refundAmount: amount - loss,
      lossAmount: loss,
      hashrate,
      profit: 0,
    };
  }

  // 场景C：正常结算 → 静态收益 13%
  const profit = calcStaticProfit(amount);
  return {
    status: 'settled',
    refundAmount: amount + profit,
    lossAmount: 0,
    hashrate: 0,
    profit,
  };
}

/**
 * 动态收益计算
 * 按直推地址数解锁层级：
 * - 2 地址：第 1 代 1.5%
 * - 5 地址：第 1 代 1.5% + 第 3 代 2%
 * - 10 地址：第 1 代 1.5% + 第 3 代 2% + 第 5 代 2.5%
 * @param {number} directCount 直推地址数
 * @param {Array<{depth:number, totalInvest:number}>} treeInvest
 *   伞下各代总投资额，如 [{depth:1, totalInvest:100}, {depth:3, totalInvest:200}]
 * @returns {number} 该轮动态收益合计
 */
export function calcReferralIncome(directCount, treeInvest) {
  // 找到当前直推数命中的最高档位（按 needDirect 从大到小匹配）
  let activeTier = null;
  for (const tier of [...REFERRAL_RULES.tiers].reverse()) {
    if (directCount >= tier.needDirect) {
      activeTier = tier;
      break;
    }
  }
  if (!activeTier) return 0;

  const depthMap = new Map(treeInvest.map((item) => [item.depth, item.totalInvest]));
  let income = 0;
  for (const rule of activeTier.depths) {
    const invest = depthMap.get(rule.depth) || 0;
    income += invest * rule.rate;
  }
  return income;
}

/**
 * 团队等级判定
 * @param {object} user 用户数据
 * @param {number} user.directCount 直推地址数
 * @param {number} user.underTreeActive 同期伞下参投人数（30 人门槛）
 * @param {number} user.subF1Count 伞下 F1 数量
 * @param {number} user.subF2Count 伞下 F2 数量
 * @returns {string} 'F1' | 'F2' | 'F3' | null
 */
export function getTeamLevel(user) {
  const d = user || {};
  const isF1 =
    d.directCount >= TEAM_RULES.levels[0].needDirect &&
    d.underTreeActive >= TEAM_RULES.levels[0].needUnderTreeActive;
  // F1 判定需额外条件：伞下历史累计 30 个地址各成功至少 1 次（任意期，非限定该期），由数据层提供 f1SuccessFlag
  if (isF1 && d.f1SuccessFlag) {
    // F2：伞下 3 个 F1
    if (d.subF1Count >= TEAM_RULES.levels[1].needSubCount) {
      // F3：伞下 3 个 F2
      if (d.subF2Count >= TEAM_RULES.levels[2].needSubCount) {
        return 'F3';
      }
      return 'F2';
    }
    return 'F1';
  }
  return null;
}

/**
 * 团队奖励计算
 * F1 拿伞下总投资额 0.5%，F2 拿 1%，F3 拿 1.5%
 * @param {string|null} level 团队等级
 * @param {number} underTreeTotalInvest 伞下总投资额
 * @returns {number} 团队奖励
 */
export function calcTeamIncome(level, underTreeTotalInvest) {
  if (!level) return 0;
  const config = TEAM_RULES.levels.find((item) => item.level === level);
  if (!config) return 0;
  return underTreeTotalInvest * config.rate;
}

/**
 * 提币手续费计算
 * 3% 手续费以手续费代币（TM）支付：
 * 所需手续费代币 = 提币价值(U) x 3% / 手续费代币价格(U)
 * @param {number} withdrawAmount 提币数量
 * @param {number} tokenPriceUsd 被提币种的美元价格
 * @param {number} feeTokenPriceUsd 手续费代币美元价格
 * @returns {object} { feeValueUsd, feeTokenAmount }
 */
export function calcWithdrawFee(withdrawAmount, tokenPriceUsd, feeTokenPriceUsd) {
  const valueUsd = withdrawAmount * tokenPriceUsd;
  const feeValueUsd = valueUsd * WITHDRAW_RULES.feeRate;
  const feeTokenAmount =
    feeTokenPriceUsd > 0 ? feeValueUsd / feeTokenPriceUsd : 0;
  return { feeValueUsd, feeTokenAmount };
}

/**
 * 算力产出计算
 * 模式1 默认：累计产出达 算力 x 3 出局，不设天数
 * 模式2 300天：每日最低产出 = (算力 x 3) / 300 / 4小时均价
 * 模式3 TM：300 天三倍，产出币量与模式2同，另需用 TM 兑换
 * @param {number} hashrate 算力（U 计价）
 * @param {number} priceUsd 4小时均价（U）
 * @param {string} modeId 模式 id
 * @returns {object} { totalPayout, dailyMin, daysToBreakEven }
 */
export function calcMinerOutput(hashrate, priceUsd, modeId = 'default') {
  // 兼容两种入参口径：字符串 id（'default'/'fixed300'/'tm'）与数值 modeId（0/1/2）。
  // ⚠ 2026-09-30 修复：DApp 从后端拿到的是**数值** modeId（/api/miner 的 modeId 字段），
  // 而这里原先只按字符串 id 匹配 → 传数值时永远匹配不到、静默回落到 modes[0]（展示成"默认三倍出局"）。
  // 现在两种都能匹配，并有单测钉死。
  const mode =
    MINER_RULES.modes.find((item) => item.id === modeId || item.modeId === modeId) ||
    MINER_RULES.modes[0];
  // 总产出币量 = 算力 x 倍数 / 币价
  const totalPayout = (hashrate * mode.payoutMultiple) / (priceUsd > 0 ? priceUsd : 1);
  let dailyMin = 0;
  if (mode.maxDays) {
    // 每日最低产出 = (算力 x 3) / 300 / 4小时均价
    dailyMin = totalPayout / mode.maxDays;
  }
  const daysToBreakEven = dailyMin > 0 ? Math.ceil(totalPayout / dailyMin) : null;
  return { totalPayout, dailyMin, daysToBreakEven, mode };
}

/**
 * 轮次状态推导
 * @param {object} round 轮次 { target, raised, endAt }
 * @returns {{phase:string, progress:number, isSuccess:boolean}}
 *   phase: 'open' 进行中 | 'success' 成功 | 'failed' 失败
 */
export function deriveRoundStatus(round) {
  const now = Date.now();
  const progress = round.target > 0 ? Math.min(1, round.raised / round.target) : 0;
  if (round.status === 'success') return { phase: 'success', progress: 1, isSuccess: true };
  if (round.status === 'failed') return { phase: 'failed', progress, isSuccess: false };
  if (now >= round.endAt) {
    // 时间到：按是否筹满判定
    const isSuccess = round.raised >= round.target;
    return { phase: isSuccess ? 'success' : 'failed', progress, isSuccess };
  }
  return { phase: 'open', progress, isSuccess: false };
}
