# 千万次：算力矿机（hash_power）三种出局模式 —— 真实多日产出仿真（私链专用）
#
# 覆盖需求 #8（「算力三选一」）+ #7（爆仓折算算力的下游语义）：
#   1. 模式0（默认三倍出局）：**只按算力占比**分配矿池日产出，没有保底
#   2. 模式1（最长 300 天三倍出局）：日产出保底 = (算力×3) / 300 / 4h均价
#   3. 模式2（TM 加速）：保底口径同模式1
#   4. 金本位出局：累计产出币量 × 实时币价 ≥ 算力 × 3 时停止产币（status→1 + finish_time）
#   5. 保底取的是 **FIBO 价**（产出币是 FIBO），不是爆仓币（可能是 USDT）的价
#   6. **FIBO 无价时的缺陷复现**：行情源不可达时保底被整体跳过、出局永不触发
#
# 为什么必须用 `hashpower.exe -once`：
#   cmd/hashpower 是 cron 程序（每天 00:30 产一次），300 天的出局周期不可能靠等待；
#   -once 只跑一次日产出就退出，脚本循环调用 N 次 == 模拟 N 天（参数与生产完全同一份代码）。
#
# 数值设计（全部取二进制可精确表示的数，避免浮点边界抖动）：
#   参数：每日区块总产出=100、矿池占比=0.3 → 矿池日产出 = 30；Buff=3；MaxDays=300；FIBO 价 = 0.125
#   阶段B 账户（总算力 12000）：
#     A0 模式0 算力 9000 → 按占比 30×9000/12000 = 22.5/天（无保底；三倍目标 216000 币，测不到出局）
#     A1 模式1 算力 1000 → 占比 2.5，保底 (1000×3)/300/0.125 = 80 → 取 80/天
#     A2 模式2 算力 1000 → 同上 80/天
#     A3 模式1 算力 1000，爆仓币=USDT → 保底仍按 FIBO 价算 = 80/天（若误用 USDT 价 1 则只有 8/天）
#   出局：A1/A2/A3 三倍目标 = 1000×3/0.125 = 24000 币；80×300 = 24000 → **第 300 天精确出局**
#
# 用法： & .\scripts\privchain-hashpower.ps1
# 前置：npower :3000 / ops :5000 已按 config/etc.privchain.yml 启动
[CmdletBinding()]
param(
  [string]$OpsBase = "http://127.0.0.1:5000",
  [string]$MysqlExe = "C:\Program Files\MySQL\MySQL Server 8.0\bin\mysql.exe",
  [string]$Database = "crowd_privchain",
  [double]$Price = 0.125,
  [int]$Days = 300,
  [int]$ExtraDays = 2
)

$ErrorActionPreference = "Stop"
$RepoRoot = Split-Path -Parent $PSScriptRoot
$TiMiDir = Join-Path $RepoRoot "houduan\TiMi"
$HashPowerExe = Join-Path $TiMiDir "build\hashpower.exe"
$HashPowerCfg = "config\hashpower.privchain.yml"

$pass = 0; $fail = 0; $skip = 0
function Step([string]$name, [bool]$ok, [string]$detail = "") {
  if ($ok) { Write-Host "  PASS  $name" -ForegroundColor Green; $script:pass++ }
  else { Write-Host "  FAIL  $name  — $detail" -ForegroundColor Red; $script:fail++ }
}
# Skip 用于「本次运行环境下该断言不适用」的情况（例如占比类断言需要独占算力池）。
# 2026-09-30 新增：本脚本的三条占比类断言（模式0 按占比分配、出局者退出总算力后独得整池、
# 无价时占比产出）都假设**池中只有自己的测试账户**；池里若还有历史/演示账户（演示数据、
# 其它脚本遗留），占比会被稀释 → 断言必然失败，但那是测试数据状态问题而不是产品缺陷。
# 现在遇到这种情况显式 Skip 并提示，避免把假失败当成回归（在干净池上实测 PASS=20/FAIL=0）。
function Skip([string]$name, [string]$why) {
  Write-Host "  SKIP  $name  — $why" -ForegroundColor Yellow; $script:skip++
}
function Sql([string]$q) {
  $prev = $ErrorActionPreference; $ErrorActionPreference = "SilentlyContinue"
  try { $raw = & $MysqlExe -uroot -p123456 -N -B -e "USE $Database; $q" | Out-String } finally { $ErrorActionPreference = $prev }
  return ($raw -split "`r?`n" | Where-Object { $_ -and $_ -notmatch 'Warning|^mysql:' })
}
function Sql1([string]$q) { return (Sql $q | Select-Object -First 1) }
function E2F([string]$v) { if (-not $v) { return 0.0 }; return [double]::Parse($v, [Globalization.CultureInfo]::InvariantCulture) }
function Near([double]$a, [double]$b, [double]$eps = 0.0001) { return [Math]::Abs($a - $b) -lt $eps }

# 测试账户（地址仅作标识，hash_power 不校验地址格式）
$A0 = "0x00000000000000000000000000000000000000a0"   # 模式0，大算力
$A1 = "0x00000000000000000000000000000000000000a1"   # 模式1
$A2 = "0x00000000000000000000000000000000000000a2"   # 模式2
$A3 = "0x00000000000000000000000000000000000000a3"   # 模式1 + 爆仓币 USDT
$A9 = "0x00000000000000000000000000000000000000a9"   # 阶段A：累计已接近三倍目标
$TestAddrs = @($A0, $A1, $A2, $A3, $A9)

function Get-Params([string]$token) {
  $resp = Invoke-RestMethod "$OpsBase/ops/params" -Headers @{ Authorization = "Bearer $token" } -TimeoutSec 10
  $map = @{}
  if ($resp.data.params) { $resp.data.params.PSObject.Properties | ForEach-Object { $map[$_.Name] = $_.Value } }
  return $map
}
function Set-Params([string]$token, $map) {
  $resp = Invoke-RestMethod "$OpsBase/ops/params?mode=replace" -Method Post `
    -Headers @{ Authorization = "Bearer $token"; "Content-Type" = "application/json" } `
    -Body ($map | ConvertTo-Json -Depth 6) -TimeoutSec 10
  if ($resp.code -ne 200) { throw "设置运行参数失败：$($resp | ConvertTo-Json -Compress)" }
}
function Get-ParamValue($map, [string]$key) { if ($map.ContainsKey($key)) { return $map[$key] }; return $null }

# 建算力账户：power / mode / symbol / 起始累计产出（target_reward 按入账时币价快照，与 BookPower 同口径）
function Seed([string]$addr, [double]$power, [int]$mode, [string]$symbol, [double]$totalReward = 0) {
  $target = $power * 3 / $Price
  Sql @"
DELETE FROM hash_power WHERE address='$addr';
INSERT INTO hash_power (created_at,updated_at,address,symbol,power,price,mode,total_reward,remain_reward,target_reward,status,start_time)
VALUES (NOW(),NOW(),'$addr','$symbol',$power,$Price,$mode,$totalReward,$totalReward,$target,0,NOW());
"@ | Out-Null
}
function RunDay() {
  # hashpower 的 -f 与 LogPath 都是相对路径，必须在 houduan\TiMi 下执行
  Push-Location $TiMiDir
  try {
    & $HashPowerExe -f $HashPowerCfg -once *> $null
    $code = $LASTEXITCODE
  } finally { Pop-Location }
  if ($code -ne 0) { throw "hashpower -once 执行失败（exit=$code）" }
}
function Total([string]$addr) { return E2F (Sql1 "SELECT total_reward FROM hash_power WHERE address='$addr';") }
function Status([string]$addr) { return [int](Sql1 "SELECT status FROM hash_power WHERE address='$addr';") }

Write-Host ("=" * 78)
Write-Host "千万次 算力矿机三模式 —— 多日产出仿真（$Database）"
Write-Host ("=" * 78)

# ---------- 0. 前置 ----------
Write-Host "[0/6] 前置检查"
try { $h = (Invoke-RestMethod "http://127.0.0.1:3000/health" -TimeoutSec 8).data; Step "npower 可达（mysql=$($h.checks.mysql)）" ($h.checks.mysql -eq "ok") } catch { Step "npower 可达" $false $_; exit 1 }
$opsToken = (Invoke-RestMethod "$OpsBase/ops/sign" -Method Post -ContentType "application/json" -Body '{"account":"ops","pwd":"privchain-ops-pwd"}' -TimeoutSec 10).data
Step "ops 登录" ($null -ne $opsToken)
Step "hashpower.exe 存在" (Test-Path $HashPowerExe) $HashPowerExe
Step "私链 hashpower 配置存在" (Test-Path (Join-Path $TiMiDir $HashPowerCfg)) $HashPowerCfg

# ---------- 1. 备份参数 + 清场 ----------
Write-Host "[1/6] 备份运行参数并清理历史算力账户"
$paramsBackup = Get-Params $opsToken
$paramsTest = @{}
foreach ($k in $paramsBackup.Keys) { $paramsTest[$k] = $paramsBackup[$k] }
$paramsTest['HashPowerPoolDailyOutput'] = 100   # → 矿池日产出 = 100 × 0.3 = 30
$paramsTest['HashPowerPoolDailyRatio'] = 0.3
$paramsTest['HashPowerBuff'] = 3
$paramsTest['HashPowerMaxDays'] = 300
# ⚠ 阶段A 刻意不下发 FiboStaticPrice：复现「行情不可达 → FIBO 无价」的现场
$paramsTest.Remove('FiboStaticPrice')
if ($paramsTest.ContainsKey('FiboStaticPrice')) { throw '参数清理失败' }
$addrIn = ($TestAddrs | ForEach-Object { "'$_'" }) -join ","
Sql "DELETE FROM hash_power WHERE address IN ($addrIn); DELETE FROM wallet_tx WHERE ``to`` IN ($addrIn);" | Out-Null
# 算力池是否"独占"：占比类断言的前提。池中若还有其它账户（演示数据/其它脚本遗留），
# 自己的算力份额会被稀释，占比断言不成立 → 后面这几条改为 Skip。
$foreignCount = [int](Sql1 "SELECT COUNT(*) FROM hash_power WHERE address NOT IN ($addrIn);")
$script:poolExclusive = ($foreignCount -eq 0)
if (-not $script:poolExclusive) {
  Write-Host "  WARN  算力池中存在 $foreignCount 个非本脚本账户（历史/演示数据）：占比类断言将标记为 SKIP" -ForegroundColor Yellow
  Write-Host "        如需完整校验（期望 PASS=20/FAIL=0），请先在空池上运行（例如临时清空 hash_power 后重跑）" -ForegroundColor Yellow
}
Set-Params $opsToken $paramsTest
Start-Sleep -Seconds 1
$eff = Get-Params $opsToken
Step "已下发矿池参数且**不含** FiboStaticPrice（复现无价现场）" (-not $eff.ContainsKey('FiboStaticPrice')) ($eff | ConvertTo-Json -Compress)

# ⚠ 从这里开始的全部动作都包在 try/finally 里：脚本中途抛错（例如配置路径写错）也必须
#   把运行参数还原、把测试算力账户清掉，否则会把污染后的参数当成下次运行的"备份"再还原一遍。
try {

# ---------- 2. 阶段A：无价 → 保底被跳过、出局不触发 ----------
Write-Host "[2/6] 阶段A（FIBO 无价）：应复现「保底失效 + 出局永不触发」"
# A1 模式1 算力1000：有价时应为 80/天（保底）；无价时只剩按占比 30×1000/2000 = 15/天
# A9 模式0 算力1000：起始累计 23999，一天后 24014 > 三倍目标 24000，但无价 → 不得出局
Seed $A1 1000 1 "FIBO"
Seed $A9 1000 0 "FIBO" 23999
RunDay
$a1NoPrice = Total $A1
$a9NoPrice = Total $A9
$a9NoPriceStatus = Status $A9
Write-Host "      A1(模式1) 无价日产出=$a1NoPrice ；A9 累计=$a9NoPrice status=$a9NoPriceStatus"
if ($script:poolExclusive) {
  Step "无价时模式1 的保底被整体跳过（实得 15，而非保底的 80）" (Near $a1NoPrice 15) "实际 $a1NoPrice"
} else {
  Skip "无价时模式1 的保底被整体跳过（实得 15）" "池中非独占，占比值被稀释：实际 $a1NoPrice"
}
Step "无价时累计已超三倍目标仍不判出局（缺陷现场）" ($a9NoPrice -gt 24000 -and $a9NoPriceStatus -eq 0) "累计=$a9NoPrice status=$a9NoPriceStatus"

# ---------- 3. 下发 FIBO 静态兜底价，进入阶段B ----------
Write-Host "[3/6] 下发 FiboStaticPrice=$Price（行情兜底），进入阶段B"
$paramsTest['FiboStaticPrice'] = $Price
Set-Params $opsToken $paramsTest
Start-Sleep -Seconds 1
$eff2 = Get-Params $opsToken
Step "FiboStaticPrice 已生效" ((Get-ParamValue $eff2 'FiboStaticPrice') -eq $Price) ($eff2 | ConvertTo-Json -Compress)

Sql "DELETE FROM hash_power WHERE address IN ($addrIn);" | Out-Null
Seed $A0 9000 0 "FIBO"
Seed $A1 1000 1 "FIBO"
Seed $A2 1000 2 "FIBO"
Seed $A3 1000 1 "USDT"
$powerSum = 12000.0
$poolDaily = 100 * 0.3
$a0PerDay = $poolDaily * 9000 / $powerSum          # 22.5
$a1Floor = 1000 * 3 / 300 / $Price                 # 80
$a0Target = 9000 * 3 / $Price                      # 216000
$a1Target = 1000 * 3 / $Price                      # 24000
Write-Host "      预期：A0=$a0PerDay/天（无保底） A1/A2/A3 保底=$a1Floor/天 ；A1 三倍目标=$a1Target 币"

# ---------- 4. 阶段B：连跑 N 天 ----------
Write-Host "[4/6] 阶段B：连跑 $Days 天（每次调用 hashpower.exe -once = 一天）"
$sw = [Diagnostics.Stopwatch]::StartNew()
for ($d = 1; $d -le $Days; $d++) {
  RunDay
  if ($d % 50 -eq 0) { Write-Host "      ...已模拟 $d 天（$([int]$sw.Elapsed.TotalSeconds)s）" }
}
$sw.Stop()
Write-Host "      $Days 天模拟完成，用时 $([int]$sw.Elapsed.TotalSeconds)s"

$a0Total = Total $A0
$a1Total = Total $A1
$a2Total = Total $A2
$a3Total = Total $A3
$a0Status = Status $A0
Write-Host "      A0=$a0Total(status=$a0Status) A1=$a1Total A2=$a2Total A3(USDT)=$a3Total"

if ($script:poolExclusive) {
  Step "模式0 只按算力占比、无保底（$Days 天 = $a0PerDay × $Days）" (Near $a0Total ($a0PerDay * $Days)) "实际 $a0Total"
} else {
  Skip "模式0 只按算力占比、无保底（$Days 天）" "池中非独占：实际 $a0Total（期望 $($a0PerDay * $Days)，按占比值算）"
}
Step "模式1 按保底产出（$a1Floor/天，远高于占比值 $($poolDaily*1000/$powerSum)）" (Near $a1Total ($a1Floor * $Days)) "实际 $a1Total"
Step "模式2 按保底产出（与模式1 同口径）" (Near $a2Total ($a1Floor * $Days)) "实际 $a2Total"
Step "爆仓币为 USDT 时保底仍取 FIBO 价（80/天；若误用 USDT 价 1 会是 8/天）" (Near $a3Total ($a1Floor * $Days)) "实际 $a3Total"
Step "模式0 未出局（三倍目标 $a0Target 币，尚差很多）" ($a0Status -eq 0) "A0 累计=$a0Total status=$a0Status"

# ---------- 5. 出局判定：第 N 天精确出局 ----------
Write-Host "[5/6] 出局判定：累计产出价值 ≥ 算力×3 应停止产币"
$a1Status = Status $A1
$a1Finish = Sql1 "SELECT finish_time FROM hash_power WHERE address='$A1';"
$tripleValue = $a1Total * $Price
Step "模式1 在第 $Days 天精确出局（累计 $a1Total 币 × $Price = $tripleValue ≥ 算力×3 = 3000）" `
  ($a1Status -eq 1) "status=$a1Status 累计=$a1Total"
Step "模式2 同步出局" ((Status $A2) -eq 1) "A2 status=$(Status $A2)"
Step "USDT 爆仓账户同步出局" ((Status $A3) -eq 1) "A3 status=$(Status $A3)"
Step "出局时间已落库（finish_time 非空）" ($null -ne $a1Finish -and "$a1Finish" -ne "") "finish_time=$a1Finish"

# ---------- 6. 出局后不再产币 + 还原 ----------
Write-Host "[6/6] 出局后停止产币；还原运行参数并清理"
$before = @{ A1 = $a1Total; A2 = $a2Total; A3 = $a3Total }
for ($d = 1; $d -le $ExtraDays; $d++) { RunDay }
$a1After = Total $A1; $a2After = Total $A2; $a3After = Total $A3; $a0After = Total $A0
Step "已出局账户再跑 $ExtraDays 天累计产出**零增长**" `
  ((Near $a1After $before.A1) -and (Near $a2After $before.A2) -and (Near $a3After $before.A3)) `
  "A1=$a1After A2=$a2After A3=$a3After"
$expectA0 = $a0PerDay * $Days + $poolDaily * $ExtraDays   # 出局后只剩 A0，独得整个矿池日产出
if ($script:poolExclusive) {
  Step "模式0 继续产币且出局者退出总算力后独得整池（+$poolDaily/天）" (Near $a0After $expectA0) "实际 $a0After 期望 $expectA0"
} else {
  Skip "模式0 继续产币且出局者退出总算力后独得整池（+$poolDaily/天）" "池中非独占：实际 $a0After 期望 $expectA0"
}

} finally {
  # 无论上面成功与否，运行参数与测试数据都必须还原（否则下次运行的"备份"就是污染值）
  Set-Params $opsToken $paramsBackup
  $restored = Get-Params $opsToken
  $b1 = Get-ParamValue $paramsBackup 'FiboStaticPrice'
  $r1 = Get-ParamValue $restored 'FiboStaticPrice'
  $b2 = Get-ParamValue $paramsBackup 'HashPowerPoolDailyOutput'
  $r2 = Get-ParamValue $restored 'HashPowerPoolDailyOutput'
  Step "运行参数已还原为测试前的值" (($b1 -eq $r1) -and ($b2 -eq $r2)) "FiboStaticPrice=$r1 HashPowerPoolDailyOutput=$r2"
  Sql "DELETE FROM hash_power WHERE address IN ($addrIn); DELETE FROM wallet_tx WHERE ``to`` IN ($addrIn);" | Out-Null
  Write-Host "      测试算力账户与产币流水已清理（不影响其他数据）"
}

Write-Host ("=" * 78)
Write-Host "结果：PASS=$pass / FAIL=$fail / SKIP=$skip"
Write-Host ("=" * 78)
exit $fail
