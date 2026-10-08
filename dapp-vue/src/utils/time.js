/**
 * 千万次 DApp - 时间工具
 */

/**
 * 将剩余毫秒拆分为 天/时/分/秒
 * @param {number} ms 剩余毫秒
 * @returns {{days:number,hours:number,minutes:number,seconds:number}}
 */
export function splitDuration(ms) {
  const total = Math.max(0, Math.floor(ms / 1000));
  const days = Math.floor(total / 86400);
  const hours = Math.floor((total % 86400) / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const seconds = total % 60;
  return { days, hours, minutes, seconds };
}

/**
 * 数字补零（两位）
 * @param {number} n
 * @returns {string}
 */
export function pad2(n) {
  return String(n).padStart(2, '0');
}

/**
 * 格式化时间戳为 YYYY-MM-DD HH:mm
 * @param {number|string|Date} input
 * @returns {string}
 */
export function formatDateTime(input) {
  const date = input instanceof Date ? input : new Date(input);
  if (Number.isNaN(date.getTime())) return '--';
  return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())} ${pad2(date.getHours())}:${pad2(date.getMinutes())}`;
}
