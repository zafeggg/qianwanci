/**
 * 私链准备脚本（hardhat node 持久私链，chainId 31337）
 *
 * 做什么（幂等，可重复执行）：
 *   1. 部署 TMToken（若 deployments/localhost.json 已有记录则复用，不重复部署）
 *   2. 首次初始发行：把 TM 铸给**手续费池 = 后端热钱包地址**（mock 热点账户 A）
 *      —— 出金从该地址转出、销毁也从该地址 burn，所以它必须同时持有 TM 与 gas
 *   3. 用 hardhat_setBalance 给同一地址充足 gas（私链专用 RPC，主网不存在）
 *   4. 打印后端配置片段（etc.privchain.yml 需要的 4 个值）
 *
 * 用法：
 *   # 先起节点（另开一个终端）
 *   npm run node:local
 *   # 再跑本脚本
 *   npm run prepare:localnode
 *
 * ⚠ 为什么 package.json 里用 `node --require ./node_modules/hardhat/register` 而不是 `hardhat run`：
 *   `hardhat run` 内部用 child_process.fork 起子进程（需要 IPC 命名管道），
 *   在 Windows 受限沙箱下会直接 `Error HH602: spawn EPERM`。
 *   直接 require hardhat 的 register 可绕开 fork，输出与 `hardhat run` 等价。
 *   不在沙箱里跑时，`npx hardhat run scripts/prepare-local.js --network localhost` 同样可用。
 *
 * 可选环境变量：
 *   POOL_ADDRESS      手续费池/热钱包地址，默认取 hardhat 账户[0]
 *   MINT_COINS        初始发行量（枚），默认 1000000
 *   GAS_ETH           给热钱包补的 gas（ETH），默认 100
 *   FORCE_DEPLOY=1    忽略已有部署记录，强制重新部署
 *
 * 私钥纪律：本脚本只使用 hardhat node 内置的公开测试助记词账户，
 * **不要**把主网/生产私钥接到这个脚本上。
 */
const hre = require("hardhat");
const fs = require("fs");
const path = require("path");

const DEPLOY_DIR = path.join(__dirname, "..", "deployments");
const ABI_DIR = path.join(DEPLOY_DIR, "abi");
const LOCAL_RPC = process.env.LOCAL_RPC_URL || "http://127.0.0.1:8545";

function readRecord() {
  const file = path.join(DEPLOY_DIR, "localhost.json");
  if (!fs.existsSync(file)) return { file, data: null };
  try {
    return { file, data: JSON.parse(fs.readFileSync(file, "utf8")) };
  } catch (e) {
    return { file, data: null };
  }
}

async function main() {
  const network = hre.network.name;
  if (network !== "localhost" && network !== "hardhat") {
    throw new Error(
      `本脚本只允许跑在本地私链上（localhost / hardhat），当前 network = ${network}。` +
        `请使用：npm run prepare:localnode`
    );
  }

  const signers = await hre.ethers.getSigners();
  const [deployer] = signers;
  const poolAddress = process.env.POOL_ADDRESS || signers[0].address;
  const mintCoins = process.env.MINT_COINS || "1000000";
  const gasEth = process.env.GAS_ETH || "100";
  const forceDeploy = process.env.FORCE_DEPLOY === "1";

  // 连的是不是真的在跑？
  const chainId = Number((await hre.ethers.provider.getNetwork()).chainId);
  const head = await hre.ethers.provider.getBlockNumber();

  console.log("=".repeat(64));
  console.log("千万次 私链准备");
  console.log("  RPC       ", LOCAL_RPC);
  console.log("  network   ", network, " chainId", chainId, " 当前块高", head);
  console.log("  部署者    ", deployer.address);
  console.log("  手续费池  ", poolAddress, "(= 后端 Withdraw.HotWalletPrivate 对应地址)");
  console.log("=".repeat(64));

  // ---------- 1. 部署（或复用）----------
  const { file: recordFile, data: existing } = readRecord();
  let tokenAddress = existing && existing.contracts && existing.contracts.TMToken
    ? existing.contracts.TMToken.address
    : "";

  const reuse = tokenAddress && !forceDeploy;
  if (reuse) {
    const code = await hre.ethers.provider.getCode(tokenAddress);
    if (code === "0x") {
      console.log(`[warn] 记录里的合约 ${tokenAddress} 在当前链上没有代码（节点重启过？），改为重新部署`);
      tokenAddress = "";
    }
  }

  if (!tokenAddress) {
    const maxSupply = hre.ethers.parseUnits(process.env.MAX_SUPPLY_COINS || "10000000000", 8);
    console.log("[1/4] 部署 TMToken ...");
    const Factory = await hre.ethers.getContractFactory("TMToken");
    const token = await Factory.deploy(deployer.address, maxSupply);
    await token.waitForDeployment();
    tokenAddress = await token.getAddress();
    const tx = token.deploymentTransaction();
    console.log("      合约地址", tokenAddress, " tx", tx.hash);

    fs.mkdirSync(ABI_DIR, { recursive: true });
    const artifact = await hre.artifacts.readArtifact("TMToken");
    fs.writeFileSync(path.join(ABI_DIR, "TMToken.json"), JSON.stringify(artifact.abi, null, 2));

    const merged = existing || {};
    merged.chainId = chainId;
    merged.network = network;
    merged.updatedAt = new Date().toISOString();
    merged.rpcUrl = LOCAL_RPC;
    merged.contracts = Object.assign({}, merged.contracts, {
      TMToken: {
        address: tokenAddress,
        chainId,
        network,
        deployer: deployer.address,
        owner: deployer.address,
        maxSupplyCoins: process.env.MAX_SUPPLY_COINS || "10000000000",
        decimals: 8,
        txHash: tx.hash,
        deployedAt: new Date().toISOString(),
        note: "本地私链部署（hardhat node，chainId 31337）。节点重启后链上状态清空，需重新部署。",
      },
    });
    fs.writeFileSync(recordFile, JSON.stringify(merged, null, 2));
    console.log("      部署记录已写入", recordFile);
  } else {
    console.log("[1/4] 复用已有部署", tokenAddress, "（FORCE_DEPLOY=1 可强制重部署）");
  }

  const token = await hre.ethers.getContractAt("TMToken", tokenAddress, deployer);

  // ---------- 2. 初始发行（合约只允许一次）----------
  console.log("[2/4] 初始发行 ...");
  const alreadyMinted = await token.initialSupplyMinted();
  if (alreadyMinted) {
    console.log("      已发行过，跳过。当前流通量", hre.ethers.formatUnits(await token.totalSupply(), 8), "TM");
  } else {
    const amount = hre.ethers.parseUnits(mintCoins, 8);
    const tx = await token.mintInitial(poolAddress, amount);
    await tx.wait();
    console.log(`      已铸 ${mintCoins} TM → ${poolAddress}  tx ${tx.hash}`);
  }

  // ---------- 3. gas ----------
  console.log("[3/4] 补充 gas ...");
  const want = hre.ethers.parseEther(gasEth);
  const have = await hre.ethers.provider.getBalance(poolAddress);
  if (have < want && hre.network.name === "localhost") {
    // hardhat node 专有 RPC：直接改余额（主网不存在该接口）
    await hre.network.provider.send("hardhat_setBalance", [
      poolAddress,
      "0x" + want.toString(16),
    ]);
    console.log(`      gas 由 ${hre.ethers.formatEther(have)} 补到 ${gasEth} ETH`);
  } else {
    console.log("      gas 充足或非 localhost 网络，跳过补充");
  }

  // ---------- 4. 输出后端配置片段 ----------
  const tmBalance = await token.balanceOf(poolAddress);
  const supply = await token.totalSupply();
  const remaining = await token.remainingBurnable();

  console.log("[4/4] 后端配置（houduan/TiMi/config/etc.privchain.yml）");
  console.log("-".repeat(64));
  console.log(`Chain:`);
  console.log(`  FiboRpcUrl: "${LOCAL_RPC}"`);
  console.log(`  FiboChainId: ${chainId}`);
  console.log(`  TmContract: "${tokenAddress}"`);
  console.log(`  Confirmations: 1          # 私链必须调小，否则监听要等 12 个块`);
  console.log(`Withdraw:`);
  console.log(`  Mode: "broadcast"`);
  console.log(`  TokenContract: "${tokenAddress}"`);
  console.log(`Burn:`);
  console.log(`  Chain: "evm"`);
  console.log(`  EvmPrivate: "<池私钥>"     # 见下方提示，脚本不落盘`);
  console.log("-".repeat(64));
  console.log("链上状态");
  console.log("  手续费池地址", poolAddress);
  console.log("  池 TM 余额  ", hre.ethers.formatUnits(tmBalance, 8));
  console.log("  总流通量    ", hre.ethers.formatUnits(supply, 8));
  console.log("  可销毁余量  ", hre.ethers.formatUnits(remaining, 8), "(下限 7777 枚由合约强制)");
  console.log("  部署记录    ", recordFile);
  console.log("  ABI         ", path.join(ABI_DIR, "TMToken.json"));
  console.log("=".repeat(64));
  console.log("提示：热钱包/池私钥请从 `npx hardhat node` 启动日志里抄（每行末尾括号内那条），");
  console.log("     或运行 `npx hardhat run scripts/print-accounts.js --network localhost`。");
  console.log("     该私钥仅用于本地私链，绝不要用于任何有价值的网络。");
  console.log("=".repeat(64));
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
