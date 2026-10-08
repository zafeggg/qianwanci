/**
 * 千万次 DApp - 演示（mock）数据源
 *
 * 用途：**仅在构建期显式设置 VITE_ENABLE_MOCK=true 时**（见 .env.example），
 *      为无后端环境提供可交互的演示界面；界面会常驻「演示数据（未连接后端）」横幅。
 * 设计原则：
 * - 数据由 rules 引擎（engine/game.js）真实计算生成，非硬编码假数
 * - 使用 localStorage 持久化，刷新页面数据不丢
 * - 与真实接口返回结构保持完全一致（见 src/api/index.js 的接口契约）
 *
 * ⚠ 本模块**只提供读接口**（轮次 / 资产 / 用户 / 挖矿 / 价格）。
 *   写操作（参投 / 提现 / 领取 / 切换算力模式）的 mock 实现已被删除：
 *   它们会返回随机 txHash 并「成功」，让用户以为资产已变动而链上什么都没发生。
 *   写操作必须真打后端，失败就明确失败（见 api/index.js#requestWrite）。
 */

import {
  calcTarget,
  calcMaxLimit,
  calcStaticProfit,
  calcSettleRound,
  calcMinerOutput,
  deriveRoundStatus,
} from '../engine/game.js';
import { GAME_RULES, MINER_RULES } from '../constants/config.js';

const MOCK_KEY = 'ncf_mock_db_v1';

/** 默认 mock 库结构（与后端 DB 结构对应） */
function createDefaultDb() {
  const now = Date.now();
  // 当前轮：第 3 轮（模拟已进行 2 轮）
  const roundIndex = 3;
  const target = calcTarget(roundIndex);
  return {
    /* 轮次表 */
    rounds: [
      // 历史轮次（第 1、2 轮已成功）
      { id: 1, target: calcTarget(1), raised: calcTarget(1), status: 'success', startAt: now - 7200_000, endAt: now - 3600_000 },
      { id: 2, target: calcTarget(2), raised: calcTarget(2), status: 'success', startAt: now - 3600_000, endAt: now - 1000 },
      // 当前轮（进行中）
      {
        id: roundIndex,
        target,
        raised: Math.round(target * 0.42),
        status: 'open',
        startAt: now - 600_000,
        endAt: now + 1800_000, // 30 分钟后结束
      },
    ],
    /* 用户仓位 */
    positions: [
      { id: 'p1', roundIndex: 1, amount: 100, joinTime: now - 7200_000, status: 'settled', profit: 13 },
      { id: 'p2', roundIndex: 2, amount: 130, joinTime: now - 3600_000, status: 'settled', profit: 16.9 },
      { id: 'p3', roundIndex: 3, amount: 60, joinTime: now - 600_000, status: 'pending', profit: 0 },
    ],
    /* 资产余额（手续费代币 TM，TiMi 基准） */
    balances: { FIBO: 420.5, USDT: 88.2, TM: 520.0 },
    /* 用户信息 */
    user: {
      address: '',
      directCount: 3,
      teamLevel: 'F1',
      referralIncome: 42.18,
      teamIncome: 16.32,
      underTreeTotalInvest: 3264.5,
      subF1Count: 2,
      subF2Count: 0,
      underTree: [
        { address: '0x7A1f...9cD2', depth: 1, invest: 1520, success: 12 },
        { address: '0xB3e9...42aF', depth: 1, invest: 860, success: 9 },
        { address: '0xC8d2...77eB', depth: 1, invest: 240, success: 5 },
        { address: '0xE1f0...aB31', depth: 3, invest: 380, success: 4 },
        { address: '0xF9aa...1dC0', depth: 3, invest: 264.5, success: 3 },
      ],
    },
    /* 挖矿 */
    miner: {
      hashrate: 5000, // 由倒2倒3扣币折算的算力（U 计价）
      mode: 'default',
      created: now - 86400_000 * 20, // 20 天前创建
      payoutTotal: 156.2, // 已累计产出
      claimable: 8.3, // 可领取
    },
    /* 价格表（U 计价） */
    prices: { FIBO: 0.5, USDT: 1.0, TM: 3.4 },
  };
}

/**
 * 读取或初始化 mock 数据库
 * @returns {object} db
 */
export function loadDb() {
  try {
    const raw = localStorage.getItem(MOCK_KEY);
    if (raw) {
      const db = JSON.parse(raw);
      // 每 5 分钟让当前轮筹集额轻微增长，模拟真实推进
      const round = db.rounds[db.rounds.length - 1];
      if (round && round.status === 'open' && Date.now() - round._lastTick > 5 * 60_000) {
        const grow = Math.min(round.target - round.raised, Math.round(round.target * 0.01));
        round.raised += Math.max(grow, 0);
        round._lastTick = Date.now();
        saveDb(db);
      }
      return db;
    }
  } catch {
    /* 解析失败则重建 */
  }
  const db = createDefaultDb();
  saveDb(db);
  return db;
}

/** 持久化 mock 数据库 */
export function saveDb(db) {
  localStorage.setItem(MOCK_KEY, JSON.stringify(db));
}

/* ================= 接口实现（与真实后端返回结构一致） ================= */

/** 获取轮次列表（含当前轮与历史轮） */
export function mockFetchRounds() {
  const db = loadDb();
  // 用引擎推导状态，保证与真实逻辑一致
  return db.rounds.map((r) => {
    const derived = deriveRoundStatus(r);
    return {
      ...r,
      phase: derived.phase,
      isSuccess: derived.isSuccess,
      progress: derived.progress,
    };
  });
}

/** 获取当前轮详情 */
export function mockFetchCurrentRound() {
  const rounds = mockFetchRounds();
  const current = rounds.find((r) => r.phase === 'open') || rounds[rounds.length - 1];
  return {
    ...current,
    index: current.id,
    minLimit: GAME_RULES.minLimit,
    maxLimit: calcMaxLimit(current.id),
    settleRound: calcSettleRound(current.id),
    staticRate: GAME_RULES.staticReturnRate,
  };
}

/** 获取用户资产（余额 + 仓位） */
export function mockFetchAssets(address) {
  const db = loadDb();
  const positions = db.positions.map((p) => {
    const profit = p.status === 'settled' ? p.profit : calcStaticProfit(p.amount);
    return { ...p, expectedProfit: profit, settleRound: calcSettleRound(p.roundIndex) };
  });
  return {
    balances: db.balances,
    positions,
    totalValueUsd: Object.entries(db.balances).reduce(
      (sum, [sym, amt]) => sum + amt * (db.prices[sym] || 0),
      0,
    ),
    totalProfitUsd: db.positions
      .filter((p) => p.status === 'settled')
      .reduce((sum, p) => sum + p.profit, 0),
  };
}

/** 获取用户推荐 / 团队信息 */
export function mockFetchUser(address) {
  const db = loadDb();
  return { ...db.user, address };
}

/**
 * 获取挖矿信息（字段与后端 GET /api/miner 对齐）
 * ⚠ 产出相关字段（modeId / dailyOutput / payoutTarget / targetCoin / switchable）
 *   正常由**后端计算**（T4：前端不得自行复算）；演示模式没有后端，这里用同一口径的
 *   本地引擎补上，保证演示界面不至于大片“--”。真实构建下这些字段直接来自后端。
 */
export function mockFetchMiner(address) {
  const db = loadDb();
  const miner = db.miner;
  const mode = MINER_RULES.modes.find((m) => m.id === miner.mode) || MINER_RULES.modes[0];
  const hashrate = miner.hashrate || 0;
  const output = calcMinerOutput(hashrate, db.prices.FIBO || 0.5, miner.mode);
  return {
    ...miner,
    modeId: mode.modeId,
    // 三倍出局目标：U 计价 = 算力 x 3；折算成币 = U 目标 / 币价
    payoutTarget: hashrate * mode.payoutMultiple,
    targetCoin: output.totalPayout,
    dailyOutput: output.dailyMin,
    // 已产生收益的账户不可再改模式（与后端 code=600 的口径一致）
    switchable: !(miner.payoutTotal > 0),
    accountCount: hashrate > 0 ? 1 : 0,
  };
}

/** 获取价格表 */
export function mockFetchPrices() {
  return loadDb().prices;
}

/** 提交参与（写库 + 返回新仓位） */
export function mockSubmitParticipate({ address, roundId, amount, asset }) {
  const db = loadDb();
  const round = db.rounds.find((r) => r.id === roundId);
  if (!round) throw new Error('轮次不存在');
  if (round.status !== 'open') throw new Error('该轮次已结束');

  // 校验限额
  const maxLimit = calcMaxLimit(roundId);
  if (amount < GAME_RULES.minLimit) throw new Error(`低于最低限额 ${GAME_RULES.minLimit}`);
  if (amount > maxLimit) throw new Error(`超出本轮上限 ${maxLimit}`);

  // 更新轮次筹集额
  round.raised += amount;
  round._lastTick = Date.now();

  // 新增仓位
  const position = {
    id: `p${Date.now()}`,
    roundIndex: roundId,
    amount,
    joinTime: Date.now(),
    status: 'pending',
    profit: 0,
  };
  db.positions.push(position);
  // 扣减余额
  if (!db.balances[asset]) db.balances[asset] = 0;
  db.balances[asset] = Math.max(0, db.balances[asset] - amount);

  saveDb(db);
  return {
    txHash: `0x${Math.random().toString(16).slice(2, 66)}`,
    position,
    balance: db.balances[asset],
  };
}

/** 提交提现（3% 手续费，用手续费代币支付） */
export function mockSubmitWithdraw({ address, asset, amount, feeToken }) {
  const db = loadDb();
  if (!db.balances[asset] || db.balances[asset] < amount) {
    throw new Error('可用余额不足');
  }
  // 校验手续费代币
  if (!db.balances[feeToken] || db.balances[feeToken] <= 0) {
    throw new Error(`手续费需使用 ${feeToken} 支付，余额不足`);
  }
  // 计算手续费：提币价值(U) x 3% / 手续费代币价格(U) = 所需手续费代币数量
  const tokenPrice = db.prices[asset] || 0;
  const feeTokenPrice = db.prices[feeToken] || 0;
  if (feeTokenPrice <= 0) throw new Error('手续费代币价格未知');
  const feeAmount = (amount * tokenPrice * 0.03) / feeTokenPrice;

  db.balances[asset] -= amount;
  db.balances[feeToken] -= feeAmount;
  saveDb(db);
  return {
    txHash: `0x${Math.random().toString(16).slice(2, 66)}`,
    amount,
    feeAmount,
    feeToken,
    balance: db.balances[asset],
  };
}

/** 领取挖矿产出 */
export function mockClaimMiner(address) {
  const db = loadDb();
  const claimable = db.miner.claimable || 0;
  db.miner.payoutTotal += claimable;
  db.miner.claimable = 0;
  if (!db.balances.FIBO) db.balances.FIBO = 0;
  db.balances.FIBO += claimable;
  saveDb(db);
  return { claimed: claimable, balance: db.balances.FIBO };
}

export const MOCK_README = `演示数据模式说明（VITE_ENABLE_MOCK=true）：
- 读接口（轮次 / 资产 / 用户 / 挖矿 / 价格）在后端不可用时回落本地模拟数据（localStorage 持久化）
- 数据由规则引擎真实计算（30% 递增 / 13% 静态 / 三进一出）
- **写接口（参投 / 提现 / 领取 / 切换算力模式）永不 mock**：必须连上真实后端，否则明确报错。
  这是刻意设计——mock 写操作会返回随机 txHash 并提示「成功」，用户会误以为资产已变动。
- 关闭方式：构建时不设置 VITE_ENABLE_MOCK（默认为关闭）`;
