-- 私链出金/销毁后的一致性核查（只读）
-- 用法：mysql -uroot -p123456 crowd_privchain < scripts/privchain-check.sql
SELECT '=== wallet_tx（type 2=提币 10=手续费；hash 应为 0x 真实链上哈希；success 0=成功 1=失败）===' AS section;
SELECT id, symbol, `from` AS from_addr, `to` AS to_addr, amount, type, success,
       LEFT(hash, 70) AS tx_hash
FROM wallet_tx ORDER BY id;

SELECT '=== fee_burn（status 0=待销毁 1=已销毁 2=失败待重试 3=销毁中）===' AS section;
SELECT id, symbol, amount, status, tx_hash FROM fee_burn ORDER BY id;

SELECT '=== wallet_point ===' AS section;
SELECT address, symbol, amount FROM wallet_point ORDER BY symbol;

SELECT '=== 汇总：手续费已扣 vs 待销毁 vs 已销毁（最小单位，应满足 已扣 = 待销毁 + 已销毁）===' AS section;
SELECT
  (SELECT COALESCE(SUM(amount),0) FROM wallet_tx WHERE type = 10) AS fee_charged,
  (SELECT COALESCE(SUM(amount),0) FROM fee_burn WHERE status IN (0,2,3)) AS fee_pending,
  (SELECT COALESCE(SUM(amount),0) FROM fee_burn WHERE status = 1) AS fee_burned;

SELECT '=== 提现流水出现 local- 前缀即为未广播成功（需人工核查）===' AS section;
SELECT id, symbol, amount, LEFT(hash, 70) AS tx_hash
FROM wallet_tx WHERE hash LIKE 'local-%' OR hash LIKE 'DEMO-%' ORDER BY id;
