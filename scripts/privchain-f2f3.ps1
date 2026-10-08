# 千万次：F1→F2→F3 晋升链验证（私链专用）
#
# 覆盖需求 #7 的后半段：「3 个 F1 → F2，3 个 F2 → F3」。
# 之前的 privchain-multiteam.ps1 只验证了 F1 晋升（以及「F1 不足 3 个 → 不升 F2」的反例），
# 正例（真的升到 F2 / F3）一直没跑过 —— 本脚本补上。
#
# 晋升口径（core/impl/wallet.go#Promote，与需求逐字对应）：
#   F1：直推 ≥10 且 伞下 ≥30 人 且 该期伞下参投 ≥30 且 伞下历史累计成功 ≥30
#   F2：候选人的**每个直推**的伞下（含自身）只要出现 1 个 F1 就记 1 个；累计 ≥3 → F2
#   F3：同上口径，统计的是伞下是否出现 F2；累计 ≥3 → F3
#
# 账号树（全部是直插库的确定性种子账号，共 346 个）：
#   Z ─┬─ Y1 ─┬─ X11 ─ 10 直推（其中 1 个再挂 20 人）= 30 伞下  → X11 升 F1
#      │      ├─ X12 …                                       → X12 升 F1
#      │      └─ X13 …                                       → X13 升 F1   ⇒ Y1 升 F2（3 个 F1）
#      ├─ Y2 ─ X21/X22/X23 …                                 ⇒ Y2 升 F2
#      ├─ Y3 ─ X31/X32/X33 …                                 ⇒ Y3 升 F2   ⇒ Z 升 F3（3 个 F2）
#      └─ Y4 ─ X41/X42                                       ⇒ 只有 2 个 F1，Y4 **不得**升 F2（负向对照）
#   每个 X 的 30 个伞下账号都投 1 枚 → 同时满足 F1 的「伞下≥30」与「参投/成功≥30」
#
# 用法： & .\scripts\privchain-f2f3.ps1
# 前置：npower :3000、ops :5000 已按 config/etc.privchain.yml 启动
[CmdletBinding()]
param(
  [string]$ApiBase = "http://127.0.0.1:3000",
  [string]$OpsBase = "http://127.0.0.1:5000",
  [string]$MysqlExe = "C:\Program Files\MySQL\MySQL Server 8.0\bin\mysql.exe",
  [string]$Database = "crowd_privchain",
  [string]$ApiKey = "privchain-apikey"
)

$ErrorActionPreference = "Stop"
$RepoRoot = Split-Path -Parent $PSScriptRoot

$pass = 0; $fail = 0
function Step([string]$name, [bool]$ok, [string]$detail = "") {
  if ($ok) { Write-Host "  PASS  $name" -ForegroundColor Green; $script:pass++ }
  else { Write-Host "  FAIL  $name  — $detail" -ForegroundColor Red; $script:fail++ }
}
function Sql([string]$q) {
  $prev = $ErrorActionPreference; $ErrorActionPreference = "SilentlyContinue"
  try { $raw = & $MysqlExe -uroot -p123456 -N -B -e "USE $Database; $q" | Out-String } finally { $ErrorActionPreference = $prev }
  return ($raw -split "`r?`n" | Where-Object { $_ -and $_ -notmatch 'Warning|^mysql:' })
}
function Sql1([string]$q) { return (Sql $q | Select-Object -First 1) }
# ⚠ 必须分批：Windows 命令行上限 32767 字符，一次性拼 300+ 行 VALUES 会被截断
function SqlBatches([string]$prefix, $rows, [int]$size = 100) {
  for ($i = 0; $i -lt $rows.Count; $i += $size) {
    $end = [Math]::Min($i + $size - 1, $rows.Count - 1)
    $chunk = $rows[$i..$end]
    $err = Sql ($prefix + ($chunk -join ","))
    if ($err) { throw "批量写入失败（第 $i 行起）：$err" }
  }
}

# ---------- 账号树规划 ----------
$YCount = 4                                  # Y1..Y3 各带 3 个 X（正例）；Y4 只带 2 个（负向对照，但 Y4 自身也算一个 X 分支）
$XPerY = @(3, 3, 3, 2)
$LeavesPerX = 30                             # 10 个直推 + 其中 1 个再挂 20 人 = 30 个伞下
$TotalX = ($XPerY | Measure-Object -Sum).Sum # 11
$TotalLeaves = $TotalX * $LeavesPerX         # 330
$Total = 1 + $YCount + $TotalX + $TotalLeaves

$idxZ = 0
$idxY = @{}
$n = 1
for ($y = 1; $y -le $YCount; $y++) { $idxY[$y] = $n; $n++ }
$idxX = @{}
for ($y = 1; $y -le $YCount; $y++) {
  for ($j = 1; $j -le $XPerY[$y - 1]; $j++) { $idxX["$y-$j"] = $n; $n++ }
}
$idxLeaf = @{}
for ($y = 1; $y -le $YCount; $y++) {
  for ($j = 1; $j -le $XPerY[$y - 1]; $j++) {
    for ($k = 0; $k -lt $LeavesPerX; $k++) { $idxLeaf["$y-$j-$k"] = $n; $n++ }
  }
}
if ($n -ne $Total) { throw "索引规划错误：分配了 $n 个，期望 $Total 个" }

Write-Host ("=" * 78)
Write-Host "千万次 F1→F2→F3 晋升链验证（$Database，$Total 个测试账号）"
Write-Host ("=" * 78)

# ---------- 0. 前置 ----------
Write-Host "[0/6] 前置检查"
try { $h = (Invoke-RestMethod "$ApiBase/health" -TimeoutSec 8).data; Step "npower 可达（mysql=$($h.checks.mysql)）" ($h.checks.mysql -eq "ok") } catch { Step "npower 可达" $false $_; exit 1 }
try { $null = Invoke-RestMethod "$OpsBase/ops/data/zmy" -TimeoutSec 8; Step "ops 可达" $true } catch { Step "ops 可达" ($null -ne $_.Exception.Response) $_.Exception.Message }
$opsToken = (Invoke-RestMethod "$OpsBase/ops/sign" -Method Post -ContentType "application/json" -Body '{"account":"ops","pwd":"privchain-ops-pwd"}' -TimeoutSec 10).data
Step "ops 登录" ($null -ne $opsToken)

# ---------- 1. 造账号 ----------
Write-Host "[1/6] 生成并入库 $Total 个确定性测试账号"
$acctFile = "scripts\.tmp-f2f3-accounts.json"
$env:PC_ARGS = "{`"n`":$Total,`"seed`":`"f2f3`",`"out`":`"scripts/.tmp-f2f3-accounts.json`"}"
& node (Join-Path $RepoRoot "scripts\privchain-accounts.mjs") | Out-Null
$seeded = Get-Content (Join-Path $RepoRoot $acctFile) -Raw | ConvertFrom-Json
Remove-Item Env:\PC_ARGS
Step "生成 $Total 个确定性账号" ($seeded.Count -eq $Total) "count=$($seeded.Count)"

$addrList = ($seeded | ForEach-Object { "'$($_.address)'" }) -join ","
Sql "DELETE FROM wallet WHERE evm_address IN ($addrList);" | Out-Null
$insRows = @()
for ($i = 0; $i -lt $Total; $i++) {
  $a = $seeded[$i].address
  # ⚠ active 必须置 1：owner 2026-09-30 敲定「直推/伞下按**激活**算」，
  #   F1 门槛（wallet.go GetLevelByInvitee）现在 join wallet 且只数 active=1；
  #   播种成 active=0 会让所有晋升断言失真（原实现数的是注册，故不置也能过）。
  $insRows += "(NOW(),NOW(),'$a','$a','f2f3_$i','F2F3$( '{0:D4}' -f $i )',0,1,0)"
}
SqlBatches "INSERT INTO wallet (created_at,updated_at,address,evm_address,name,code,level,active,admin) VALUES " $insRows 80
$wid = @{}
for ($i = 0; $i -lt $Total; $i++) {
  $id = [int](Sql1 "SELECT id FROM wallet WHERE evm_address='$($seeded[$i].address)';")
  if ($id -le 0) { throw "回查 id 失败：index=$i" }
  $wid[$i] = $id
}
Step "$Total 个账号入库并可回查 id" ($wid.Count -eq $Total)

# ---------- 2. 建层级（闭包表） ----------
Write-Host "[2/6] 建立邀请层级（完整传递闭包）"
# 父节点索引恒小于子节点索引（Z < Y < X < leaves，且 leaf[0] < leaf[10..29]），
# 所以按索引升序即为「父先于子」的拓扑序，无需额外排序。
$parent = @{}
foreach ($y in 1..$YCount) { $parent[$idxY[$y]] = $idxZ }
foreach ($y in 1..$YCount) {
  for ($j = 1; $j -le $XPerY[$y - 1]; $j++) {
    $xi = $idxX["$y-$j"]
    $parent[$xi] = $idxY[$y]
    for ($k = 0; $k -lt $LeavesPerX; $k++) {
      $li = $idxLeaf["$y-$j-$k"]
      # 前 10 个是 X 的直推；其余 20 个挂在该 X 的第 1 个直推下 → X 的伞下恰好 30 人
      if ($k -lt 10) { $parent[$li] = $xi } else { $parent[$li] = $idxLeaf["$y-$j-0"] }
    }
  }
}
if ($parent.Count -ne ($Total - 1)) { throw "父表节点数错误：$($parent.Count)，期望 $($Total - 1)" }

Sql "DELETE FROM wallet_tree;" | Out-Null
$tree = @{}                       # 下标 -> 祖先表(walletId -> distance)
$pending = New-Object System.Collections.ArrayList
foreach ($i in 0..($Total - 1)) {
  $node = $wid[$i]
  $anc = @{}
  if ($parent.ContainsKey($i)) {
    $p = $parent[$i]                                  # 父节点的**下标**
    $anc[$wid[$p]] = 0
    if ($tree.ContainsKey($p)) { foreach ($k in $tree[$p].Keys) { $anc[$k] = $tree[$p][$k] + 1 } }
  }
  $tree[$i] = $anc
  if ($anc.Count -gt 0) {
    foreach ($k in $anc.Keys) { [void]$pending.Add("($k,$node,$($anc[$k]))") }
    if ($pending.Count -ge 500) {
      Sql ("INSERT INTO wallet_tree (ancestor,descendant,distance) VALUES " + ($pending -join ",")) | Out-Null
      $pending.Clear()
    }
  }
}
if ($pending.Count -gt 0) { Sql ("INSERT INTO wallet_tree (ancestor,descendant,distance) VALUES " + ($pending -join ",")) | Out-Null }

$x11 = $idxX["1-1"]
$x11Direct = [int](Sql1 "SELECT COUNT(*) FROM wallet_tree WHERE ancestor=$($wid[$x11]) AND distance=0;")
$x11All = [int](Sql1 "SELECT COUNT(*) FROM wallet_tree WHERE ancestor=$($wid[$x11]);")
$y1Direct = [int](Sql1 "SELECT COUNT(*) FROM wallet_tree WHERE ancestor=$($wid[$idxY[1]]) AND distance=0;")
$zAll = [int](Sql1 "SELECT COUNT(*) FROM wallet_tree WHERE ancestor=$($wid[$idxZ]);")
Step "X11 直推 10 / 伞下 30（正好卡在 F1 条件一二的门槛上）" ($x11Direct -eq 10 -and $x11All -eq 30) "直推=$x11Direct 伞下=$x11All"
Step "Y1 直推 3（F2 看直推子树里有没有 F1）" ($y1Direct -eq 3) "Y1直推=$y1Direct Z伞下=$zAll"

# ---------- 3. 布置项目与轮次 ----------
Write-Host "[3/6] 布置项目/轮次，并给 $TotalLeaves 个伞下账号注入余额"
$bpRows = @()
foreach ($key in $idxLeaf.Keys) { $bpRows += "(NOW(),NOW(),'$($seeded[$idxLeaf[$key]].address)','TM',100000000000)" }
SqlBatches "INSERT INTO wallet_point (created_at,updated_at,address,symbol,amount) VALUES " $bpRows 80
$leafBalance = [int](Sql1 "SELECT COUNT(*) FROM wallet_point WHERE address IN ($addrList);")
Step "$TotalLeaves 个伞下账号余额已注入（每人 1000 TM）" ($leafBalance -ge $TotalLeaves) "rows=$leafBalance"

Sql @"
DELETE FROM vote_reward; DELETE FROM vote; DELETE FROM project_round; DELETE FROM project;
INSERT INTO project (created_at,updated_at,period,symbol,status) VALUES (NOW(),NOW(),1,'TM',1);
"@ | Out-Null
$projId = [int](Sql1 "SELECT id FROM project ORDER BY id DESC LIMIT 1;")
Sql @"
INSERT INTO project_round (created_at,updated_at,period,project_id,round,time_limit,target_vote,min_vote,max_vote,current_vote,start_time,end_time,count,symbol,success,status) VALUES
 (NOW(),NOW(),1,$projId,1,3600,1000,1,100000,0,DATE_SUB(NOW(), INTERVAL 3 HOUR),DATE_SUB(NOW(), INTERVAL 2 HOUR),0,'TM',1,3),
 (NOW(),NOW(),1,$projId,2,3600,1000,1,100000,0,DATE_SUB(NOW(), INTERVAL 2 HOUR),DATE_SUB(NOW(), INTERVAL 1 HOUR),0,'TM',1,3),
 (NOW(),NOW(),1,$projId,3,3600,1000,1,100000,0,DATE_SUB(NOW(), INTERVAL 1 HOUR),DATE_SUB(NOW(), INTERVAL 30 MINUTE),0,'TM',1,3),
 (NOW(),NOW(),1,$projId,4,3600,1000,1,100000,0,NOW(),DATE_ADD(NOW(), INTERVAL 2 HOUR),0,'TM',0,1);
"@ | Out-Null
$r = @{}
foreach ($k in 1..4) { $r[$k] = [int](Sql1 "SELECT id FROM project_round WHERE project_id=$projId AND round=$k;") }
Step "轮次已建立 id=$($r[1]),$($r[2]),$($r[3]),$($r[4])" ($r[1] -gt 0 -and $r[4] -gt 0)

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
# 探测 npower 当前**实际生效**的写限流（响应头被 Go 规范化成 X-Ratelimit-Limit，故正则忽略大小写）
function Get-EffectiveWriteLimit {
  $hdr = & curl.exe -s -i -X POST "$ApiBase/api/participate" -H "X-API-Key: $ApiKey" -H "Content-Type: application/json" `
    --data-binary '{"address":"0x0000000000000000000000000000000000000000","roundId":0,"amount":1,"asset":"TM"}'
  $m = [regex]::Match(($hdr -join "`n"), '(?i)X-Ratelimit-Limit:\s*(\d+)')
  if ($m.Success) { return [int]$m.Groups[1].Value }
  return 0
}

# ---------- 4. 投票（第 4 轮 = 结算器要结束的那一轮） ----------
Write-Host "[4/6] $TotalLeaves 个伞下账号在第 4 轮各投 1 枚"
$writeLimit0 = Get-EffectiveWriteLimit
Write-Host "      测试前 write 限流 = $writeLimit0/分（$TotalLeaves 次投票必须临时调高）"
$paramsBackup = Get-Params $opsToken
$paramsRestore = @{}
foreach ($k in $paramsBackup.Keys) { $paramsRestore[$k] = $paramsBackup[$k] }
# ⚠ 还原必须**显式写回原数值**：ApplyNpowerParams 的约定是「0/空 = 不覆盖」，
#   只把键删掉的话 npower 内存里仍是调高后的值（2026-09-22 实测踩到）
if ($writeLimit0 -gt 0) { $paramsRestore['WriteRatePerMinute'] = $writeLimit0 }
try {
  $paramsTest = @{}
  foreach ($k in $paramsBackup.Keys) { $paramsTest[$k] = $paramsBackup[$k] }
  $paramsTest['WriteRatePerMinute'] = 1200   # $TotalLeaves 次写请求会撞默认的 60/分
  Set-Params $opsToken $paramsTest
  Start-Sleep -Seconds 2
  $limitNow = Get-EffectiveWriteLimit
  Step "写限流已临时调高并实测生效（$limitNow/分 ≥ 需投票数）" ($limitNow -ge $TotalLeaves) "生效值=$limitNow"

  Sql "UPDATE project_round SET status=1, start_time=NOW(), end_time=DATE_ADD(NOW(), INTERVAL 4 HOUR) WHERE id=$($r[4]);" | Out-Null
  $bodyFile = Join-Path $env:TEMP "f2f3-vote.json"
  $okCount = 0
  $failSample = ""
  foreach ($key in $idxLeaf.Keys) {
    @{ address = $seeded[$idxLeaf[$key]].address; roundId = $r[4]; amount = 1; asset = "TM" } | ConvertTo-Json -Compress |
      Out-File -Encoding ascii $bodyFile
    $resp = & curl.exe -s -X POST "$ApiBase/api/participate" -H "X-API-Key: $ApiKey" -H "Content-Type: application/json" --data-binary "@$bodyFile"
    if ($resp -match '"code":0') { $okCount++ } elseif ($failSample -eq "") { $failSample = $resp }
  }
  Step "$TotalLeaves 个伞下账号参投成功（$okCount/$TotalLeaves）" ($okCount -eq $TotalLeaves) "ok=$okCount 首个失败=$failSample"

  # 第 4 轮标记为已成功（Promote 的「伞下历史累计成功」统计的正是 success=1 的轮）
  Sql "UPDATE project_round SET status=3, success=1 WHERE id=$($r[4]);" | Out-Null
  $succ = [int](Sql1 "SELECT COUNT(DISTINCT v.wallet_id) FROM vote v JOIN project_round pr ON pr.id=v.round_id WHERE pr.success=1 AND v.wallet_id IN ($($wid.Values -join ','));")
  Step "伞下累计成功人数 = $succ（F1 需 ≥30）" ($succ -eq $TotalLeaves) "succ=$succ"

  # ---------- 5. 真实结算器触发 Promote ----------
  Write-Host "[5/6] 用真实结算器 cmd/round 结束第 4 轮（Promote 只在这条路径上执行）"
  Sql "UPDATE project_round SET status=1, current_vote=target_vote, start_time=NOW(), end_time=DATE_ADD(NOW(), INTERVAL 5 SECOND) WHERE id=$($r[4]);" | Out-Null
  Sql "UPDATE project_round SET status=2, current_vote=target_vote WHERE id=$($r[1]);" | Out-Null
  $roundExe = Join-Path $RepoRoot "houduan\TiMi\build\round.exe"
  $roundCfg = Join-Path $RepoRoot "houduan\TiMi\config\round.privchain.yml"
  $roundProc = Start-Process -FilePath $roundExe -ArgumentList "-f", $roundCfg `
    -WorkingDirectory (Join-Path $RepoRoot "houduan\TiMi") -WindowStyle Hidden -PassThru
  Start-Sleep -Seconds 75
  Stop-Process -Id $roundProc.Id -Force -ErrorAction SilentlyContinue
  # ⚠ 不能用「第 1 轮有收益行」当结算器跑过的证据：本脚本所有投票都在第 4 轮，
  #   第 1 轮没有投票 → Success(第1轮) 天然产出 0 行（收益是按投票人发的）。
  #   真正的证据是 End 走完后留下的两个痕迹：第 4 轮被置为「已成功 + 待结算」，以及**自动开出第 5 轮**。
  $r4Status = [int](Sql1 "SELECT status FROM project_round WHERE id=$($r[4]);")
  $r4Success = [int](Sql1 "SELECT success FROM project_round WHERE id=$($r[4]);")
  $r5 = "$(Sql1 "SELECT CONCAT(round, '/', target_vote, '/', status) FROM project_round WHERE project_id=$projId AND round=5;")"
  $r1Status = [int](Sql1 "SELECT status FROM project_round WHERE id=$($r[1]);")
  Step "真实结算器已结束第 4 轮（RoundManager.End → Promote 已执行）" ($r4Success -eq 1 -and $r4Status -eq 2) "第4轮 success=$r4Success status=$r4Status"
  Step "已自动开出第 5 轮且 target = 1000×1.3 = 1300" ($r5 -match '^5/1300/') "第5轮 round/target/status=$r5"
  Step "第 1 轮已被结算窗口处理（status=3 已结束；本轮无投票故无收益行）" ($r1Status -eq 3) "第1轮 status=$r1Status"
  Start-Sleep -Seconds 3

  # ---------- 6. 断言 ----------
  Write-Host "[6/6] 晋升结果断言"
  $zLevel = [int](Sql1 "SELECT level FROM wallet WHERE id=$($wid[$idxZ]);")
  $xIds = ($idxX.Keys | ForEach-Object { $wid[$idxX[$_]] }) -join ","
  $yIds = ($idxY.Keys | ForEach-Object { $wid[$idxY[$_]] }) -join ","
  $leafIds = ($idxLeaf.Keys | ForEach-Object { $wid[$idxLeaf[$_]] }) -join ","
  $xGroup = (Sql "SELECT level, COUNT(*) FROM wallet WHERE id IN ($xIds) GROUP BY level;") -join " / "
  $yRows = Sql "SELECT level, COUNT(*) FROM wallet WHERE id IN ($yIds) GROUP BY level;"
  $yLevels = @()
  foreach ($y in 1..$YCount) { $yLevels += [int](Sql1 "SELECT level FROM wallet WHERE id=$($wid[$idxY[$y]]);") }
  $xLevel1 = [int](Sql1 "SELECT COUNT(*) FROM wallet WHERE id IN ($xIds) AND level=1;")
  $xAbove1 = [int](Sql1 "SELECT COUNT(*) FROM wallet WHERE id IN ($xIds) AND level>1;")
  $leafAbove0 = [int](Sql1 "SELECT COUNT(*) FROM wallet WHERE id IN ($leafIds) AND level>0;")
  Write-Host "      Z=level$zLevel ；Y1..Y4 等级=$($yLevels -join ',') ；11 个 X 分级($xGroup) ；330 个叶子中 level>0 个数=$leafAbove0"

  Step "全部 $TotalX 个 X 晋升 F1（level=1）" ($xLevel1 -eq $TotalX) "level1 个数=$xLevel1 分级=($xGroup)"
  Step "X 没有被越级升到 F2/F3" ($xAbove1 -eq 0) "level>1 个数=$xAbove1"
  Step "Y1/Y2/Y3 各带 3 个 F1 → 晋升 F2（level=2）" ($yLevels[0] -eq 2 -and $yLevels[1] -eq 2 -and $yLevels[2] -eq 2) "Y 等级=$($yLevels -join ',')"
  Step "Y4 只带 2 个 F1 → **不**晋升 F2（负向对照，证明阈值是 3 而不是「有就行」）" ($yLevels[3] -eq 0) "Y4 level=$($yLevels[3])"
  Step "Z 伞下有 3 个 F2 → 晋升 F3（level=3）" ($zLevel -eq 3) "Z level=$zLevel"
  Step "$TotalLeaves 个叶子账号均未被误晋升（自身无直推）" ($leafAbove0 -eq 0) "level>0 个数=$leafAbove0"

  Remove-Item $acctFile, $bodyFile -Force -ErrorAction SilentlyContinue
} finally {
  Set-Params $opsToken $paramsRestore
  Start-Sleep -Seconds 2
  $limitAfter = Get-EffectiveWriteLimit
  Step "写限流已还原为测试前的生效值（$writeLimit0/分）" ($limitAfter -eq $writeLimit0) "测试前=$writeLimit0 还原后=$limitAfter"
}

Write-Host ("=" * 78)
Write-Host "结果：PASS=$pass / FAIL=$fail"
Write-Host ("=" * 78)
exit $fail
