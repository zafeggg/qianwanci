/**
 * 千万次（TiMi）合约工程 - Hardhat 配置
 * 链：FIBONACCI（chainId 12306，RPC https://network.hzroc.art）
 *
 * 隔离声明：本工程与 GYT / ZYT 项目完全独立，仅共享 FIBO 链网络参数。
 * 私钥纪律：PRIVATE_KEY 通过环境变量注入，不落盘 .env，不入库。
 *   export PRIVATE_KEY=0x...  然后 npm run deploy:fibo
 */
require("@nomicfoundation/hardhat-toolbox");
require("dotenv").config();

const accounts = process.env.PRIVATE_KEY ? [process.env.PRIVATE_KEY] : [];

module.exports = {
  solidity: {
    version: "0.8.24",
    settings: {
      optimizer: {
        enabled: true,
        runs: 200,
      },
      // FIBO 链沿用 paris，避免 PUSH0 兼容问题（与 GYT 同口径）
      evmVersion: "paris",
    },
  },

  networks: {
    // FIBO 主网
    fibo: {
      url: process.env.FIBO_RPC_URL || "https://network.hzroc.art",
      chainId: 12306,
      accounts,
      gasPrice: "auto",
      type: 0, // 主网为 legacy 交易
    },

    // FIBO 测试网
    fiboTestnet: {
      url: process.env.FIBO_TESTNET_RPC_URL || "https://testnet.fibo.io",
      chainId: 12306,
      accounts,
      gasPrice: "auto",
      type: 0,
    },

    // 本地开发网络（hardhat 内存链，进程退出即丢；只能用于单测，后端连不上）
    hardhat: {
      chainId: 31337,
    },

    // 本地持久私链节点：`npx hardhat node` 起在 127.0.0.1:8545，chainId 31337。
    // 后端私链联调连的就是这个网络（见 houduan/TiMi/config/etc.privchain.yml）。
    // 注意：必须用 --network localhost 才能把合约部署到**正在运行的节点**上，
    // 用 --network hardhat 只会部署到 hardhat 进程内的临时链，部署完即消失。
    localhost: {
      url: process.env.LOCAL_RPC_URL || "http://127.0.0.1:8545",
      chainId: 31337,
      // 私链沿用 legacy 交易（type 0），与后端 utils/evm.SendERC20 的签名口径一致
      type: 0,
      gasPrice: "auto",
    },
  },

  paths: {
    sources: "./contracts",
    tests: "./test",
    cache: "./cache",
    artifacts: "./artifacts",
  },

  mocha: {
    timeout: 40000,
  },
};
