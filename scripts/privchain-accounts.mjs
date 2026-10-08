/**
 * 私链测试账号工具：批量创建确定性测试账号（地址 + 私钥），供多账号验证脚本使用。
 *
 * 用法：
 *   PC_ARGS={"n":30,"seed":"multiteam"}  node scripts/privchain-accounts.mjs
 * 输出：JSON 数组 [{index,address,privateKey}]，地址小写。
 *
 * 为什么不用 hardhat 内置账户：内置只有 20 个且地址是公开助记词派生的，
 * 多账号场景（直推 10 / 伞下 30）需要更多账号，且需要确定性（脚本可重复跑、断言可复现）。
 * 生成方式：sha256(seed + index) 作为私钥（仅本地私链使用，无任何真实价值）。
 */
import { createRequire } from "node:module";
import path from "node:path";
import fs from "node:fs";
import crypto from "node:crypto";
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

const n = Number(A.n || 30);
const seed = String(A.seed || "multiteam");
const outFile = A.out || "";

const list = [];
for (let i = 0; i < n; i++) {
  const pk = "0x" + crypto.createHash("sha256").update(`${seed}#${i}`).digest("hex");
  const w = new ethers.Wallet(pk);
  list.push({ index: i, address: w.address.toLowerCase(), privateKey: pk });
}

const json = JSON.stringify(list);
if (outFile) {
  fs.writeFileSync(path.resolve(ROOT, outFile), json, "utf8");
  console.log(path.resolve(ROOT, outFile));
} else {
  console.log(json);
}
