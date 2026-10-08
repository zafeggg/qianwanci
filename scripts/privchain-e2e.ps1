# 千万次 私链一键联调脚本
#
# 把「有资金的充值池 → 用户链上充值 → 监听入账 → 参投 → 第N+3轮成功结算 → 提现出金 → 手续费销毁」
# 串成一条可重复执行的链路。每一步都尽量走**真实进程/接口**，不用假数据绕过。
#
# 前置（见 千万次-私链测试就绪清单.md 与 千万次-私链联调手册.md）：
#   1. contracts:  npm run node:local          （终端 A，常驻）
#   2. contracts:  npm run prepare:localnode    （部署 TM、铸初始发行、生成 ABI/部署记录）
#   3. npower:     build\npower.exe -f config\etc.privchain.yml
#   4. ops:        build\ops.exe -f config\etc.privchain.yml -port :5000
#   5. 数据库：crowd_privchain 已由 npower 首启 AutoMigrate 建表
#
# 用法： & .\scripts\privchain-e2e.ps1
# 可选参数： -Invest 10  -WithdrawAmount 5  -Rounds 4
#
# ⚠ 注意 hardhat node 默认「有交易才出块」，且 Confirmations=1：
#   充值交易之后必须再产生一个区块，监听才看得到那笔（脚本里已自动补一笔）。
[CmdletBinding()]
param(
  [string]$ApiBase = "http://127.0.0.1:3000",
  [string]$OpsBase = "http://127.0.0.1:5000",
  [string]$RpcUrl  = "http://127.0.0.1:8545",
  [string]$MysqlExe = "C:\Program Files\MySQL\MySQL Server 8.0\bin\mysql.exe",
  [string]$Database = "crowd_privchain",   # 不能叫 $Db：与 PowerShell 内置 -Debug 别名冲突
  [string]$TmContract = "0x5FbDB2315678afecb367f032d93F642f64180aa3",
  [string]$ApiKey = "privchain-apikey",
  [string]$PoolAddress = "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",   # 充值收款地址 = 测试用户本身（监听按 to 查 wallet 表）
  [string]$HotKey = "0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a", # 出金热钱包 = 账户[2]
  [string]$PoolKey = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80", # 手续费池私钥 = 账户[0]
  [string]$HotAddr = "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC", # 出金热钱包地址 = 账户[2]（与 Withdraw.HotWalletPrivate 对应）
  [string]$UserKey = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d", # 测试用户私钥 = 账户[1]
  [int]$Invest = 10,
  [double]$WithdrawAmount = 1
)

$ErrorActionPreference = "Stop"
$RepoRoot = Split-Path -Parent $PSScriptRoot
$fail = 0
function Step([string]$name, [bool]$ok, [string]$detail = "") {
  if ($ok) { Write-Host "  PASS  $name" -ForegroundColor Green }
  else { Write-Host "  FAIL  $name  — $detail" -ForegroundColor Red; $script:fail++ }
}
function Sql([string]$q) {
  $prev = $ErrorActionPreference; $ErrorActionPreference = "SilentlyContinue"
  try { $raw = & $MysqlExe -uroot -p123456 -N -B -e "USE $Database; $q" | Out-String } finally { $ErrorActionPreference = $prev }
  return ($raw -split "`r?`n" | Where-Object { $_ -and $_ -notmatch 'Warning|^mysql:' })
}
# 链上操作统一走 scripts/privchain-chain.mjs（避免在 PowerShell 里拼 JS）
function Invoke-Chain([string]$cmd, [string]$argsJson = "[]") {
  $script = Join-Path $RepoRoot "scripts\privchain-chain.mjs"
  $env:PC_RPC = $RpcUrl
  $env:PC_TOKEN = $TmContract
  $env:PC_HOT_KEY = $HotKey
  $env:PC_POOL_KEY = $PoolKey
  $prev = $ErrorActionPreference; $ErrorActionPreference = "SilentlyContinue"
  try {
    # 额外参数走环境变量 JSON：PowerShell 传数组给原生程序会踩形状问题（实测踩过）
    $env:PC_ARGS = $argsJson
    $out = & node $script $cmd 2>&1 | Out-String
  } finally { $ErrorActionPreference = $prev; Remove-Item Env:\PC_ARGS -ErrorAction SilentlyContinue }
  $out = ($out -split "`r?`n" | Where-Object { $_ -and $_ -notmatch '^(node:|At line:|    \+|CategoryInfo|FullyQualifiedErrorId|\s*~)' }) -join "`n"
  return $out.Trim()
}
Write-Host ("=" * 74)
Write-Host "千万次 私链一键联调（$Database）"
Write-Host ("=" * 74)

# ---------- 0. 健康检查 ----------
Write-Host "[0/7] 服务健康"
try { $h = Invoke-RestMethod "$ApiBase/health" -TimeoutSec 5; Step "npower 可达（chain=$($h.data.checks.ktoChain)）" $true }
catch { Step "npower 可达" $false $_; return }
try { Invoke-RestMethod "$OpsBase/ops/data/zmy" -TimeoutSec 5 | Out-Null; Step "ops 可达" $true }
catch { Step "ops 可达（能收到任何 HTTP 响应即算可达）" ($null -ne $_.Exception.Response) $_.Exception.Message }
$chainId = (& node -e "fetch('$RpcUrl',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:1,method:'eth_chainId',params:[]})}).then(r=>r.json()).then(j=>console.log(parseInt(j.result,16)))")
Step "私链 RPC 可达 chainId=$chainId" ($chainId -eq "31337") "chainId=$chainId"

# ---------- 1. 用户登录（签名登录是私链主路径，不依赖 KTO） ----------
Write-Host "[1/7] 钱包签名登录（测试用户 = hardhat 账户[1]）"
$login = & node (Join-Path $RepoRoot "scripts\privchain-login.mjs") 2>&1
$loginObj = $null
try { $loginObj = $login | ConvertFrom-Json } catch { }
Step "签名登录成功" ($loginObj.code -eq 0 -and $loginObj.walletId -gt 0) ($login -join " ")
$userAddr = $loginObj.address
$session = $loginObj.token
Write-Host "      用户 $userAddr  walletId=$($loginObj.walletId)"

# ---------- 2. 链上充值 → 监听入账 ----------
Write-Host "[2/7] 链上充值 5 TM → evmwatch 入账"
# 入账对象由配置 Chain.DepositCreditTo 决定（见 core/impl 的 evmwatch 入账分支）：
#   recipient = 记到**收款地址**（to）对应账户 —— 每个用户一个专属充值地址的模型；
#   sender    = 记到**付款地址**（from）对应账户 —— 所有人共用一个收款池的模型（DApp/私链口径）。
# ⚠ 2026-09-30 修正：本步骤原先**写死按收款地址断言**，而私链配置在切到 DApp「共享收款池」模型后
#   已改为 DepositCreditTo=sender（etc.privchain.yml），于是"充了 5 TM 但用户余额没变"恒成立——
#   不是监听坏了，是脚本口径与配置脱节。现在读配置决定测哪个地址，两种口径都能正确断言。
$creditTo = 'recipient'
$cfgPath = Join-Path $RepoRoot 'houduan\TiMi\config\etc.privchain.yml'
if (Test-Path $cfgPath) {
  $cfgText = [System.IO.File]::ReadAllText($cfgPath)
  if ($cfgText -match '(?m)^\s*DepositCreditTo:\s*"?([A-Za-z]+)"?') { $creditTo = $Matches[1].ToLower() }
}
# 出资方 = 账户[0]（手续费池私钥对应的地址）；sender 口径下它是入账对象
$payerAddr = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"
$creditAddr = if ($creditTo -eq 'sender') { $payerAddr } else { $PoolAddress }
Write-Host "      入账口径 DepositCreditTo=$creditTo → 断言地址 $creditAddr"
$before = [long](Sql "SELECT COALESCE(amount,0) FROM wallet_point WHERE address='$creditAddr' AND symbol='TM';" | Select-Object -First 1)
$chainBefore = [long](Invoke-Chain "balance" (@{ address = $PoolAddress } | ConvertTo-Json -Compress))
$depTx = Invoke-Chain "send-token" (@{ to = $PoolAddress; pk = $PoolKey; coins = "5" } | ConvertTo-Json -Compress)
$chainAfter = [long](Invoke-Chain "balance" (@{ address = $PoolAddress } | ConvertTo-Json -Compress))
Step "链上充值已到账（$chainBefore → $chainAfter）" (($chainAfter - $chainBefore) -ge 500000000) "delta=$($chainAfter-$chainBefore)"

# ⚠ 私链重启后会"重现"完全相同的交易哈希：hardhat 账户是固定的，E2E 又从 nonce 0 走同样的步骤，
#   于是 raw tx 逐字节相同、tx hash 也相同；而 `wallet_hash_charge` 的唯一索引会把"同一 hash"
#   判为重复入账（这是**正确**的防重复逻辑）→ 断言 delta=0，让人误以为监听坏了。
#   这里先把该 hash 的入账记录删掉，让本轮从"未入账"开始（同一条 hash 的幂等性由后面的"重扫幂等"步骤验证）。
if ($depTx) {
  Sql "DELETE FROM wallet_hash_charge WHERE hash='$depTx';" | Out-Null
  Write-Host "      已清理本轮 tx 的入账记录（保证重放场景下断言可重复）：$depTx"
}

# ⚠ 必须清掉扫块进度键：监听「无进度时才用 -fromBlock」，否则会从上次断点续扫，
#   而 hardhat 只在有交易时出块，新区块往往还没超过断点 → 这一轮什么都扫不到（实测踩过）。
#   清进度后从块 1 重扫，重复入账由 wallet_hash_charge 唯一索引挡住，是安全的。
#   ⚠ 2026-09-30 修正：进度键里的地址是 Go 侧 `pool.Hex()`（**EIP-55 校验和大小写**），
#   而这里原先用全小写地址去 DEL —— 根本删不掉（删除一直是无效的），于是 -fromBlock 1 被忽略、
#   继续从旧断点续扫，充值块常落在断点之外 → "充值了但余额没变"间隔性复现。现在两种大小写都删。
$redis = "C:\Program Files\Redis\redis-cli.exe"
$poolLower = Invoke-Chain "norm" (@{ address = $PoolAddress } | ConvertTo-Json -Compress)
if (Test-Path $redis) {
  foreach ($k in @("npower:evmwatch:lastblock:TM:$PoolAddress", "npower:evmwatch:lastblock:TM:$poolLower")) {
    $delOut = & $redis -n 6 DEL $k 2>$null
    Write-Host "      清进度键 $k → DEL=$delOut（1=已删, 0=不存在）"
  }
  $left = @(& $redis -n 6 KEYS 'npower:evmwatch:lastblock:TM:*' 2>$null | Where-Object { $_ })
  Step "扫块进度键已清空（剩余 $($left.Count) 个）" ($left.Count -eq 0) "left=$($left -join ',')"
}
& (Join-Path $RepoRoot "houduan\TiMi\build\evmwatch.exe") -f $cfgPath -symbol TM -decimals 8 -fromBlock 1 -once *> $null
$after = [long](Sql "SELECT COALESCE(amount,0) FROM wallet_point WHERE address='$creditAddr' AND symbol='TM';" | Select-Object -First 1)
Step "充值入账 +5 TM（$before → $after）" (($after - $before) -ge 500000000) "delta=$($after-$before)"

# 幂等：清进度重扫同一区间，余额不能变（唯一索引闸门应拦住重复入账）
& (Join-Path $RepoRoot "houduan\TiMi\build\evmwatch.exe") -f $cfgPath -symbol TM -decimals 8 -fromBlock 1 -once *> $null
$after2 = [long](Sql "SELECT COALESCE(amount,0) FROM wallet_point WHERE address='$creditAddr' AND symbol='TM';" | Select-Object -First 1)
Step "重扫幂等（余额未变 $after2）" ($after2 -eq $after) "was=$after now=$after2"

# ---------- 3. 提现（出金广播 + 手续费销毁） ----------
Write-Host "[3/7] 提现 $WithdrawAmount TM（链上广播 + 3% 手续费入 fee_burn）"
# 出金热钱包必须自己有 TM 才能转给用户（生产由资金池定期补充）。私链上从手续费池补 200 枚，幂等。
$hotBal = [long](Invoke-Chain "balance" (@{ address = $HotAddr } | ConvertTo-Json -Compress))
if ($hotBal -lt 10000000000) {
  Invoke-Chain "send-token" (@{ to = $HotAddr; pk = $PoolKey; coins = "200" } | ConvertTo-Json -Compress) | Out-Null
  Step "出金热钱包已补足 TM（$hotBal → 200 枚）" $true
} else {
  Write-Host "      出金热钱包已有 $([math]::Round($hotBal/100000000)) TM，跳过补水"
}
$feeBurnBefore = [int](Sql "SELECT COUNT(*) FROM fee_burn;" | Select-Object -First 1)
$wd = Invoke-RestMethod "$ApiBase/api/withdraw" -Method Post -Headers @{ "X-API-Key" = $ApiKey; "Content-Type" = "application/json"; Authorization = "Bearer $session" } `
  -Body (@{ address = $userAddr; asset = "TM"; amount = $WithdrawAmount; feeToken = "TM" } | ConvertTo-Json) -TimeoutSec 60
Step "提现接口 code=0" ($wd.code -eq 0) ($wd | ConvertTo-Json -Compress)
Step "返回真实链上哈希" ($wd.data.txHash -match '^0x[0-9a-f]{64}$') $wd.data.txHash
$feeBurnAfter = [int](Sql "SELECT COUNT(*) FROM fee_burn;" | Select-Object -First 1)
Step "手续费已入 fee_burn（+$($feeBurnAfter-$feeBurnBefore) 条）" ($feeBurnAfter -gt $feeBurnBefore) "before=$feeBurnBefore after=$feeBurnAfter"

# 反向用例：USDT 未开放链上出金，必须被拒绝
$neg = Invoke-RestMethod "$ApiBase/api/withdraw" -Method Post -Headers @{ "X-API-Key" = $ApiKey; "Content-Type" = "application/json"; Authorization = "Bearer $session" } `
  -Body (@{ address = $userAddr; asset = "USDT"; amount = 1; feeToken = "TM" } | ConvertTo-Json) -TimeoutSec 20
Step "未开放资产出金被拒（fail-closed）" ($neg.code -ne 0) ($neg.message)

# ---------- 4. 手续费销毁（EVM burn） ----------
Write-Host "[4/7] 手续费销毁 burn(uint256)"
$supplyBefore = Invoke-Chain "total-supply"
& (Join-Path $RepoRoot "houduan\TiMi\build\burn.exe") -f (Join-Path $RepoRoot "houduan\TiMi\config\etc.privchain.yml") -once *> $null
$supplyAfter = Invoke-Chain "total-supply"
$burned = [long](Sql "SELECT COALESCE(SUM(amount),0) FROM fee_burn WHERE status=1;" | Select-Object -First 1)
Step "链上 totalSupply 减少（$supplyBefore → $supplyAfter）" ([long]$supplyAfter -lt [long]$supplyBefore) "before=$supplyBefore after=$supplyAfter"
Step "fee_burn 存在已销毁记录（累计 $burned）" ($burned -gt 0) "burned=$burned"
$pending = [int](Sql "SELECT COUNT(*) FROM fee_burn WHERE status IN (0,2,3);" | Select-Object -First 1)
if ($pending -gt 0) {
  & (Join-Path $RepoRoot "houduan\TiMi\build\burn.exe") -f (Join-Path $RepoRoot "houduan\TiMi\config\etc.privchain.yml") -once *> $null
  $pending2 = [int](Sql "SELECT COUNT(*) FROM fee_burn WHERE status IN (0,2,3);" | Select-Object -First 1)
  Step "销毁幂等（重跑不再新增销毁）" ($pending2 -le $pending) "pending=$pending→$pending2"
}

# ---------- 5/6. 轮次与结算 ----------
Write-Host "[5/7] 轮次推进与第 N+3 轮结算"
& (Join-Path $RepoRoot "scripts\privchain-rounds.ps1") -Reset -UserAddress $userAddr -Invest 2 2>&1 | Select-String -Pattern 'PASS|静态收益|结算 ok|参投 ok' | ForEach-Object { "      $($_.Line.Trim())" }
$static = [double](Sql "SELECT COALESCE(SUM(static),0) FROM vote_reward;" | Select-Object -First 1)
Step "第 1 轮在第 4 轮成功结算（静态收益合计 $static）" ($static -gt 0) "static=$static"

# ---------- 6. 真实结算器：自动开下一轮 + 最高限额递增（需求#2） ----------
# ⚠ /ops/endRound 直接调 RewardManager，**不经过 RoundManager.End**，验证不到自动开轮；
#   自动开轮只在真实结算器 cmd/round 的 End 路径里。这里用真实 round.exe 跑一轮来验证。
Write-Host "[6/7] 真实结算器自动开轮（cmd/round → RoundManager.End）"
$projectId = [int]((Sql "SELECT id FROM project WHERE period=1 AND symbol='TM' ORDER BY id DESC LIMIT 1;") | Select-Object -First 1)

# 前置：End 要求第 (N-MinRound) 轮处于「待结算(2)」，否则中途 return（就测不到自动开轮）。
# 把前 3 轮置回待结算（它们由 driver 建立、已带 vote 行），第 4 轮置为进行中并设 5 秒后到期。
Sql "UPDATE project SET status=1 WHERE id=$projectId;" | Out-Null
Sql "UPDATE project_round SET status=2 WHERE project_id=$projectId AND round <= 3;" | Out-Null
Sql "UPDATE project_round SET status=1, current_vote=target_vote, start_time=NOW(), end_time=DATE_ADD(NOW(), INTERVAL 5 SECOND) WHERE project_id=$projectId AND round=4;" | Out-Null

$maxBefore = [double]((Sql "SELECT max_vote FROM project_round WHERE project_id=$projectId AND round=4;" | Select-Object -First 1))
$minBefore = [double]((Sql "SELECT min_vote FROM project_round WHERE project_id=$projectId AND round=4;" | Select-Object -First 1))

$roundExe = Join-Path $RepoRoot "houduan\TiMi\build\round.exe"
$roundCfg = Join-Path $RepoRoot "houduan\TiMi\config\round.privchain.yml"
$roundProc = Start-Process -FilePath $roundExe -ArgumentList "-f", $roundCfg `
  -WorkingDirectory (Join-Path $RepoRoot "houduan\TiMi") -WindowStyle Hidden -PassThru
Start-Sleep -Seconds 75   # 结束轮是每分钟 0 秒触发，给足一个周期
Stop-Process -Id $roundProc.Id -Force -ErrorAction SilentlyContinue

$newRound = Sql "SELECT round,target_vote,min_vote,max_vote FROM project_round WHERE project_id=$projectId AND round=5;" | Select-Object -First 1
if ($newRound) {
  $parts = ($newRound -split "`t")
  $nMax = [double]$parts[3]; $nMin = [double]$parts[2]
  Step "结算后自动开出第 5 轮（target=$($parts[1]) min=$nMin max=$nMax）" $true
  Step "需求#2 最高限额递增（$maxBefore → $nMax）" ($nMax -gt $maxBefore) "max=$maxBefore→$nMax"
  Step "需求#2 最低限额不变（$minBefore → $nMin）" ($nMin -eq $minBefore) "min=$minBefore→$nMin"
} else {
  Step "结算后自动开出第 5 轮" $false "未创建第 5 轮（确认 round.exe 可启动、且已到整分触发）"
}

# ---------- 7. 一键对账 ----------
Write-Host "[7/7] 后端账本一致性（recon）"
& (Join-Path $RepoRoot "houduan\TiMi\build\recon.exe") -f (Join-Path $RepoRoot "houduan\TiMi\config\etc.privchain.yml") 2>&1 |
  Select-Object -Last 6 | ForEach-Object { "      $_" }

Write-Host "[7/7] 结论"
if ($fail -eq 0) { Write-Host "  全部通过：充值/入账/幂等/出金/手续费销毁/轮次结算 已在私链上跑通" -ForegroundColor Green }
else { Write-Host "  有 $fail 项失败，见上方 FAIL 行" -ForegroundColor Yellow }
Write-Host ("=" * 74)
exit $fail
