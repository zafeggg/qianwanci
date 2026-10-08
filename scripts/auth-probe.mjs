// /api 鉴权三路径验证（key 从环境变量读，避免命令行字面值被会话改写）
const BASE = process.env.TBASE || 'http://127.0.0.1:3002';
const REAL = process.env.TKEY || '';

async function probe(label, key) {
  const headers = key === null ? {} : { 'X-API-Key': key };
  const res = await fetch(BASE + '/api/prices', { headers });
  const text = await res.text();
  console.log(label, '->', text.slice(0, 80));
}
await probe('1 无key   ', null);
await probe('2 错key   ', REAL + 'x');
await probe('3 对key   ', REAL);
