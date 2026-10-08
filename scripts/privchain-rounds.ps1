# 千万次 私链轮次与结算链路驱动（本地私链专用）
#
# 覆盖就绪清单里的「轮次推进 + 第 N+3 轮成功结算」半条链路。用到的都是既有能力：
#   ops /ops/sign、/ops/setRound、/ops/endRound   （ops.exe，默认 :5000）
#   npower /api/participate                       （npower.exe，:3000）
#
# 前置：
#   1. contracts: npm run node:local / npm run prepare:localnode
#   2. npower 已用 config/etc.privchain.yml 启动（:3000）
#   3. scripts/privchain-seed.sql 已灌入 crowd_privchain（建 project 行与 config 行）
#   4. 测试用户在 /api/auth/login 首登注册过，且 wallet_point 有 TM 余额
#
# 用法：pwsh -File scripts/privchain-rounds.ps1
# 环境变量：OPS_BASE / API_BASE / MYSQL_EXE / DB / TEST_PRIVATE_KEY / TARGET

param(
  [string]$OpsBase  = "http://127.0.0.1:5000",
  [string]$ApiBase  = "http://127.0.0.1:3000",
  [string]$MysqlExe = "C:\Program Files\MySQL\MySQL Server 8.0\bin\mysql.exe",
  [string]$Db       = "crowd_privchain",
  [int]   $Rounds   = 4,
  [double]$Invest   = 10,
  [string]$UserAddress = "",   # 参投用户地址；留空则取 wallet 表第一行
  [switch]$Reset
)

$ErrorActionPreference = "Stop"
$RepoRoot = Split-Path -Parent $PSScriptRoot
$TiMi = Join-Path $RepoRoot "houduan\TiMi"

function Sql([string]$q) {
  # mysql 会把 "password on the command line" 警告写到 stderr；
  # 在 $ErrorActionPreference=Stop 下这会被当成 NativeCommandError 直接中断脚本，
  # 因此临时切到 SilentlyContinue，收集输出后再过滤掉警告行。
  $prev = $ErrorActionPreference
  $ErrorActionPreference = "SilentlyContinue"
  try {
    $raw = & $MysqlExe -uroot -p123456 -N -B -e "USE $Db; $q" | Out-String
  } finally {
    $ErrorActionPreference = $prev
  }
  return ($raw -split "`r?`n" | Where-Object { $_ -and $_ -notmatch 'Warning|^mysql:' })
}
function J($o) { return $o }   # Invoke-RestMethod 已返回对象，无需再解析

Write-Host ("=" * 70)
Write-Host "千万次 私链轮次+结算驱动  ($Db)"
Write-Host ("=" * 70)

# ---------- 0. 前置检查 ----------
try { Invoke-RestMethod "$ApiBase/api/prices" -Headers @{ "X-API-Key" = "privchain-apikey" } -TimeoutSec 5 | Out-Null }
catch { throw "npower ($ApiBase) 不可达，请先用 config/etc.privchain.yml 启动它" }
try { Invoke-RestMethod "$OpsBase/ops/data/zmy" -TimeoutSec 5 | Out-Null }
catch { Write-Warning "ops ($OpsBase) 未响应（若后续 /ops/* 失败，请启动 build\ops.exe -f config/etc.privchain.yml -port :5000）" }

if ($Reset) {
  Write-Host "[reset] 清理轮次/投票/收益数据（保留钱包与余额）"
  Sql "DELETE FROM vote_reward; DELETE FROM vote; DELETE FROM project_round; DELETE FROM project; DELETE FROM wallet_tx WHERE type IN (1,4,5,6,7,8,9,14);" | Out-Null
}

# ---------- 1. ops 登录 ----------
Write-Host "[1/5] ops 登录"
$sign = J (Invoke-RestMethod "$OpsBase/ops/sign" -Method Post -ContentType "application/json" `
    -Body (@{ account = "ops"; pwd = "privchain-ops-pwd" } | ConvertTo-Json) -TimeoutSec 10 )
if ($sign.code -ne 200) { throw "ops 登录失败：$($sign | ConvertTo-Json -Compress)" }
$opsToken = $sign.data
Write-Host "      token ok"

# ---------- 2. 运行参数：阶段切到 2（TM 属手续费币种，阶段1 只放行 FIBO/USDT） ----------
# /ops/params 是**整体覆盖**语义：必须取当前值、改一个字段再写回，否则会清掉其它参数。
Write-Host "[2/5] 运行参数（CurrentStage=2 以放行 TM 参与）"
$params = J (Invoke-RestMethod "$OpsBase/ops/params" -Headers @{ Authorization = "Bearer $opsToken" } -TimeoutSec 10)
# 信封为 {code,msg,data:{params:{...}}}；params 为空对象时无法直接加属性，
# 因此新建 hashtable 并复制既有键（/ops/params 是整体覆盖语义，必须保留原值）。
$src = $params.data.params
$paramObj = @{}
if ($src) { $src.PSObject.Properties | ForEach-Object { $paramObj[$_.Name] = $_.Value } }
$paramObj["CurrentStage"] = 2
$setRes = J (Invoke-RestMethod "$OpsBase/ops/params?mode=replace" -Method Post -Headers @{ Authorization = "Bearer $opsToken" } `
    -ContentType "application/json" -Body ($paramObj | ConvertTo-Json -Depth 6) -TimeoutSec 10)
if ($setRes.code -ne 200) { throw "设置运行参数失败：$($setRes | ConvertTo-Json -Compress)" }
Write-Host "      params 已写入（CurrentStage=2）"

# ---------- 3. 建 project + 轮次 ----------
Sql "INSERT INTO project (created_at, updated_at, period, symbol, status) SELECT NOW(), NOW(), 1, 'TM', 1 WHERE NOT EXISTS (SELECT 1 FROM project WHERE period=1 AND symbol='TM');" | Out-Null
$projectId = [int]((Sql "SELECT id FROM project WHERE period=1 AND symbol='TM' ORDER BY id DESC LIMIT 1;") | Select-Object -First 1)
Write-Host "[3/5] project id=$projectId"

$existing = [int](Sql "SELECT COUNT(*) FROM project_round WHERE project_id=$projectId;")
if ($existing -eq 0) {
  $headers = @{ Authorization = "Bearer $opsToken" }
  for ($i = 1; $i -le $Rounds; $i++) {
    # target 必须 >= max；起始时间必须晚于当前时间（RoundManager.Init 的硬校验）
    $start = (Get-Date).AddSeconds(5).ToString("yyyy-MM-dd HH:mm:ss")
    $end = (Get-Date).AddHours(2).ToString("yyyy-MM-dd HH:mm:ss")
    $body = @{ projectId = $projectId; target = 1000 * $i; min = 1; max = 10; startTime = $start; endTime = $end } | ConvertTo-Json
    $r = J (Invoke-RestMethod "$OpsBase/ops/setRound" -Method Post -Headers $headers -ContentType "application/json" -Body $body -TimeoutSec 10 )
    if ($r.code -ne 200) { throw "setRound 第 $i 轮失败：$($r | ConvertTo-Json -Compress)" }
    Write-Host "      轮 $($r.data.round) 已建 (id=$($r.data.id) target=$($r.data.targetVote))"
    Start-Sleep -Seconds 1
  }
} else {
  Write-Host "      已存在 $existing 个轮次，跳过建轮"
}
Sql "SELECT id,round,target_vote,status FROM project_round WHERE project_id=$projectId ORDER BY round;" | ForEach-Object { Write-Host "      $_" }

# ---------- 3. 逐轮「开始 → 参投 → 结束」 ----------
Write-Host "[4/5] 逐轮推进（目标=1 保证筹满，第 4 轮结束应结算第 1 轮）"
for ($n = 1; $n -le $Rounds; $n++) {
  $rid = [int](Sql "SELECT id FROM project_round WHERE project_id=$projectId AND round=$n LIMIT 1;")
  if (-not $rid) { Write-Warning "第 $n 轮不存在，跳过"; continue }

  # round.exe 只认 status=0 的轮次，且要求 start_time 已过 1m15s；测试驱动直接置 1 开始
  Sql "UPDATE project_round SET status=1, start_time=NOW(), end_time=DATE_ADD(NOW(), INTERVAL 2 HOUR) WHERE id=$rid;" | Out-Null

  # 参投（走真实 /api/participate）
  $user = if ($UserAddress) { $UserAddress } else { (Sql "SELECT address FROM wallet ORDER BY id LIMIT 1;" | Select-Object -First 1) }
  $pv = J (Invoke-RestMethod "$ApiBase/api/participate" -Method Post -Headers @{ "X-API-Key" = "privchain-apikey"; "Content-Type" = "application/json" } `
      -Body (@{ address = $user; roundId = $rid; amount = $Invest; asset = "TM" } | ConvertTo-Json) -TimeoutSec 10 )
  if ($pv.code -ne 0) { throw "参投第 $n 轮失败：$($pv | ConvertTo-Json -Compress)" }
  Write-Host "      轮 $n 参投 ok（余额 $($pv.data.balance)）"

  # 结束轮：Success 触发 Promote + 结算第 (n-3) 轮 + 自动开下一轮
  $er = J (Invoke-RestMethod "$OpsBase/ops/endRound" -Method Post -Headers @{ Authorization = "Bearer $opsToken"; "Content-Type" = "application/json" } `
      -Body (@{ roundId = $rid; act = "Success" } | ConvertTo-Json) -TimeoutSec 20 )
  if ($er.code -ne 200) { throw "endRound 第 $n 轮失败：$($er | ConvertTo-Json -Compress)" }
  Write-Host "      轮 $n 结算 ok"
  Start-Sleep -Seconds 2   # 结算在 ants 协程池里异步执行，给它时间落库
}

# ---------- 4. 校验 ----------
Write-Host "[5/5] 校验"
Start-Sleep -Seconds 3
Write-Host "  轮次状态（3=已结束；成功轮 Success=1；max_vote 应逐轮递增、min_vote 应不变）"
Sql "SELECT round,target_vote,min_vote,max_vote,current_vote,success,status FROM project_round WHERE project_id=$projectId ORDER BY round;" | ForEach-Object { Write-Host "      $_" }

# 需求#2 断言：AutoCreateNextRound 自动开出的下一轮必须「目标×1.3、最高限额递增、最低限额不变」
$maxList = @(Sql "SELECT max_vote FROM project_round WHERE project_id=$projectId ORDER BY round;" | ForEach-Object { [double]$_ })
$minList = @(Sql "SELECT min_vote FROM project_round WHERE project_id=$projectId ORDER BY round;" | ForEach-Object { [double]$_ })
$growOK = $true
for ($i = 1; $i -lt $maxList.Count; $i++) { if ($maxList[$i] -le $maxList[$i-1]) { $growOK = $false } }
$minSame = ($minList | Select-Object -Unique).Count -le 1
Write-Host "  最高限额序列: $($maxList -join ' → ')"
Write-Host "  最低限额序列: $($minList -join ' → ')"

Write-Host "  静态收益记录 vote_reward（第1轮应在第4轮结算：static = 10 × 13% = 1.3）"
Sql "SELECT id,address,static,dynamic_shard,dynamic_team FROM vote_reward ORDER BY id;" | ForEach-Object { Write-Host "      $_" }

Write-Host "  钱包流水（7=成功本金退还, 6=静态收益）"
Sql "SELECT id,symbol,amount,type FROM wallet_tx WHERE type IN (1,6,7) ORDER BY id DESC LIMIT 6;" | ForEach-Object { Write-Host "      $_" }

Write-Host "  余额"
Sql "SELECT address,symbol,amount FROM wallet_point ORDER BY symbol,address;" | ForEach-Object { Write-Host "      $_" }

$static = [double](Sql "SELECT COALESCE(SUM(static),0) FROM vote_reward;")
Write-Host "[5/5] 结论"
if ($static -gt 0) {
  Write-Host "      PASS 已产生静态收益 $static（第 1 轮在第 $Rounds 轮结算，三进一出 N+3 生效）" -ForegroundColor Green
} else {
  Write-Warning "      未产生静态收益：请确认轮次数 > 3（当前 $Rounds），且每轮都筹满"
}
# 说明：本驱动用 /ops/setRound 手工建轮，各轮 max 由参数给定（都是 10），所以这里通常显示"未严格递增"。
# 「自动开轮时最高限额递增」由 AutoCreateNextRound 负责，只在真实结算器 cmd/round 的 End 路径生效，
# 断言在 scripts/privchain-e2e.ps1 的第 [6/7] 步（该步会拉起真实 round.exe 跑一轮）。
if ($maxList.Count -gt 1 -and $growOK) {
  Write-Host "      PASS 最高限额逐轮递增（需求#2）" -ForegroundColor Green
} elseif ($maxList.Count -gt 1) {
  Write-Warning "      最高限额未严格递增：$($maxList -join ' → ')"
}
if ($minSame) {
  Write-Host "      PASS 最低限额保持不变（需求#2）" -ForegroundColor Green
} else {
  Write-Warning "      最低限额发生变化：$($minList -join ' → ')"
}
Write-Host ("=" * 70)
