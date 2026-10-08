/**
 * 千万次 DApp - 链上代币读写（充值相关）
 *
 * 职责：把「查链上余额 / 一键充值」这两件事从页面里抽出来，页面只关心状态与提示。
 * 纯逻辑层，不持有 Pinia 状态（与 services/wallet.js、services/auth.js 同风格）。
 *
 * ⚠ 三条底线：
 *  1. **收款地址必须来自后端** `/api/deposit/info`，前端不硬编码、也不回退到任何"演示地址"——
 *     地址写错就是把用户的钱转丢，这类参数没有"降级"的余地。
 *  2. 查余额走**只读 JsonRpcProvider**（不需要用户授权）；转账才用钱包 signer。
 *  3. 金额换算必须用后端下发的 ERC20 decimals 位数（TM/FIBO = 8），不是后端记账的换算倍率（1e8）。
 */

import { ethers } from 'ethers';

/** 最小 ERC20 ABI：只含展示与充值需要的四个方法，不引入整份 ABI */
export const ERC20_MIN_ABI = [
  'function balanceOf(address owner) view returns (uint256)',
  'function decimals() view returns (uint8)',
  'function symbol() view returns (string)',
  'function transfer(address to, uint256 amount) returns (bool)',
];

const RPC_TIMEOUT_MS = 6000;

/** 只读 provider（查余额用，不触发钱包授权） */
export function readProvider(rpcUrl) {
  return new ethers.providers.JsonRpcProvider({ url: rpcUrl, timeout: RPC_TIMEOUT_MS });
}

/**
 * 读取某地址在某 ERC20 合约上的链上余额（返回主单位数值）
 * @returns {Promise<number|null>} 读不到（没配合约/RPC 不通）时返回 null，由界面显示"--"
 */
export async function readTokenBalance({ rpcUrl, contract, owner, decimals }) {
  if (!rpcUrl || !contract || !owner) return null;
  try {
    const provider = readProvider(rpcUrl);
    const token = new ethers.Contract(contract, ERC20_MIN_ABI, provider);
    const raw = await token.balanceOf(owner);
    return Number(ethers.utils.formatUnits(raw, decimals));
  } catch (err) {
    console.warn('[token] 读取链上余额失败', err?.message);
    return null;
  }
}

/**
 * 一键充值：用连接的钱包签名调用 ERC20.transfer(收款地址, 金额)
 * @param {{signer:object, contract:string, to:string, amount:(string|number), decimals:number}} p
 * @returns {Promise<{hash:string, status:number}>}
 */
export async function transferToken({ signer, contract, to, amount, decimals }) {
  if (!signer) throw new Error('钱包未连接');
  if (!contract || !to) throw new Error('充值通道未配置，请联系客服');
  if (String(amount).trim() === '' || Number(amount) <= 0) throw new Error('请输入大于 0 的金额');

  const token = new ethers.Contract(contract, ERC20_MIN_ABI, signer);
  const value = ethers.utils.parseUnits(String(amount), decimals);
  if (value.lte(0)) throw new Error('金额必须大于 0');

  const tx = await token.transfer(to, value);
  const receipt = await tx.wait();
  //receipt.status: 1 成功、0 reverted（ethers v5 在 revert 时会直接抛，这里再兜一层）
  if (receipt && receipt.status === 0) throw new Error('链上交易被回滚');
  return { hash: tx.hash, status: receipt ? receipt.status : 1 };
}
