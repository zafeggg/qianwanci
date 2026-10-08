/**
 * 千万次 DApp - 钱包签名登录服务（A 模块前端侧）
 *
 * 职责：把「连接钱包」升级为「连接 + 签名登录」，拿到后端会话票。
 * 本模块是纯逻辑层（不持有状态、不碰 Pinia），状态由 stores/wallet.js 持有。
 *
 * 流程（与后端 http/handler/auth.go 完全对齐）：
 *   1. GET  /api/auth/nonce?address=0x..  → { message, nonce, issuedAt, chainId, brand }
 *   2. signer.signMessage(message)         → EIP-191 personal_sign 签名
 *   3. POST /api/auth/login {address, signature, inviteCode?, name?} → { token, ... }
 *
 * ⚠ 三个必须守住的点：
 *  1. **必须签服务端返回的 message 原文**，前端不做任何拼接。
 *     后端验签时用 nonce 存档里的 chainId/issuedAt 重建原文，前端自己拼就会因格式漂移而失败。
 *  2. **nonce 一次性**：验签通过后端立刻删除；失败（如用户拒签后重试）必须重新取 nonce。
 *  3. **会话票与地址绑定**：localStorage 里同时存地址，钱包切到别的地址时旧票必须作废
 *     （否则在 Auth.EnforceSession=true 的后端上会以旧地址身份发请求）。
 */

import {
  fetchAuthNonce,
  submitAuthLogin,
  fetchAuthMe,
  setAuthToken,
} from '../api/index.js';

const TOKEN_KEY = 'ncf_auth_token';
const ADDRESS_KEY = 'ncf_auth_address';

/** 读取已保存的会话（无则返回 {token:'',address:''}） */
export function loadSession() {
  try {
    return {
      token: localStorage.getItem(TOKEN_KEY) || '',
      address: (localStorage.getItem(ADDRESS_KEY) || '').toLowerCase(),
    };
  } catch {
    /* 禁存储环境：退化为纯内存会话 */
    return { token: '', address: '' };
  }
}

/** 保存会话（token 为空即清空），并同步到 API 层 */
export function saveSession(token, address) {
  const t = token || '';
  const a = (address || '').toLowerCase();
  try {
    if (t) {
      localStorage.setItem(TOKEN_KEY, t);
      localStorage.setItem(ADDRESS_KEY, a);
    } else {
      localStorage.removeItem(TOKEN_KEY);
      localStorage.removeItem(ADDRESS_KEY);
    }
  } catch {
    /* 存储不可用则仅内存生效 */
  }
  setAuthToken(t);
  return { token: t, address: a };
}

/** 清空会话（断开钱包 / 切换账户 / 会话失效时调用） */
export function clearSession() {
  return saveSession('', '');
}

/** 地址比较（大小写不敏感） */
export function sameAddress(a, b) {
  return String(a || '').toLowerCase() === String(b || '').toLowerCase();
}

/**
 * 取 nonce 并签名 → 换取会话票
 * @param {object} signer ethers v5 Signer（来自 Web3Provider.getSigner()）
 * @param {string} address 已连接地址（小写）
 * @param {{inviteCode?:string,name?:string}} [opts]
 * @returns {Promise<{token:string,address:string,walletId:number,inviteCode:string,registered:boolean}>}
 */
export async function signInWithWallet(signer, address, opts = {}) {
  if (!signer) throw new Error('钱包未连接，无法签名登录');
  const nonceData = await fetchAuthNonce(address);
  if (!nonceData?.message) throw new Error('服务端未返回签名原文');

  // ⚠ 必须原样签 message（见文件头第 1 条）
  const signature = await signer.signMessage(nonceData.message);

  const login = await submitAuthLogin({
    address,
    signature,
    inviteCode: opts.inviteCode || '',
    name: opts.name || '',
  });
  if (!login?.token) throw new Error('登录未返回会话票');
  return login;
}

/**
 * 用已保存的会话票探测后端：有效则返回钱包信息，无效/过期返回 null。
 * 注意探测成功不代表可以复用：调用方还须核对 `me.address` 与当前钱包地址一致。
 * @returns {Promise<object|null>}
 */
export async function probeSession(token) {
  if (!token) return null;
  setAuthToken(token);
  try {
    return await fetchAuthMe();
  } catch {
    return null;
  }
}
