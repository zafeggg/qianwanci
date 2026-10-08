/**
 * 千万次 DApp - 钱包连接服务
 *
 * 支持两类钱包发现机制：
 * 1. EIP-6963：现代标准，钱包通过 window.addEventListener('eip6963:announceProvider')
 *    广播自身信息，支持多钱包并存（MetaMask / TokenPocket 等）
 * 2. 传统注入：window.ethereum（MetaMask / TP / FigBox 等共享注入点）
 *
 * 与 GYT dapp 钱包方案对齐：
 * - FigBox 为手机端钱包（内置 DApp 浏览器），连接恒走 window.ethereum
 *   （FigBox 与 MetaMask 共享注入点，检测与连接方式完全一致）
 * - 断开走 EIP-2255 wallet_revokePermissions + localStorage 双重清理
 *
 * ethers v5 API（本项目版本）：
 * - 使用 ethers.providers.Web3Provider（v6 为 BrowserProvider）
 *
 * Vue 版说明：
 * - 本模块为「纯逻辑层」，不直接读写状态
 * - 状态由 Pinia store（stores/wallet.js）持有，调用本模块函数后写入
 */

import { ethers } from 'ethers';
import { CHAIN_CONFIG } from '../constants/config.js';

/* 断开标记 key：存在则说明用户主动断开过，重连需重新授权 */
export const DISCONNECT_FLAG_KEY = 'ncf_wallet_disconnected';

/** 支持的已知钱包图标（emoji 占位，后续可替换为 SVG） */
const KNOWN_WALLETS = {
  metaMask: { name: 'MetaMask', icon: '🦊', providerKey: 'isMetaMask' },
  tokenPocket: { name: 'TokenPocket', icon: '💎', providerKey: 'isTokenPocket' },
  figBox: { name: 'FigBox', icon: '🔐', providerKey: 'isAlphaWallet' },
  trust: { name: 'Trust Wallet', icon: '🛡️', providerKey: 'isTrust' },
  imToken: { name: 'imToken', icon: '🟣', providerKey: 'isImToken' },
  other: { name: '浏览器钱包', icon: '👛', providerKey: null },
};

/**
 * 强识别 FIGBOX / FIBO 钱包（从 GYT dapp 移植）
 * 兼容两种品牌特征：FIBO（isFibo / fiboWallet）与 FIGBOX（isFigbox / figboxWallet）
 *
 * 识别策略（2026-09-02 调整）：仅信任品牌标识与 name，取消 isMetaMask 伪装比对。
 * 依据：FigBox 新版不伪装 isMetaMask，统一带 isAlphawallet 品牌标识，直接以
 * isAlphaWallet / isFibo / isFigbox 等为准，避免旧引用比对在真机失效时误判为 MetaMask。
 * @param {object} p 钱包注入的 provider 对象
 * @returns {boolean} 是否为 FIGBOX / FIBO 钱包
 */
export function isFiboProvider(p) {
  if (!p) return false;
  // 品牌特征标识（isAlphawallet 最优先，兼容新旧写法）
  if (
    p.isAlphawallet ||
    p.isAlphaWallet ||
    p.isFibo ||
    p.isFiboWallet ||
    p.isFigbox ||
    p.isFigBox ||
    p.isFIGBOX
  ) {
    return true;
  }
  // provider.name 兜底
  const nm = String(p.name || '').toLowerCase();
  if (nm.includes('fibo') || nm.includes('figbox')) return true;
  return false;
}

/**
 * 是否为移动设备（手机 / 平板）
 * 用于移动端兜底：钱包内置浏览器注入 window.ethereum 但无品牌特征时，视为 FIGBOX
 * @returns {boolean}
 */
export function isMobileDevice() {
  if (typeof navigator === 'undefined') return false;
  return (
    /Android|iPhone|iPad|iPod|Mobile/i.test(navigator.userAgent) ||
    navigator.maxTouchPoints > 1
  );
}

/**
 * 获取钱包身份标识（用于跨 provider 去重）
 * 同一个钱包可能同时通过 EIP-6963 广播 + 注入 window.ethereum + 专属对象注入，
 * 按身份（fibo / metamask / tokenpocket ...）合并，避免弹窗重复展示
 * @param {object} entry {rdns, provider} 等发现条目
 * @returns {string} 钱包身份 id
 */
function getWalletId(entry) {
  const p = entry.provider;
  if (p && isFiboProvider(p)) return 'fibo';
  if (p?.isTokenPocket) return 'tokenpocket';
  if (p?.isMetaMask) return 'metamask';
  if (p?.isTrust) return 'trust';
  if (p?.isImToken) return 'imtoken';
  if (p?.isNovaWallet) return 'nova';
  if (p?.isCoinbaseWallet) return 'coinbase';
  if (p?.isRabby) return 'rabby';
  if (p?.isOKXWallet) return 'okx';
  if (entry.rdns) {
    const rd = String(entry.rdns).toLowerCase();
    if (rd.includes('fibo') || rd.includes('figbox') || rd === 'figbox') return 'fibo';
    // 'injected' / 'unknown' 为占位 rdns，不代表钱包身份，视为未知
    if (rd !== 'injected' && rd !== 'unknown') return rd;
  }
  return 'unknown';
}

/**
 * 解析 FigBox 真实 provider（固定入口 provider 为 null 时调用）
 * 与 GYT dapp selectWallet('fibo') 完全一致：
 * - 检测 = 小狐狸的检测（window.ethereum 存在即检测到）
 * - 连接 = 小狐狸的连接（直接使用 window.ethereum）
 * FigBox 与小狐狸共享 window.ethereum 注入点，在 FigBox 内置浏览器中
 * window.ethereum 即 FigBox 自身，无需做专属对象扫描。
 * @returns {object|null} 可用的 provider，未注入时返回 null
 */
export function resolveFigBoxProvider() {
  if (window.ethereum && typeof window.ethereum.request === 'function') {
    return window.ethereum;
  }
  return null;
}

/**
 * 根据 provider 特征识别钱包名称
 * 注意：FigBox 识别完全依赖品牌标识（isAlphaWallet 等），不依赖 isMetaMask
 * @param {object} provider 钱包注入的 provider 对象
 * @returns {string} 钱包显示名
 */
export function detectWalletName(provider) {
  if (!provider) return '未知钱包';
  if (isFiboProvider(provider)) return KNOWN_WALLETS.figBox.name;
  if (provider.isMetaMask) return KNOWN_WALLETS.metaMask.name;
  if (provider.isTokenPocket) return KNOWN_WALLETS.tokenPocket.name;
  if (provider.isTrust) return KNOWN_WALLETS.trust.name;
  if (provider.isImToken) return KNOWN_WALLETS.imToken.name;
  if (provider.isNovaWallet) return 'Nova Wallet';
  if (provider.isCoinbaseWallet) return 'Coinbase Wallet';
  return KNOWN_WALLETS.other.name;
}

/**
 * 发现可用钱包列表（EIP-6963 + 传统注入，按钱包身份去重）
 * 返回条目字段：{rdns, name, icon, provider, walletId, uid}
 * - provider 为 null 的条目是 FIGBOX 固定入口占位（未注入时仍展示），
 *   点击时由 resolveFigBoxProvider() 解析（恒走 window.ethereum）
 * @returns {Promise<Array<{rdns:string,name:string,icon:string,provider:object|null,walletId:string,uid:string}>>}
 */
export async function discoverWallets() {
  const wallets = [];
  const seenRefs = new Set(); // 按 provider 引用去重
  const seenIds = new Set();  // 按钱包身份去重（fibo / metamask / ...）
  let uidSeq = 0;

  /** 统一入列：同 provider 引用绝不重复；同身份只保留先到者（EIP-6963 优先）
   *  @param {object} entry 钱包条目
   *  @param {string} [forcedId] 强制身份（如移动端兜底视为 FIGBOX） */
  const pushWallet = (entry, forcedId) => {
    if (entry.provider) {
      if (seenRefs.has(entry.provider)) return;
      seenRefs.add(entry.provider);
    }
    const id = forcedId || getWalletId(entry);
    if (id !== 'unknown') {
      if (seenIds.has(id)) return;
      seenIds.add(id);
    }
    wallets.push({ ...entry, walletId: id, uid: `w${++uidSeq}` });
  };

  // 1) EIP-6963 异步发现（限时 1.5s，避免无限等待）
  await new Promise((resolve) => {
    let settled = false;
    const finish = () => {
      if (!settled) {
        settled = true;
        resolve();
      }
    };
    const onAnnounce = (event) => {
      const info = event.detail;
      if (!info?.provider) return;
      pushWallet({
        rdns: info.info?.rdns || 'unknown',
        name: info.info?.name || detectWalletName(info.provider),
        icon: info.info?.icon || KNOWN_WALLETS.other.icon,
        provider: info.provider,
      });
    };
    window.addEventListener('eip6963:announceProvider', onAnnounce);
    // 广播请求
    window.dispatchEvent(new Event('eip6963:requestProvider'));
    // 超时兜底
    setTimeout(finish, 1500);
  });

  // 2) 传统注入兜底：window.ethereum 存在且未被上面收录
  //    移动端兜底：无品牌特征 + 移动设备 → 视为 FIGBOX（FIBO 主网官方钱包）
  if (window.ethereum) {
    const baseId = getWalletId({ provider: window.ethereum, rdns: 'injected' });
    const isFigboxGuess = baseId === 'unknown' && isMobileDevice();
    pushWallet(
      {
        rdns: 'injected',
        name: isFigboxGuess ? KNOWN_WALLETS.figBox.name : detectWalletName(window.ethereum),
        icon: isFigboxGuess ? KNOWN_WALLETS.figBox.icon : KNOWN_WALLETS.other.icon,
        provider: window.ethereum,
      },
      isFigboxGuess ? 'fibo' : undefined,
    );
  }

  // 3) FIGBOX 固定入口：无论当前环境是否检测到都展示（手机端钱包，冷钱包场景）
  //    仅占位（provider=null），点击连接时由 resolveFigBoxProvider() 解析。
  //    与 GYT dapp 一致：连接恒走 window.ethereum，不做专属对象扫描。
  if (!seenIds.has('fibo')) {
    pushWallet({
      rdns: 'figbox',
      name: KNOWN_WALLETS.figBox.name,
      icon: KNOWN_WALLETS.figBox.icon,
      provider: null,
    });
  }

  return wallets;
}

/**
 * 请求切换/添加 FIBONACCI 链（chainId 12306）
 * 使用 wallet_addEthereumChain 供钱包添加自定义网络
 * @param {object} provider 钱包 provider
 * @returns {Promise<boolean>} 是否成功
 */
async function ensureChain(provider) {
  try {
    const hexChainId = CHAIN_CONFIG.chainIdHex;
    const current = await provider.request({ method: 'eth_chainId' });
    if (current && String(current).toLowerCase() === hexChainId.toLowerCase()) {
      return true; // 已在目标链
    }
    // 先尝试直接切换
    try {
      await provider.request({
        method: 'wallet_switchEthereumChain',
        params: [{ chainId: hexChainId }],
      });
      return true;
    } catch (err) {
      // 4902 = 未添加该链，需要走 add 流程
      if (err?.code !== 4902) throw err;
      const addParams = {
        chainId: hexChainId,
        chainName: CHAIN_CONFIG.chainName,
        rpcUrls: [CHAIN_CONFIG.rpcUrl],
        nativeCurrency: {
          name: CHAIN_CONFIG.nativeSymbol,
          symbol: CHAIN_CONFIG.nativeSymbol,
          decimals: CHAIN_CONFIG.nativeDecimals,
        },
      };
      // 私链/测试网没有区块浏览器：空串会被钱包判为非法参数，直接不带该字段
      if (CHAIN_CONFIG.explorerUrl) addParams.blockExplorerUrls = [CHAIN_CONFIG.explorerUrl];
      await provider.request({
        method: 'wallet_addEthereumChain',
        params: [addParams],
      });
      return true;
    }
  } catch (err) {
    console.warn('[wallet] 切换链失败', err);
    return false;
  }
}

/**
 * 连接钱包（纯逻辑：不写状态，返回结果由 Pinia store 落库）
 * @param {object} provider 钱包 provider
 * @param {string} walletName 钱包名
 * @returns {Promise<{ok:boolean, error?:string, wallet?:object}>}
 */
export async function connectWallet(provider, walletName) {
  try {
    // 用户之前断开过则强制清理（避免残留授权态）
    if (localStorage.getItem(DISCONNECT_FLAG_KEY)) {
      try {
        await provider.request({
          method: 'wallet_revokePermissions',
          params: [{ eth_accounts: {} }],
        });
      } catch {
        /* 忽略 revoke 失败，继续尝试 */
      }
      localStorage.removeItem(DISCONNECT_FLAG_KEY);
    }

    // 1) 确保在 FIBONACCI 链
    const chainOk = await ensureChain(provider);
    if (!chainOk) {
      return { ok: false, error: '无法切换到 FIBONACCI 网络，请在钱包内手动切换' };
    }

    // 2) 请求账户授权
    const accounts = await provider.request({ method: 'eth_requestAccounts' });
    if (!accounts || !accounts.length) {
      return { ok: false, error: '未获取到钱包地址' };
    }

    // 3) 构造 ethers v5 Web3Provider（注意：v6 中是 BrowserProvider）
    const ethersProvider = new ethers.providers.Web3Provider(provider);
    const signer = ethersProvider.getSigner();
    const network = await ethersProvider.getNetwork();

    // 4) 订阅账户 / 链切换事件（回调中分发全局事件，由 store 层监听）
    _bindProviderEvents(provider);

    // 5) 返回连接结果（状态写入由调用方 Pinia store 完成）
    return {
      ok: true,
      wallet: {
        connected: true,
        address: accounts[0].toLowerCase(),
        chainId: network.chainId || CHAIN_CONFIG.chainId,
        provider: ethersProvider,
        signer,
        walletName,
      },
    };
  } catch (err) {
    console.error('[wallet] 连接失败', err);
    return { ok: false, error: err?.message || '连接钱包失败' };
  }
}

/** 绑定 provider 事件：账户切换、链切换、断开（分发全局事件，避免直接依赖 store） */
function _bindProviderEvents(provider) {
  const emit = (type, payload) =>
    window.dispatchEvent(new CustomEvent(type, { detail: payload }));

  const handleAccountsChanged = (accounts) => {
    if (!accounts || accounts.length === 0) {
      // 钱包侧断开
      emit('ncf:wallet-disconnected', {});
      return;
    }
    emit('ncf:wallet-account-changed', { address: accounts[0].toLowerCase() });
  };
  const handleChainChanged = () => window.location.reload();
  const handleDisconnect = () => emit('ncf:wallet-disconnected', {});

  // 先移除旧监听避免重复绑定
  provider.removeListener?.('accountsChanged', handleAccountsChanged);
  provider.removeListener?.('chainChanged', handleChainChanged);
  provider.removeListener?.('disconnect', handleDisconnect);
  provider.on?.('accountsChanged', handleAccountsChanged);
  provider.on?.('chainChanged', handleChainChanged);
  provider.on?.('disconnect', handleDisconnect);
}

/**
 * 断开钱包（纯逻辑：撤销权限 + localStorage 标记，状态清理由 Pinia 处理）
 * @param {object} provider ethers Web3Provider（或原生 provider）
 * @returns {Promise<void>}
 */
export async function revokeProvider(provider) {
  if (provider) {
    try {
      // EIP-2255：撤销账户权限
      await provider.send?.('wallet_revokePermissions', [{ eth_accounts: {} }]);
    } catch {
      /* 部分钱包不支持 revoke，忽略 */
    }
  }
  // localStorage 标记：重连时必须重新走授权流程
  localStorage.setItem(DISCONNECT_FLAG_KEY, '1');
  // 通知全局（由 store 层监听并清理状态）
  window.dispatchEvent(new CustomEvent('ncf:wallet-disconnected'));
}

/**
 * 页面加载时恢复上次连接（如 localStorage 保存过地址且未断开）
 * 注意：为安全考虑不做静默重连，只提示用户可一键重连
 * @returns {boolean} 是否有可恢复的连接（由调用方传入当前连接状态判断）
 */
export function hasRecoverableSession(isConnected = false) {
  return !localStorage.getItem(DISCONNECT_FLAG_KEY) && isConnected;
}
