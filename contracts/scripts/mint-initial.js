/**
 * TMToken 一次性初始发行脚本（总量定稿后执行）
 *
 * 用法
 *   export PRIVATE_KEY=0x...
 *   MINT_COINS=1000000 POOL_ADDRESS=0x... npx hardhat run scripts/mint-initial.js --network fibo
 *
 * 说明
 *   合约只允许调用一次 mintInitial。执行前请确认总量已与运营、资金模型核对完毕
 */
const hre = require("hardhat");
const fs = require("fs");
const path = require("path");

async function main() {
  const network = hre.network.name;
  const [signer] = await hre.ethers.getSigners();

  let address = process.env.CONTRACT_ADDRESS;
  if (!address) {
    const file = path.join(__dirname, "..", "deployments", `${network}.json`);
    if (!fs.existsSync(file)) {
      throw new Error(`未找到部署记录 ${file}，请先部署或显式传入 CONTRACT_ADDRESS`);
    }
    address = JSON.parse(fs.readFileSync(file, "utf8")).contracts.TMToken.address;
  }

  const mintCoins = process.env.MINT_COINS;
  const poolAddress = process.env.POOL_ADDRESS;
  if (!mintCoins || !poolAddress) {
    throw new Error("缺少参数：MINT_COINS（枚）与 POOL_ADDRESS（接收地址）");
  }
  const amount = hre.ethers.parseUnits(mintCoins, 8);

  const token = await hre.ethers.getContractAt("TMToken", address, signer);

  console.log("=".repeat(60));
  console.log("TMToken 初始发行");
  console.log("  网络    ", network);
  console.log("  合约    ", address);
  console.log("  调用者  ", signer.address);
  console.log("  接收方  ", poolAddress);
  console.log("  发行量  ", mintCoins, "枚");
  console.log("  待发行  ", await token.initialSupplyMinted() ? "已完成，本次将失败" : "未发行");
  console.log("  硬顶    ", hre.ethers.formatUnits(await token.MAX_SUPPLY(), 8), "枚");
  console.log("=".repeat(60));

  const tx = await token.mintInitial(poolAddress, amount);
  console.log("交易  ", tx.hash);
  await tx.wait();

  console.log("流通量", hre.ethers.formatUnits(await token.totalSupply(), 8), "枚");
  console.log("销毁下限", hre.ethers.formatUnits(await token.MIN_CIRCULATING(), 8), "枚");
  console.log("可销毁余量", hre.ethers.formatUnits(await token.remainingBurnable(), 8), "枚");
  console.log("=".repeat(60));
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
