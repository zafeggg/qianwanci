/**
 * 千万次 DApp - 游戏数据状态（Pinia）
 *
 * 职责：统一管理轮次 / 资产 / 用户推荐 / 挖矿 / 价格数据，
 * 通过 api 层（真实后端 + mock 降级）加载，供各视图共享。
 * 对应纯 HTML 版 src/store/index.js 中的 round/assets/user/miner 切片。
 */

import { defineStore } from 'pinia';
import {
  fetchCurrentRound,
  fetchRounds,
  fetchAssets,
  fetchUser,
  fetchMiner,
  fetchPrices,
  submitParticipate,
  submitMinerMode,
} from '../api/index.js';

export const useGameStore = defineStore('game', {
  state: () => ({
    /* 当前轮 */
    round: {
      index: 1,
      target: 0,
      raised: 0,
      startAt: 0,
      endAt: 0,
      status: 'open',
      minLimit: 10,
      maxLimit: 0,
      asset: 'FIBO',
    },
    /* 轮次历史 */
    roundHistory: [],
    /* 资产 */
    assets: {
      balances: {},
      positions: [],
      totalValueUsd: 0,
      totalProfitUsd: 0,
    },
    /* 用户推荐 / 团队 */
    user: {
      directCount: 0,
      teamLevel: null,
      referralIncome: 0,
      teamIncome: 0,
      underTree: [],
      underTreeTotalInvest: 0,
    },
    /* 挖矿 */
    miner: {
      hashrate: 0,
      mode: 'default',   // 展示名（default | fixed300 | tm）
      modeId: 0,         // 数值模式（0 默认三倍出局 / 1 最长300天 / 2 TM）——切换与高亮以它为准
      dailyOutput: 0,    // 后端计算的日产出（币/天）：页面不再自己重算，避免与结算口径不一致
      payoutTotal: 0,
      payoutTarget: 0,   // 三倍出局目标（U 计价）
      targetCoin: 0,     // 三倍出局目标（币本位）
      claimable: 0,
      switchable: false, // 是否还能改模式（已产出/已出局的账户不可改）
      accountCount: 0,
      switching: false,  // 切换请求进行中（防连点）
    },
    /* 价格表 */
    prices: {},
    /* 加载状态 */
    loading: false,
  }),

  getters: {
    /** 当前轮进度（0-1） */
    roundProgress: (state) =>
      state.round.target > 0 ? Math.min(1, state.round.raised / state.round.target) : 0,
    /** 当前轮是否进行中 */
    roundOpen: (state) => state.round.status === 'open',
  },

  actions: {
    /**
     * 刷新当前轮（首页轮询 / 参与后刷新）
     * @param {string} address 钱包地址（用于联动用户数据）
     */
    async refreshRound(address) {
      const [round, rounds, prices] = await Promise.all([
        fetchCurrentRound(),
        fetchRounds(),
        fetchPrices(),
      ]);
      this.round = {
        index: round.index,
        target: round.target,
        raised: round.raised,
        startAt: round.startAt,
        endAt: round.endAt,
        status: round.phase,
        minLimit: round.minLimit,
        maxLimit: round.maxLimit,
        asset: round.asset || 'FIBO',
      };
      this.roundHistory = rounds;
      this.prices = prices;
      // 若已连接钱包，联动刷新资产与用户（保持数据一致）
      if (address) {
        await this.refreshAssetsAndUser(address);
      }
    },

    /** 刷新资产 */
    async refreshAssets(address) {
      const assets = await fetchAssets(address);
      this.assets = assets;
    },

    /** 刷新用户推荐 / 团队 */
    async refreshUser(address) {
      const user = await fetchUser(address);
      this.user = user;
    },

    /** 同时刷新资产与用户（连接钱包后 / 轮次刷新时调用） */
    async refreshAssetsAndUser(address) {
      const [assets, user] = await Promise.all([fetchAssets(address), fetchUser(address)]);
      this.assets = assets;
      this.user = user;
    },

    /** 刷新挖矿 */
    async refreshMiner(address) {
      const miner = await fetchMiner(address);
      this.miner = { ...this.miner, ...miner, switching: false };
    },

    /**
     * 切换算力产出模式（需求#8「算力：选择其中一种方式」）
     *
     * 修复前：页面只改本地 state（假交互，刷新即回默认，后端一无所知）；
     * 现在：调 POST /api/miner/mode（需签名会话），成功后按后端返回值刷新。
     * @param {{address:string, symbol?:string, mode:number}} params
     * @returns {Promise<object>} 后端返回 {mode, modeName, updatedAt}
     */
    async switchMinerMode(params) {
      this.miner.switching = true;
      try {
        const r = await submitMinerMode(params);
        await this.refreshMiner(params.address);
        return r;
      } finally {
        this.miner.switching = false;
      }
    },

    /** 刷新价格表 */
    async refreshPrices() {
      this.prices = await fetchPrices();
    },

    /**
     * 参与当前轮（提交后刷新轮次与资产）
     * @param {object} params {address, roundId, amount, asset}
     * @returns {Promise<object>} 参与结果（含 txHash / position）
     */
    async participate(params) {
      const result = await submitParticipate(params);
      // 参与成功：刷新轮次数据与资产
      await this.refreshRound(params.address);
      return result;
    },
  },
});
