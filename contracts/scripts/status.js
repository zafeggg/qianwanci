/**
 * TMToken 只读状态查询（运维与对账用，无写操作）
 *
 * 用法
 *   npx hardhat run scripts/status.js --network fibo
 *   或 CONTRACT_ADDRESS=0x... npx hardhat run scripts/status.js --network fibo
 */
const hre = require("hardhat");
const fs = require("fs");
const path = require("path");

async function main() {
  const network = hre.network.name;
  let address = process.env.CONTRACT_ADDRESS;
  if (!address) {
    const file = path.join(__dirname, "..", "deployments", `${network}.json`);
    if (!fs.existsSync(file)) {
      throw new Error(`未找到部署记录 ${file}`);
    }
    address = JSON.parse(fs.readFileSync(file, "utf8")).contracts.TMToken.address;
  }

  const token = await hre.ethers.getContractAt("TMToken", address);
  const fmt = (v) => hre.ethers.formatUnits(v, 8);

  const [supply, burned, remaining, reached, minted, owner] = await Promise.all([
    token.circulatingSupply(),
    token.getTotalBurned(),
    token.remainingBurnable(),
    token.burnTargetReached(),
    token.initialSupplyMinted(),
    token.owner(),
  ]);

  console.log("TMToken 状态");
  console.log("  网络        ", network);
  console.log("  合约        ", address);
  console.log("  owner       ", owner);
  console.log("  已初始发行  ", minted ? "是" : "否（总量待定稿）");
  console.log("  流通量      ", fmt(supply), "枚");
  console.log("  累计销毁    ", fmt(burned), "枚");
  console.log("  可销毁余量  ", fmt(remaining), "枚");
  console.log("  销毁下限    ", fmt(await token.MIN_CIRCULATING()), "枚");
  console.log("  是否达终点  ", reached ? "是" : "否");
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
