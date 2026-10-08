/**
 * 打印本地私链账户（hardhat node 内置公开测试账户）
 *
 * 用途：后端 etc.privchain.yml 需要
 *   - 手续费池/热钱包地址（Withdraw.HotWalletPrivate / Burn.EvmPrivate 对应地址）
 *   - 用户的 EVM 测试地址（签名登录用，各账户私钥由前端钱包或脚本持有）
 *
 * ⚠ 这些私钥是 hardhat 公开助记词的派生结果，**全网公开**，
 *   只能用于本地私链；任何有价值的网络使用它们等于送钱。
 *
 * 用法：
 *   npm run node:local                       # 先起节点（另开终端）
 *   npx hardhat run scripts/print-accounts.js --network localhost
 */
const hre = require("hardhat");

async function main() {
  const signers = await hre.ethers.getSigners();
  const net = await hre.ethers.provider.getNetwork();

  console.log("=".repeat(78));
  console.log(`network=${hre.network.name}  chainId=${net.chainId}  账户数=${signers.length}`);
  console.log("=".repeat(78));
  console.log("idx | address                                    | ETH        | TM");
  console.log("-".repeat(78));

  for (let i = 0; i < signers.length; i++) {
    const s = signers[i];
    const bal = await hre.ethers.provider.getBalance(s.address);
    console.log(
      `${String(i).padStart(3)} | ${s.address} | ${hre.ethers.formatEther(bal).padStart(10)} | -`
    );
  }

  console.log("-".repeat(78));
  console.log("私钥（仅本地私链可用）：");
  // hardhat node 的默认助记词，派生路径 m/44'/60'/0'/0/<i>
  const mnemonic = "test test test test test test test test test test test junk";
  for (let i = 0; i < Math.min(signers.length, 5); i++) {
    const w = hre.ethers.HDNodeWallet
      ? hre.ethers.HDNodeWallet.fromPhrase(mnemonic, undefined, `m/44'/60'/0'/0/${i}`)
      : hre.ethers.Wallet.fromPhrase(mnemonic, `m/44'/60'/0'/0/${i}`);
    console.log(`  [${i}] ${w.address}  ${w.privateKey}`);
  }
  console.log("-".repeat(78));
  console.log("⚠ 以上私钥来自公开助记词，绝不可用于主网/测试网等任何有价值的网络。");
  console.log("=".repeat(78));
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
