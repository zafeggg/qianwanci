/**
 * 千万次 DApp - 规则数值的后端同步（2026-09-30 新增）
 *
 * 背景（为什么要这个文件）：
 *   规则数值原先在**三处各写一份** —— 后端 `core/impl/config.go`（真正参与结算，可经 /ops/params 热改）、
 *   后端适配层硬编码、前端 `constants/config.js`。运维一改参数，前端还是旧数字：
 *   页面写着"13% 静态收益"，实际结算比例可能已经不同 —— 用户看到的就是"说好的收益不对"。
 *
 * 做法：应用启动时拉一次 `GET /api/rules`（后端**实际生效值**），把结果**就地覆盖**到
 *   `constants/config.js` 导出的规则对象上。页面与 engine 仍按原方式读取这些对象，
 *   因此不需要改动各处读取点，也天然让"展示口径 = 结算口径"。
 *
 * 兜底：接口失败时保留本地默认值（不至于白屏），并把 `rulesState.loaded` 置 false，
 *   页面可据此提示"规则数值取自本地默认值"。
 */
import { GAME_RULES, REFERRAL_RULES, TEAM_RULES, MINER_RULES, WITHDRAW_RULES, BRAND, MOCK_ENABLED } from './config.js';
import { requestRules } from '../api/index.js';

/** 同步状态（页面可读）：loaded=是否已用后端值覆盖；error=失败原因；at=同步时间 */
export const rulesState = {
  loaded: false,
  error: '',
  at: 0,
};

/** 把后端下发的数值就地覆盖到本地规则对象（只覆盖后端明确给出的字段，缺省保留本地默认） */
export function applyBackendRules(payload) {
  if (!payload || typeof payload !== 'object') return false;

  const setNum = (obj, key, val) => {
    if (typeof val === 'number' && Number.isFinite(val) && val !== 0) obj[key] = val;
  };

  // 静态收益 / 结算轮次 / 倒2倒3扣除比例
  setNum(GAME_RULES, 'staticReturnRate', payload.staticRewardRate);
  setNum(GAME_RULES, 'settleOffset', payload.settleOffset);
  setNum(GAME_RULES, 'penaltyRateForLastMinus', payload.lossRate);
  // 通缩目标（提币手续费销毁至仅剩该数量）：同时更新 BRAND.feeToken.burnTarget，
  // 因为规则页/提币页的手续费说明读的是它（原先写死 7777）
  setNum(GAME_RULES, 'burnTarget', payload.burnTarget);
  if (BRAND?.feeToken && typeof payload.burnTarget === 'number' && payload.burnTarget > 0) {
    BRAND.feeToken.burnTarget = payload.burnTarget;
  }
  // 提币手续费比例
  setNum(WITHDRAW_RULES, 'feeRate', payload.feeRate);
  // 最低起提额（owner 2026-09-30 敲定 100 个起提）；0 = 不限（也要接收，用 >=0 判断）
  if (typeof payload.withdrawMinAmount === 'number' && Number.isFinite(payload.withdrawMinAmount) && payload.withdrawMinAmount >= 0) {
    WITHDRAW_RULES.minAmount = payload.withdrawMinAmount;
  }
  if (typeof payload.feeSymbol === 'string' && payload.feeSymbol) {
    WITHDRAW_RULES.feeSymbol = payload.feeSymbol;
  }

  // 动态收益各代比例（第 1/3/5 代）：按 depth 覆盖已有档位，不改动档位结构
  const shardByDepth = { 1: payload.dynamicShardRate1, 3: payload.dynamicShardRate3, 5: payload.dynamicShardRate5 };
  for (const tier of REFERRAL_RULES.tiers || []) {
    for (const d of tier.depths || []) {
      const v = shardByDepth[d.depth];
      if (typeof v === 'number' && Number.isFinite(v) && v > 0) d.rate = v;
    }
  }

  // 团队等级：只覆盖后端真正使用的口径 —— F1 看"直推数 + 同期伞下参投人数"，
  // F2/F3 看"伞下各级数量"（本地 levels[1]/[2] 的 needDirect 是 0，不能被 F1 的门槛覆盖掉）
  if (Array.isArray(payload.teamLevels)) {
    for (const lv of payload.teamLevels) {
      const local = (TEAM_RULES.levels || []).find((x) => x.level === lv.level);
      if (!local) continue;
      setNum(local, 'rate', lv.rate);
      if (lv.level === 'F1') {
        setNum(local, 'needDirect', lv.needDirect);
        setNum(local, 'needUnderTreeActive', lv.needUnderTreeActive);
      } else {
        setNum(local, 'needSubCount', lv.needSubCount);
      }
    }
  }

  // 算力矿机：三倍倍数 / 最长天数 / 矿池占比（payoutMultiple 与 maxDays 是**每个模式**上的字段）
  setNum(MINER_RULES, 'poolDailyRatio', payload.hashPowerPoolDailyRatio);
  for (const mode of MINER_RULES.modes || []) {
    setNum(mode, 'payoutMultiple', payload.hashPowerBuff);
    if (mode.maxDays) setNum(mode, 'maxDays', payload.hashPowerMaxDays);
  }

  rulesState.loaded = true;
  rulesState.error = '';
  rulesState.at = Date.now();
  return true;
}

/**
 * 拉取并应用后端规则数值。**永不抛错**（失败只记录状态），因此可以在启动时 fire-and-forget。
 * @returns {Promise<boolean>} 是否成功应用
 */
export async function loadBackendRules() {
  // mock 模式（仅联调）不覆盖：那时后端可能根本不可用，保持本地示例规则
  if (MOCK_ENABLED) {
    rulesState.error = 'mock 模式：规则数值取自本地默认值';
    return false;
  }
  try {
    const data = await requestRules();
    return applyBackendRules(data);
  } catch (e) {
    rulesState.loaded = false;
    rulesState.error = e?.message || '规则数值获取失败，已回退本地默认值';
    return false;
  }
}
