/**
 * 私链测试用户「钱包签名登录」辅助（供 privchain-e2e.ps1 调用）
 * 输出一行 JSON：{code, address, walletId, token, inviteCode, registered}
 *
 * 走的是真实接口 GET /api/auth/nonce + POST /api/auth/login（EIP-191 验签），
 * 首登即注册 —— 这条路径不依赖 KTO/TRON，是私链环境唯一可用的注册入口。
 *
 * 环境变量：API_BASE（默认 http://127.0.0.1:3000）、API_KEY（默认 privchain-apikey）、
 *           TEST_PRIVATE_KEY（默认 hardhat 账户[1]）
 */
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const require = createRequire(path.join(__dirname, "..", "contracts", "package.json"));
const { ethers } = require("ethers");

const API_BASE = process.env.API_BASE || "http://127.0.0.1:3000";
const API_KEY = process.env.API_KEY || "privchain-apikey";
const PK =
  process.env.TEST_PRIVATE_KEY ||
  "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"; // hardhat 账户[1]

async function main() {
  const wallet = new ethers.Wallet(PK);
  const headers = { "X-API-Key": API_KEY, "Content-Type": "application/json" };

  const nonceRes = await (
    await fetch(`${API_BASE}/api/auth/nonce?address=${wallet.address}`, { headers })
  ).json();
  if (nonceRes.code !== 0) {
    console.log(JSON.stringify({ code: nonceRes.code, message: nonceRes.message }));
    process.exitCode = 1;
    return;
  }

  const signature = await wallet.signMessage(nonceRes.data.message);
  const loginRes = await (
    await fetch(`${API_BASE}/api/auth/login`, {
      method: "POST",
      headers,
      body: JSON.stringify({ address: wallet.address, signature, name: "privchain-user" }),
    })
  ).json();

  if (loginRes.code !== 0) {
    console.log(JSON.stringify({ code: loginRes.code, message: loginRes.message }));
    process.exitCode = 1;
    return;
  }

  console.log(
    JSON.stringify({
      code: 0,
      address: loginRes.data.address,
      walletId: loginRes.data.walletId,
      token: loginRes.data.token,
      inviteCode: loginRes.data.inviteCode,
      registered: loginRes.data.registered,
    })
  );
}

main().catch((e) => {
  console.log(JSON.stringify({ code: -1, message: String(e) }));
  process.exitCode = 1;
});
