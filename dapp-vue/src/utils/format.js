/**
 * 千万次 DApp - 格式化工具
 */

/**
 * 千分位格式化（保留指定小数位，去除多余尾零）
 * @param {number|string} value 数值
 * @param {number} [decimals=2] 小数位
 * @returns {string} 格式化后的字符串
 */
export function formatNumber(value, decimals = 2) {
  const num = Number(value);
  if (!isFinite(num)) return '--';
  return num.toLocaleString('en-US', {
    minimumFractionDigits: 0,
    maximumFractionDigits: decimals,
  });
}

/**
 * 地址缩写：0x1234...abcd
 * @param {string} address 完整地址
 * @param {number} [head=6] 头部保留位数
 * @param {number} [tail=4] 尾部保留位数
 * @returns {string}
 */
export function shortAddress(address, head = 6, tail = 4) {
  if (!address) return '';
  const str = String(address);
  if (str.length <= head + tail + 3) return str;
  return `${str.slice(0, head)}...${str.slice(-tail)}`;
}

/**
 * 金额格式化：自动选择合适单位（用于大额币量展示）
 * @param {number} value 数值
 * @returns {string} 如 1.2K / 3.45M / 2.1B
 */
export function formatCompact(value) {
  const num = Number(value);
  if (!isFinite(num)) return '--';
  if (Math.abs(num) >= 1e9) return `${(num / 1e9).toFixed(2)}B`;
  if (Math.abs(num) >= 1e6) return `${(num / 1e6).toFixed(2)}M`;
  if (Math.abs(num) >= 1e3) return `${(num / 1e3).toFixed(1)}K`;
  return formatNumber(num, 2);
}

/**
 * 百分比格式化
 * @param {number} rate 小数比例，如 0.13
 * @param {number} [decimals=2] 小数位
 * @returns {string} 如 "13%"；非法输入返回 "--"（与 formatNumber/formatCompact 同口径）
 */
export function formatPercent(rate, decimals = 2) {
  const num = Number(rate);
  // ⚠ 2026-09-30 修复：原实现直接 (Number(rate)*100).toFixed()，遇到 NaN/Infinity 会渲染成
  // "NaN%"/"Infinity%"（例如收益率在无数据时按 0/0 计算）。统一按本文件的约定回 "--"。
  if (!isFinite(num)) return '--';
  return `${(num * 100).toFixed(decimals)}%`;
}

/**
 * 安全解析 BigInt 或字符串为 Number（用于链上返回值）
 * @param {*} value 链上值
 * @param {number} decimals 代币精度
 * @returns {number} 十进制数值
 */
export function toDecimal(value, decimals = 18) {
  try {
    const bn = BigInt(value);
    const divisor = 10n ** BigInt(decimals);
    const intPart = bn / divisor;
    const fracPart = bn % divisor;
    return Number(intPart) + Number(fracPart) / Number(divisor);
  } catch {
    return Number(value) || 0;
  }
}

/**
 * 数字转链上最小单位 BigInt 字符串（ethers v5 BigNumber 兼容）
 * @param {number} value 十进制数值
 * @param {number} decimals 代币精度
 * @returns {string} 最小单位字符串
 */
export function toWeiStr(value, decimals = 18) {
  const num = Number(value);
  if (!isFinite(num) || num < 0) return '0';
  const factor = 10 ** decimals;
  return Math.round(num * factor).toString();
}
