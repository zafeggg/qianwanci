const { expect } = require("chai");
const { ethers } = require("hardhat");

/**
 * TMToken 单元测试
 * 覆盖：元数据 / 一次性发行 / 7777 销毁下限 / 暂停 / 黑名单 / 权限 / 销毁对账接口
 */
describe("TMToken 千万次手续费代币", function () {
  const u = (n) => ethers.parseUnits(String(n), 8);
  const MAX_SUPPLY = u(10_000_000_000); // 硬顶 100 亿枚（总量待定，先用宽裕上限）
  const FLOOR = u(7777); // 销毁下限 7777 枚

  let token, owner, pool, user, other, attacker;

  beforeEach(async function () {
    [owner, pool, user, other, attacker] = await ethers.getSigners();
    const Factory = await ethers.getContractFactory("TMToken");
    token = await Factory.deploy(owner.address, MAX_SUPPLY);
    await token.waitForDeployment();
  });

  describe("元数据与构造", function () {
    it("名称 TiMi，符号 TM，精度 8 位", async function () {
      expect(await token.name()).to.equal("TiMi");
      expect(await token.symbol()).to.equal("TM");
      expect(await token.decimals()).to.equal(8);
    });

    it("硬顶与销毁下限按构造参数落地", async function () {
      expect(await token.MAX_SUPPLY()).to.equal(MAX_SUPPLY);
      expect(await token.MIN_CIRCULATING()).to.equal(FLOOR);
    });

    it("部署后未铸造，流通量为 0", async function () {
      expect(await token.totalSupply()).to.equal(0n);
      expect(await token.initialSupplyMinted()).to.equal(false);
    });

    it("owner 为零地址应回滚（由 OpenZeppelin Ownable 拦截）", async function () {
      const Factory = await ethers.getContractFactory("TMToken");
      await expect(Factory.deploy(ethers.ZeroAddress, MAX_SUPPLY)).to.be.reverted;
    });

    it("硬顶低于销毁下限应回滚", async function () {
      const Factory = await ethers.getContractFactory("TMToken");
      await expect(Factory.deploy(owner.address, u(100))).to.be.revertedWith(
        "TM: max supply below floor"
      );
    });
  });

  describe("一次性初始发行", function () {
    it("owner 可发行并落账到资金池", async function () {
      await expect(token.mintInitial(pool.address, u(1_000_000)))
        .to.emit(token, "InitialSupplyMinted")
        .withArgs(pool.address, u(1_000_000));

      expect(await token.balanceOf(pool.address)).to.equal(u(1_000_000));
      expect(await token.initialSupplyMinted()).to.equal(true);
    });

    it("重复发行应回滚", async function () {
      await token.mintInitial(pool.address, u(1_000));
      await expect(token.mintInitial(pool.address, u(1))).to.be.revertedWith(
        "TM: initial supply already set"
      );
    });

    it("非 owner 发行应回滚", async function () {
      await expect(
        token.connect(attacker).mintInitial(attacker.address, u(1_000))
      ).to.be.revertedWithCustomError(token, "OwnableUnauthorizedAccount");
    });

    it("超过硬顶应回滚", async function () {
      await expect(
        token.mintInitial(pool.address, MAX_SUPPLY + 1n)
      ).to.be.revertedWith("TM: amount exceeds max supply");
    });

    it("发行到零地址应回滚", async function () {
      await expect(token.mintInitial(ethers.ZeroAddress, u(1_000))).to.be.revertedWith(
        "TM: invalid recipient"
      );
    });
  });

  describe("销毁与 7777 下限", function () {
    beforeEach(async function () {
      await token.mintInitial(pool.address, u(20_000));
    });

    it("资金池销毁自身 TM 正常减量", async function () {
      await expect(token.connect(pool).burn(u(5_000)))
        .to.emit(token, "FeeBurned")
        .withArgs(pool.address, u(5_000), u(15_000));

      expect(await token.balanceOf(pool.address)).to.equal(u(15_000));
      expect(await token.totalSupply()).to.equal(u(15_000));
      expect(await token.getTotalBurned()).to.equal(u(5_000));
      expect(await token.circulatingSupply()).to.equal(u(15_000));
    });

    it("剩余可销毁量随销毁递减", async function () {
      expect(await token.remainingBurnable()).to.equal(u(20_000) - FLOOR);
      await token.connect(pool).burn(u(1_000));
      expect(await token.remainingBurnable()).to.equal(u(19_000) - FLOOR);
    });

    it("销毁触及 7777 下限后拒绝继续销毁", async function () {
      // 流通量 20000，可销毁 12223
      await token.connect(pool).burn(u(12_223));
      expect(await token.totalSupply()).to.equal(FLOOR);
      expect(await token.burnTargetReached()).to.equal(true);
      expect(await token.remainingBurnable()).to.equal(0n);

      await expect(token.connect(pool).burn(1n)).to.be.revertedWith(
        "TM: burn target 7777 reached"
      );
    });

    it("单笔超出可销毁量应回滚，不会击穿下限", async function () {
      await expect(token.connect(pool).burn(u(12_224))).to.be.revertedWith(
        "TM: exceeds burnable, floor 7777 protected"
      );
      expect(await token.totalSupply()).to.equal(u(20_000));
    });

    it("销毁 0 应回滚", async function () {
      await expect(token.connect(pool).burn(0)).to.be.revertedWith("TM: zero amount");
    });

    it("burnFrom 需授权，授权后可销毁他人份额", async function () {
      await expect(
        token.connect(attacker).burnFrom(pool.address, u(100))
      ).to.be.revertedWithCustomError(token, "ERC20InsufficientAllowance");

      await token.connect(pool).approve(attacker.address, u(100));
      await token.connect(attacker).burnFrom(pool.address, u(100));
      expect(await token.totalSupply()).to.equal(u(19_900));
    });

    it("burnFrom 同样受 7777 下限保护", async function () {
      await token.connect(pool).approve(attacker.address, u(20_000));
      await expect(
        token.connect(attacker).burnFrom(pool.address, u(12_224))
      ).to.be.revertedWith("TM: exceeds burnable, floor 7777 protected");
      expect(await token.totalSupply()).to.equal(u(20_000));
    });
  });

  describe("暂停", function () {
    beforeEach(async function () {
      await token.mintInitial(pool.address, u(20_000));
    });

    it("仅 owner 可切换暂停", async function () {
      await expect(token.connect(attacker).setPaused(true)).to.be.revertedWithCustomError(
        token,
        "OwnableUnauthorizedAccount"
      );
    });

    it("暂停后普通转账被拒，销毁不受影响", async function () {
      await token.connect(owner).setPaused(true);
      await expect(
        token.connect(pool).transfer(user.address, u(100))
      ).to.be.revertedWith("TM: transfers paused");

      await token.connect(pool).burn(u(1_000));
      expect(await token.totalSupply()).to.equal(u(19_000));
    });

    it("恢复后转账正常", async function () {
      await token.connect(owner).setPaused(true);
      await token.connect(owner).setPaused(false);
      await token.connect(pool).transfer(user.address, u(100));
      expect(await token.balanceOf(user.address)).to.equal(u(100));
    });

    it("暂停不阻断初始发行（from=0 豁免）", async function () {
      const Factory = await ethers.getContractFactory("TMToken");
      const fresh = await Factory.deploy(owner.address, MAX_SUPPLY);
      await fresh.waitForDeployment();
      await fresh.connect(owner).setPaused(true);
      await fresh.mintInitial(pool.address, u(1_000));
      expect(await fresh.totalSupply()).to.equal(u(1_000));
    });
  });

  describe("黑名单", function () {
    beforeEach(async function () {
      await token.mintInitial(pool.address, u(20_000));
    });

    it("黑名单地址不可转出", async function () {
      await token.connect(owner).updateBlacklist(pool.address, true);
      await expect(
        token.connect(pool).transfer(user.address, u(100))
      ).to.be.revertedWith("TM: account blacklisted");
    });

    it("黑名单地址不可接收", async function () {
      await token.connect(owner).updateBlacklist(user.address, true);
      await expect(
        token.connect(pool).transfer(user.address, u(100))
      ).to.be.revertedWith("TM: account blacklisted");
    });

    it("解除黑名单后恢复", async function () {
      await token.connect(owner).updateBlacklist(user.address, true);
      await token.connect(owner).updateBlacklist(user.address, false);
      await token.connect(pool).transfer(user.address, u(100));
      expect(await token.balanceOf(user.address)).to.equal(u(100));
    });

    it("黑名单接口仅 owner 可调", async function () {
      await expect(
        token.connect(attacker).updateBlacklist(user.address, true)
      ).to.be.revertedWithCustomError(token, "OwnableUnauthorizedAccount");
    });

    it("向黑名单地址发行被拒", async function () {
      const Factory = await ethers.getContractFactory("TMToken");
      const fresh = await Factory.deploy(owner.address, MAX_SUPPLY);
      await fresh.waitForDeployment();
      await fresh.connect(owner).updateBlacklist(user.address, true);
      await expect(fresh.mintInitial(user.address, u(100))).to.be.revertedWith(
        "TM: account blacklisted"
      );
    });
  });

  describe("所有权", function () {
    it("禁止 renounceOwnership", async function () {
      await expect(token.renounceOwnership()).to.be.revertedWith("TM: renounce not allowed");
    });

    it("可转移所有权给新 owner", async function () {
      await token.transferOwnership(other.address);
      expect(await token.owner()).to.equal(other.address);
    });
  });
});
