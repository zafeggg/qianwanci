/**
 * 千万次 DApp - 钱包状态（Pinia）
 *
 * 职责：
 * - 持有钱包连接状态（address / provider / walletName）
 * - 持有**签名登录会话**（token / walletId / inviteCode / sessionActive）
 * - 调用 services/wallet.js 完成连接、services/auth.js 完成签名登录
 * - 监听 provider 全局事件（账户切换 / 断开）保持状态同步
 *
 * 连接与登录的关系（2026-09-22 补齐需求 #17 前端侧）：
 *   连接钱包 = 拿到地址（只证明「能读这个地址」）
 *   签名登录 = 拿到会话票（证明「这个地址确实是本人的」）
 *   connect() 会先连接再自动尝试签名登录；签名失败**不阻断连接**（后端 Auth.EnforceSession
 *   默认 false，存量前端仍可免票读写），但 sessionActive 保持 false，界面会显示「未签名」，
 *   并且不会把任何会话票带给 /api（绝不做「假登录」）。
 */

import { defineStore } from 'pinia';
import { markRaw } from 'vue';
import {
  connectWallet as connect,
  revokeProvider,
  discoverWallets,
} from '../services/wallet.js';
import {
  signInWithWallet,
  loadSession,
  saveSession,
  clearSession,
  probeSession,
  sameAddress,
} from '../services/auth.js';

export const useWalletStore = defineStore('wallet', {
  state: () => ({
    connected: false,
    address: '',
    chainId: '',
    provider: null,      // ethers Web3Provider
    signer: null,
    walletName: '',
    connecting: false,   // 防重复点击

    // ---- 签名登录会话 ----
    token: '',           // 后端会话票（Authorization: Bearer）
    walletId: 0,         // 后端 wallet.id（首登会自动注册）
    inviteCode: '',      // 后端下发的邀请码
    registered: false,   // 本次登录是否顺手完成了注册
    sessionActive: false,// 是否已拿到有效会话
    sessionReused: false,// 本次会话是否复用了本地已存票（未重新签名）
    authError: '',       // 签名登录失败原因（不改 connected，仅在界面提示）
    signingIn: false,    // 签名请求进行中
  }),

  getters: {
    /** 已连接但没拿到会话票：界面据此提示「未签名」 */
    needsSignature(state) {
      return state.connected && !state.sessionActive;
    },
  },

  actions: {
    /**
     * 连接钱包（选择弹窗调用）：连接成功后自动尝试签名登录
     * @param {object} provider 原生钱包 provider
     * @param {string} walletName 钱包名
     * @param {{inviteCode?:string,name?:string}} [authOpts] 可选邀请码（首登注册用）
     * @returns {Promise<{ok:boolean, error?:string, address?:string, sessionActive?:boolean}>}
     */
    async connect(provider, walletName, authOpts = {}) {
      if (this.connecting) return { ok: false, error: '正在连接中，请稍候' };
      this.connecting = true;
      try {
        const result = await connect(provider, walletName);
        if (result.ok && result.wallet) {
          const { wallet } = result;
          this.connected = true;
          this.address = wallet.address;
          this.chainId = wallet.chainId;
          // ⚠ 必须 markRaw：Pinia state 是 reactive 代理，而 ethers 的 Web3Provider / JsonRpcSigner
          // 内部对 provider 字段做「品牌校验」（非 configurable 只读属性）。
          // 被 Proxy 包一层后读 `signer.provider` 会抛
          //   "'get' on proxy: property 'provider' is a read-only and non-configurable data property …"
          // 导致 signMessage/发交易全部失败（2026-09-22 实测踩到：连接成功但一签名就报这个错）。
          this.provider = markRaw(wallet.provider);
          this.signer = markRaw(wallet.signer);
          this.walletName = wallet.walletName;
          // provider 事件由 services/wallet.js 统一绑定并分发全局事件，
          // store 只监听全局事件即可（见 _listenGlobalEvents）
          await this.ensureSession(authOpts);
          return {
            ok: true,
            address: wallet.address,
            sessionActive: this.sessionActive,
            authError: this.authError,
          };
        }
        return result;
      } finally {
        this.connecting = false;
      }
    },

    /**
     * 确保有会话票：本地已存票且地址一致 → 先探测复用；否则走签名登录。
     * 复用能省掉一次钱包签名弹窗（地址一致时票本来就是同一身份的），探测失败自动回落到重新签名。
     * @param {{inviteCode?:string,name?:string}} [authOpts]
     */
    async ensureSession(authOpts = {}) {
      const saved = loadSession();
      if (saved.token && sameAddress(saved.address, this.address)) {
        const me = await probeSession(saved.token);
        if (me && sameAddress(me.address, this.address)) {
          this._applySession(saved.token, me, true);
          return { ok: true, reused: true };
        }
        // 票已失效或身份对不上：作废后重新签名
        clearSession();
      }
      return this.signIn(authOpts);
    },

    /**
     * 主动签名登录（也可由界面「重新签名」按钮触发）
     * @param {{inviteCode?:string,name?:string}} [authOpts]
     */
    async signIn(authOpts = {}) {
      if (!this.signer) return { ok: false, error: '钱包未连接' };
      if (this.signingIn) return { ok: false, error: '正在签名中，请稍候' };
      this.signingIn = true;
      this.authError = '';
      try {
        const login = await signInWithWallet(this.signer, this.address, authOpts);
        this._applySession(login.token, login, false);
        return { ok: true, login };
      } catch (err) {
        // 登录失败必须清干净：绝不留下半个会话，也绝不把旧票继续带给 /api
        clearSession();
        this.token = '';
        this.sessionActive = false;
        this.sessionReused = false;
        this.authError = err?.message || '签名登录失败';
        return { ok: false, error: this.authError };
      } finally {
        this.signingIn = false;
      }
    },

    /**
     * 断开钱包（撤销权限 + 清理状态 + 作废会话）
     * @param {object} providerOverride 可选：传入原生 provider 撤销权限
     */
    async disconnect(providerOverride) {
      // 优先用 override（原生 provider），否则用 store 中的 ethers provider 的原生层
      const rawProvider = providerOverride || this.provider?.provider;
      await revokeProvider(rawProvider);
      this._clearSessionState();
      this.$reset();
    },

    /** 打开钱包选择弹窗（由组件调用，返回连接结果） */
    async openWalletPicker() {
      const wallets = await discoverWallets();
      return { wallets, store: this };
    },

    /** 把会话写入 state（token 落 localStorage 由 services/auth.js 负责） */
    _applySession(token, data, reused) {
      saveSession(token, this.address);
      this.token = token;
      this.walletId = Number(data?.walletId || data?.id || 0);
      this.inviteCode = data?.inviteCode || '';
      this.registered = !!data?.registered;
      this.sessionActive = true;
      this.sessionReused = !!reused;
      this.authError = '';
    },

    /** 只清会话，不动钱包连接态 */
    _clearSessionState() {
      clearSession();
      this.token = '';
      this.walletId = 0;
      this.inviteCode = '';
      this.registered = false;
      this.sessionActive = false;
      this.sessionReused = false;
      this.authError = '';
    },

    /** 监听全局钱包事件（账户切换 / 断开），在应用初始化时调用一次 */
    _listenGlobalEvents() {
      if (this._listening) return;
      this._listening = true;
      window.addEventListener('ncf:wallet-account-changed', (e) => {
        const next = e.detail?.address || '';
        if (sameAddress(next, this.address)) return;
        // 账户变了：旧票属于旧地址，必须立刻作废（见 services/auth.js 文件头第 3 条）
        this._clearSessionState();
        this.address = next;
      });
      window.addEventListener('ncf:wallet-disconnected', () => {
        this._clearSessionState();
        this.$reset();
      });
      // 后端判定会话失效（HTTP 401：票据过期 / 在别处登录被顶号 / 服务端重启）：
      // 立刻作废本地票并提示重新签名 —— 既不带着失效票继续请求，也不静默降级成假数据
      // （事件由 api 层在收到 401 时派发，见 api/index.js#notifySessionExpired）
      window.addEventListener('ncf:session-expired', () => {
        if (!this.connected) return;
        this._clearSessionState();
        this.authError = '登录已过期，请重新签名';
      });
    },
  },
});
