/**
 * 私链链上辅助操作（供 privchain-e2e.ps1 调用，避免在 PowerShell 里拼 JS）
 *
 * 用法：node privchain-chain.mjs <子命令>
 *   参数从环境变量 PC_ARGS（JSON 对象）读取，例如：
 *     PC_ARGS={"to":"0x..","coins":"5","pk":"0x.."}  node privchain-chain.mjs send-token
 *
 *   子命令：
 *     pool-balance                 充值池 TM 余额（最小单位）
 *     balance    {address}         指定地址 TM 余额（最小单位）
 *     total-supply                 链上总供应量（最小单位）
 *     norm       {address}         地址归一化为小写（与 Redis 进度键口径一致）
 *     fund-pool  {coins}           从手续费池给充值池补 TM
 *     send-token {to,pk,coins}     从指定私钥地址转 TM 给 to（模拟用户充值）
 *     deposit    {to,coins}        从充值池转 TM 给 to
 *
 * 为什么参数走 JSON 环境变量而不是命令行：Windows PowerShell 把数组传给原生程序时形状不可控
 * （参数被吞掉 → node 收到 undefined → 静默失败），JSON 最稳。
 *
 * 输出：成功时只打印一行结果；失败打印 CHAIN_ERR 并以非 0 退出。
 * 全部操作都在本地私链上，账户为 hardhat 内置公开测试账户。
 */
import { createRequire } from "node:module";
import path from "node:path";
import fs from "node:fs";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(__dirname, "..");
const require = createRequire(path.join(ROOT, "contracts", "package.json"));
const { ethers } = require("ethers");

let A = {};
try {
  A = process.env.PC_ARGS ? JSON.parse(process.env.PC_ARGS) : {};
} catch (e) {
  console.error("CHAIN_ERR: PC_ARGS 不是合法 JSON: " + e.message);
  process.exit(1);
}

const RPC = process.env.PC_RPC || "http://127.0.0.1:8545";
// hardhat node 内置公开助记词账户（全网公开，仅限本地私链）
const HOT_PK =
  process.env.PC_HOT_KEY ||
  "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"; // 账户[1] 出金热钱包
const POOL_PK =
  process.env.PC_POOL_KEY ||
  "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"; // 账户[0] 充值池

const deploy = JSON.parse(
  fs.readFileSync(path.join(ROOT, "contracts", "deployments", "localhost.json"), "utf8")
);
const abi = JSON.parse(
  fs.readFileSync(path.join(ROOT, "contracts", "deployments", "abi", "TMToken.json"), "utf8")
);
const token = process.env.PC_TOKEN || deploy.contracts.TMToken.address;

const provider = new ethers.JsonRpcProvider(RPC);
const hot = new ethers.Wallet(HOT_PK, provider);
const pool = new ethers.Wallet(POOL_PK, provider);
const tokenHot = new ethers.Contract(token, abi, hot);
const tokenPool = new ethers.Contract(token, abi, pool);

/**
 * 发送一笔交易。
 * ⚠ nonce 必须由本地显式递增：hardhat automine 下 getTransactionCount("pending") 可能滞后，
 * 连续两次调用会拿到同一个 nonce（实测报 Nonce too low, Expected 8 but got 7）。
 * 这里用「读取一次 + 进程内递增」并显式写进 tx，避免依赖节点的 pending 视图。
 *
 * ⚠ 2026-09-30 修复：缓存必须**按地址分开**。原实现只有一个 `nonceCache` 变量，被 hot/pool
 * 两个签名者共用 —— send-token（pool 签名）之后紧接着 bump()（hot 签名）会拿到 pool 的 nonce，
 * 报 `Nonce too high. Expected nonce to be 4 but got 18`（实测），补块交易因此**从未真正落链**：
 * Confirmations=1 下充值块永远进不了安全区间，表现就是"充值了但余额没变/隔一次才入账"。
 */
const nonceCache = new Map(); // address(lowercase) -> 下一个可用 nonce
async function nextNonce(signer) {
  const key = signer.address.toLowerCase();
  const chain = await provider.getTransactionCount(signer.address, "pending");
  const prev = nonceCache.get(key);
  const nonce = prev === undefined ? chain : Math.max(prev + 1, chain);
  nonceCache.set(key, nonce);
  return nonce;
}

async function send(contract, signer, fn, args) {
  const nonce = await nextNonce(signer);
  const tx = await contract[fn](...args, { nonce });
  await tx.wait();
  return tx.hash;
}

/** 补一个空块（自转 0），让 Confirmations 计数推进 */
async function bump() {
  const nonce = await nextNonce(hot);
  const tx = await hot.sendTransaction({ to: hot.address, value: 0n, nonce });
  await tx.wait();
}

async function main() {
  const cmd = process.argv[2];
  switch (cmd) {
    case "pool-balance":
      console.log((await tokenHot.balanceOf(pool.address)).toString());
      return;

    case "balance": {
      if (!A.address) throw new Error("缺少 address");
      console.log((await tokenHot.balanceOf(A.address)).toString());
      return;
    }

    case "total-supply":
      console.log((await tokenHot.totalSupply()).toString());
      return;

    case "norm": {
      if (!A.address) throw new Error("缺少 address");
      // 归一化成小写（与 evm.NormalizeAddress / Redis 进度键口径一致）
      console.log(ethers.getAddress(A.address).toLowerCase());
      return;
    }

    case "fund-pool": {
      const coins = A.coins || "200";
      const hash = await send(tokenHot, hot, "transfer", [pool.address, ethers.parseUnits(coins, 8)]);
      await bump();
      console.log(hash);
      return;
    }

    case "send-token": {
      // 从指定私钥地址转 TM 给 to —— 模拟「用户 → 充值池」的真实充值方向。
      // ⚠ 充值必须 to == WatchPool；反向（池 → 用户）会被 from==pool 过滤掉，永远不入账。
      const { to, pk, coins = "5" } = A;
      if (!to || !pk) throw new Error("缺少 to 或 pk");
      const signer = new ethers.Wallet(pk, provider);
      const c = new ethers.Contract(token, abi, signer);
      const hash = await send(c, signer, "transfer", [to, ethers.parseUnits(coins, 8)]);
      // 补块：Confirmations=1 要求充值交易之后再出一个块，监听才会把它纳入扫描区间
      await bump();
      await bump();
      console.log(hash);
      return;
    }

    case "deposit": {
      const { to, coins = "5" } = A;
      if (!to) throw new Error("缺少 to");
      const hash = await send(tokenPool, pool, "transfer", [to, ethers.parseUnits(coins, 8)]);
      await bump();
      console.log(hash);
      return;
    }

    default:
      throw new Error(`未知子命令：${cmd}`);
  }
}

main().catch((e) => {
  console.error("CHAIN_ERR: " + e.message);
  process.exit(1);
});
