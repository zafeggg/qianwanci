// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import "@openzeppelin/contracts/token/ERC20/extensions/ERC20Burnable.sol";
import "@openzeppelin/contracts/access/Ownable.sol";
import "@openzeppelin/contracts/utils/Pausable.sol";

/**
 * @title TMToken 千万次（TiMi）手续费代币 TM
 * @notice 部署链 FIBONACCI（chainId 12306，RPC https://network.hzroc.art）
 *
 * 规则依据（来源 houduan/TiMi/千万次-技术方案.md）
 *  1. 1.7 提币手续费 3%，以 TM 支付，价格 3.4U/枚兜底
 *  2. 1.7 手续费全部链上销毁，直至 TM 仅剩 7777 枚
 *  3. 3.3 流程3 后端 burn 程序按 fee_burn 表逐笔调用本合约完成销毁
 *
 * 设计口径（owner 2026-09-14 裁定）
 *  1. 精度 8 位，与后端 config.SymbolDictionary["TM"]=1e8 对齐，免换算层
 *  2. 初始发行总量留空，部署时不铸造，由 owner 在资金模型定稿后一次性 mintInitial
 *  3. 销毁下限 7777 枚在合约层强制，链上可验证；后端 burn 工具作第二道保险
 *  4. owner 为单一部署 EOA，禁用 renounceOwnership 防合约永久失管
 *
 * 总量留空的原因
 *  1. 手续费池规模、用户获取 TM 的路径、销毁速度三者需与运营数值模型匹配
 *  2. 合约不可升级，总量定错只能重新部署并迁移，代价高
 *  3. 以 MAX_SUPPLY 硬顶封住增发风险，发行量后置设定同时保住灵活性与约束力
 */
contract TMToken is ERC20, ERC20Burnable, Ownable, Pausable {
    /// @notice 精度 8 位（对齐后端记账 1e8）
    uint8 private constant TM_DECIMALS = 8;

    /// @notice 销毁下限：流通量不得低于 7777 枚（通缩设计终点）
    uint256 public constant MIN_CIRCULATING = 7_777 * 10**TM_DECIMALS;

    /// @notice 发行硬顶，部署时确定，立即数不可改
    uint256 public immutable MAX_SUPPLY;

    /// @notice 初始发行是否已完成（全生命周期只允许一次）
    bool public initialSupplyMinted;

    /// @notice 累计销毁量（最小单位）
    uint256 public totalBurnedAmount;

    /// @notice 黑名单（风控用，命中地址不可转出与接收）
    mapping(address => bool) public blacklisted;

    event InitialSupplyMinted(address indexed to, uint256 amount);
    event FeeBurned(address indexed from, uint256 amount, uint256 circulatingAfter);
    event BlacklistUpdated(address indexed account, bool status);
    event PausedUpdated(bool paused);

    modifier notBlacklisted(address account) {
        require(!blacklisted[account], "TM: account blacklisted");
        _;
    }

    /**
     * @param initialOwner owner 地址（单一部署 EOA）
     * @param maxSupply_ 发行硬顶（最小单位）。总量未定稿时给宽裕上限，例如 100 亿枚
     */
    constructor(address initialOwner, uint256 maxSupply_)
        ERC20("TiMi", "TM")
        Ownable(initialOwner)
    {
        require(initialOwner != address(0), "TM: invalid owner");
        require(maxSupply_ > MIN_CIRCULATING, "TM: max supply below floor");
        MAX_SUPPLY = maxSupply_;
    }

    /// @inheritdoc ERC20
    function decimals() public pure override returns (uint8) {
        return TM_DECIMALS;
    }

    // ============ 发行 ============

    /**
     * @notice 一次性初始发行（总量留空，部署后由 owner 定稿）
     * @param to 接收地址，通常为手续费资金池
     * @param amount 发行量（最小单位，1 枚 = 1e8）
     */
    function mintInitial(address to, uint256 amount) external onlyOwner {
        require(!initialSupplyMinted, "TM: initial supply already set");
        require(to != address(0), "TM: invalid recipient");
        require(amount > 0 && amount <= MAX_SUPPLY, "TM: amount exceeds max supply");
        initialSupplyMinted = true;
        _mint(to, amount);
        emit InitialSupplyMinted(to, amount);
    }

    // ============ 销毁（手续费通缩）============

    /**
     * @notice 销毁调用者自身持有的 TM（资金池销毁手续费的主路径）
     * @param amount 销毁量（最小单位）
     */
    function burn(uint256 amount) public override notBlacklisted(msg.sender) {
        _burnWithFloor(msg.sender, amount);
    }

    /**
     * @notice 授权销毁他人 TM（备用路径，需先 approve）
     * @param account 被销毁账户
     * @param amount 销毁量（最小单位）
     */
    function burnFrom(address account, uint256 amount)
        public
        override
        notBlacklisted(msg.sender)
        notBlacklisted(account)
    {
        _spendAllowance(account, msg.sender, amount);
        _burnWithFloor(account, amount);
    }

    /**
     * @dev 销毁并强制 7777 枚下限
     * 流通量降到 MIN_CIRCULATING 时拒绝继续销毁，链上直接可验证
     */
    function _burnWithFloor(address from, uint256 amount) internal {
        require(amount > 0, "TM: zero amount");
        uint256 supply = totalSupply();
        require(supply > MIN_CIRCULATING, "TM: burn target 7777 reached");
        require(amount <= supply - MIN_CIRCULATING, "TM: exceeds burnable, floor 7777 protected");
        _burn(from, amount);
        totalBurnedAmount += amount;
        emit FeeBurned(from, amount, totalSupply());
    }

    // ============ 运维 ============

    /**
     * @notice 紧急暂停/恢复（仅限普通转账，铸造与销毁不受影响）
     */
    function setPaused(bool p) external onlyOwner {
        if (p) {
            _pause();
        } else {
            _unpause();
        }
        emit PausedUpdated(p);
    }

    /// @notice 更新黑名单
    function updateBlacklist(address account, bool status) external onlyOwner {
        require(account != address(0), "TM: invalid address");
        blacklisted[account] = status;
        emit BlacklistUpdated(account, status);
    }

    // ============ 只读（后端 burn 工具对账用）============

    /// @notice 流通量 = 总供应量（销毁即真实减量）
    function circulatingSupply() external view returns (uint256) {
        return totalSupply();
    }

    /// @notice 距 7777 下限还可销毁的数量
    function remainingBurnable() external view returns (uint256) {
        uint256 supply = totalSupply();
        return supply > MIN_CIRCULATING ? supply - MIN_CIRCULATING : 0;
    }

    /// @notice 是否已达销毁终点
    function burnTargetReached() external view returns (bool) {
        return totalSupply() <= MIN_CIRCULATING;
    }

    /// @notice 累计销毁量
    function getTotalBurned() external view returns (uint256) {
        return totalBurnedAmount;
    }

    // ============ 内部 ============

    function _update(address from, address to, uint256 value)
        internal
        override
        notBlacklisted(from)
        notBlacklisted(to)
    {
        // 暂停只限制普通转账；铸造（from=0）与销毁（to=0）不受影响
        if (from != address(0) && to != address(0)) {
            require(!paused(), "TM: transfers paused");
        }
        super._update(from, to, value);
    }

    /// @dev 禁止放弃所有权，防止合约永久失管
    function renounceOwnership() public view override onlyOwner {
        revert("TM: renounce not allowed");
    }
}
