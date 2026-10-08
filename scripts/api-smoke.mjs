/**
 * 千万次后端联调验证脚本（C 方案）
 * 实现 npower 原生协议：RSA 公钥加密 AES passphrase -> Sign 头
 *   body = OpenSSL 格式 AES-256-CBC(Salted__ + salt) 加密 JSON
 *   Authorization = Bearer <HS256 JWT>（模拟已登录态，token 预置 Redis）
 * 验证：签名链路打通 + 后端查询接口返回真实 DB 数据
 */
import crypto from 'node:crypto';
import fs from 'node:fs';

const BASE = 'http://127.0.0.1:3000';
const PUB = fs.readFileSync(new URL('./server_pkix.pem', import.meta.url));
const SECRET = 'dsajflkadsjglafsdj'; // config/etc.yml SecretKey

/* ---------- 协议原语 ---------- */

// 随机 passphrase（RSA 加密传输 + AES 派生）
const passphrase = crypto.randomBytes(16).toString('hex');

// Sign 头：RSA PKCS1v15 加密 passphrase 后 base64
function signHeader() {
  const enc = crypto.publicEncrypt({ key: PUB, padding: crypto.constants.RSA_PKCS1_PADDING }, Buffer.from(passphrase));
  return enc.toString('base64');
}

// OpenSSL 格式 AES-256-CBC：Salted__ + salt + cipher，EVP_BytesToKey(SHA256, 1 轮)
function opensslEncrypt(plain) {
  const salt = crypto.randomBytes(8);
  let d1 = crypto.createHash('sha256').update(Buffer.concat([Buffer.from(passphrase), salt])).digest();
  const d2 = crypto.createHash('sha256').update(Buffer.concat([d1, Buffer.from(passphrase), salt])).digest();
  const key = d1;                          // 32B key（D1 全量）
  const iv = d2.subarray(0, 16);           // 16B iv（D2 前 16B）
  const cipher = crypto.createCipheriv('aes-256-cbc', key, iv);
  const enc = Buffer.concat([cipher.update(Buffer.from(plain, 'utf8')), cipher.final()]);
  const full = Buffer.concat([Buffer.from('Salted__'), salt, enc]);
  return full.toString('base64'); // go-openssl DecryptBytes 期望 base64 密文
}

// HS256 JWT（claims 需含 walletId，SecretKey 与后端一致）
function makeJwt(walletId) {
  const b64u = (buf) => Buffer.from(buf).toString('base64url');
  const header = b64u(JSON.stringify({ alg: 'HS256', typ: 'JWT' }));
  const now = Math.floor(Date.now() / 1000);
  const payload = b64u(JSON.stringify({ walletId, name: 'tester', exp: now + 7200 }));
  const sig = crypto.createHmac('sha256', SECRET).update(`${header}.${payload}`).digest('base64url');
  return `${header}.${payload}.${sig}`;
}

/* ---------- HTTP 封装 ---------- */

async function call(path, { method = 'GET', body, token, raw = false } = {}) {
  const headers = { Sign: signHeader() };
  if (token) headers.Authorization = `Bearer ${token}`;
  let payload;
  if (body !== undefined) {
    headers['Content-Type'] = 'application/octet-stream';
    payload = opensslEncrypt(JSON.stringify(body));
  }
  const res = await fetch(BASE + path, { method, headers, body: payload });
  const text = await res.text();
  if (!text) return { http: res.status, text: '' };
  try { return { http: res.status, json: JSON.parse(text) }; }
  catch { return { http: res.status, text }; }
}

/* ---------- 验证流程 ---------- */

const walletId = Number(process.argv[2] || 1);
// token 优先取参数（shell 已预置 Redis 同值），否则脚本自签（需外部同步预置）
const token = process.argv[3] || makeJwt(walletId);
console.log('walletId =', walletId);

const run = async () => {
  // 1. 无签名请求应被拒（基线）
  const noSign = await fetch(BASE + '/home/version').then((r) => r.text()).catch(() => 'net-error');
  console.log('[1] 无 Sign 头被拒:', noSign.slice(0, 80));

  // 2. 带签名 + 预置 token GET /home/ 轮次列表（真实 DB 数据）
  const home = await call('/home/', { token });
  console.log('[2] GET /home/ ->', JSON.stringify(home.json || home.text).slice(0, 300));

  // 3. GET /wallet/info 钱包资产（真实 DB 数据）
  const info = await call('/wallet/info', { token });
  console.log('[3] GET /wallet/info ->', JSON.stringify(info.json || info.text).slice(0, 300));

  // 4. GET /self/ 我的信息
  const self = await call('/self/', { token });
  console.log('[4] GET /self/ ->', JSON.stringify(self.json || self.text).slice(0, 300));

  // 5. POST /ops 风格 AES body 链路验证：用 POST /wallet/withdraw 形式请求应返回业务层错误而非解密错误
  const wd = await call('/wallet/modifyPwd', { method: 'POST', token, body: { oldPwd: 'x', newPwd: 'y' } });
  console.log('[5] POST AES body 链路 ->', JSON.stringify(wd.json || wd.text).slice(0, 200));
};

run().catch((e) => console.error('FAIL', e));
