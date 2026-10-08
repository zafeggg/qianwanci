/**
 * TMToken 部署脚本（FIBONACCI 链 12306）
 *
 * 用法
 *   export PRIVATE_KEY=0x...            # 部署私钥（不落盘）
 *   export OWNER_ADDRESS=0x...          # 可选，owner，默认取部署者
 *   export MAX_SUPPLY_COINS=10000000000 # 可选，发行硬顶（枚），默认 100 亿
 *   npx hardhat run scripts/deploy-fibo.js --network fibo
 *
 * 说明
 *   部署时不铸造。初始发行量待业务定稿后，由 owner 调用 mintInitial 一次性设定
 */
const hre = require("hardhat");
const fs = require("fs");
const path = require("path");

const DEPLOY_DIR = path.join(__dirname, "..", "deployments");
const ABI_DIR = path.join(DEPLOY_DIR, "abi");

async function main() {
  const network = hre.network.name;
  const [deployer] = await hre.ethers.getSigners();

  const ownerAddress = process.env.OWNER_ADDRESS || deployer.address;
  const maxSupplyCoins = process.env.MAX_SUPPLY_COINS || "10000000000"; // 100 亿枚
  const maxSupply = hre.ethers.parseUnits(maxSupplyCoins, 8);

  console.log("=".repeat(60));
  console.log("TMToken 部署");
  console.log("  网络      ", network);
  console.log("  部署者    ", deployer.address);
  console.log("  owner     ", ownerAddress);
  console.log("  发行硬顶  ", maxSupplyCoins, "枚");
  console.log("  精度      8 位");
  console.log("=".repeat(60));

  const Factory = await hre.ethers.getContractFactory("TMToken");
  const token = await Factory.deploy(ownerAddress, maxSupply);
  await token.waitForDeployment();

  const address = await token.getAddress();
  const tx = token.deploymentTransaction();

  console.log("部署交易  ", tx.hash);
  console.log("合约地址  ", address);

  const chainId = (await hre.ethers.provider.getNetwork()).chainId;
  const record = {
    TMToken: {
      address,
      chainId: Number(chainId),
      network,
      deployer: deployer.address,
      owner: ownerAddress,
      constructorArgs: [ownerAddress, maxSupply.toString()],
      maxSupplyCoins,
      decimals: 8,
      minCirculating: "777700000000",
      txHash: tx.hash,
      deployedAt: new Date().toISOString(),
      note: "初始发行量未设定，待业务定稿后调用 mintInitial",
    },
  };

  fs.mkdirSync(ABI_DIR, { recursive: true });
  const file = path.join(DEPLOY_DIR, `${network}.json`);
  let merged = {};
  if (fs.existsSync(file)) {
    merged = JSON.parse(fs.readFileSync(file, "utf8"));
  }
  merged.chainId = Number(chainId);
  merged.network = network;
  merged.updatedAt = new Date().toISOString();
  merged.contracts = Object.assign({}, merged.contracts, record);
  fs.writeFileSync(file, JSON.stringify(merged, null, 2));

  const artifact = await hre.artifacts.readArtifact("TMToken");
  fs.writeFileSync(path.join(ABI_DIR, "TMToken.json"), JSON.stringify(artifact.abi, null, 2));

  console.log("-".repeat(60));
  console.log("记录已写入", file);
  console.log("ABI  已写入", path.join(ABI_DIR, "TMToken.json"));
  console.log("");
  console.log("下一步（总量定稿后执行）");
  console.log(
    `  PRIVATE_KEY=0x... MINT_COINS=1000000 POOL_ADDRESS=0x... npx hardhat run scripts/mint-initial.js --network ${network}`
  );
  console.log("=".repeat(60));
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
