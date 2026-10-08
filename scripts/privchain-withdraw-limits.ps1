# 千万次：提现额度三项 —— 真实链路验证（私链专用）
#
# 覆盖「提现额度」这项风控的**两条路径都能按运维参数生效**：
#   1. `/api/withdraw`（DApp 走的适配层）—— 单笔 / 单日累计 / 单日笔数 三项逐一验证
#   2. 阈值为 0（或 -1 显式取消）时确实不限制（用「超额 → 报到余额不足」反证：说明它没被额度拦下）
#
# 为什么不用链：`CheckWithdrawLimits` 在 `/api/withdraw` 里位于**扣账事务与链上广播之前**，
# 被额度拒绝时整笔不受理、不产生任何资金变动。所以本脚本全程不依赖 hardhat 节点，也不会动余额。
#
# ⚠ 参数语义（本轮为此专门补的）：这三个阈值的 0 本身就是"不限制"，
#   若沿用通用的「0 = 不覆盖」，一旦设过上限就再也无法取消。
#   故约定 >0 设定上限、-1 显式取消限制（置 0）、0 不覆盖 —— 脚本结束时用 -1 精确还原。
#
# 用法： & .\scripts\privchain-withdraw-limits.ps1
# 前置：npower :3000 / ops :5000 已按 config/etc.privchain.yml 启动
[CmdletBinding()]
param(
  [string]$ApiBase = "http://127.0.0.1:3000",
  [string]$OpsBase = "http://127.0.0.1:5000",
  [string]$MysqlExe = "C:\Program Files\MySQL\MySQL Server 8.0\bin\mysql.exe",
  [string]$Database = "crowd_privchain",
  [string]$ApiKey = "privchain-apikey"
)

$ErrorActionPreference = "Stop"
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

# 测试账号：专用于额度验证，不与其它脚本的账号冲突
$Addr = "0x00000000000000000000000000000000000000d1"
$Code = "WDLIMIT1"
$TM = 100000000          # config.SymbolDictionary["TM"] = 1e8（wallet_tx.amount 存最小单位）
$BlockOut = 2            # enum.BlockOut

Write-Host ("=" * 74)
Write-Host "千万次 提现额度三项 验证（$Database）"
Write-Host ("=" * 74)

# ---------- 0. 前置 ----------
Write-Host "[0/5] 前置检查"
try { $h = (Invoke-RestMethod "$ApiBase/health" -TimeoutSec 8).data; Step "npower 可达（mysql=$($h.checks.mysql)）" ($h.checks.mysql -eq "ok") } catch { Step "npower 可达" $false $_; exit 1 }
$opsToken = (Invoke-RestMethod "$OpsBase/ops/sign" -Method Post -ContentType "application/json" -Body '{"account":"ops","pwd":"privchain-ops-pwd"}' -TimeoutSec 10).data
Step "ops 登录" ($null -ne $opsToken)

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

# 只改三个额度键，其余键原样保留（/ops/params 是整体覆盖语义）
$paramsBackup = Get-Params $opsToken
function Apply-Limits([double]$single, [double]$daily, [int]$count) {
  $m = @{}
  foreach ($k in $paramsBackup.Keys) { $m[$k] = $paramsBackup[$k] }
  $m['WithdrawSingleMax'] = $single
  $m['WithdrawDailyMax'] = $daily
  $m['WithdrawDailyCountMax'] = $count
  Set-Params $opsToken $m
  Start-Sleep -Seconds 1
}
function Withdraw([double]$amount) {
  $body = @{ address = $Addr; asset = "TM"; amount = $amount; feeToken = "TM" } | ConvertTo-Json -Compress
  $f = Join-Path $env:TEMP "wdlimit.json"
  $body | Out-File -Encoding ascii $f
  $resp = & curl.exe -s -X POST "$ApiBase/api/withdraw" -H "X-API-Key: $ApiKey" -H "Content-Type: application/json" --data-binary "@$f"
  Remove-Item $f -Force -ErrorAction SilentlyContinue
  return $resp
}

# ---------- 1. 造测试账号 ----------
Write-Host "[1/5] 造测试账号（余额充足，确保只有额度能拦住它）"
# ⚠ here-string 走的是双引号语义：里面的反引号是转义符（`f = 换页符、`t = 制表符），
#   想输出字面反引号必须写成两个反引号，否则 SQL 会被静默写坏（本次踩到）。
Sql @"
DELETE FROM wallet_tx WHERE ``from``='$Addr' OR ``to``='$Addr';
DELETE FROM wallet_point WHERE address='$Addr';
DELETE FROM wallet WHERE evm_address='$Addr';
INSERT INTO wallet (created_at,updated_at,address,evm_address,name,code,level,active,admin)
VALUES (NOW(),NOW(),'$Addr','$Addr','wdlimit','$Code',0,1,0);
INSERT INTO wallet_point (created_at,updated_at,address,symbol,amount) VALUES
 (NOW(),NOW(),'$Addr','TM',100000000000),(NOW(),NOW(),'$Addr','FIBO',100000000000);
"@ | Out-Null
$ok = ([int](Sql1 "SELECT COUNT(*) FROM wallet WHERE evm_address='$Addr';") -eq 1) -and ([int](Sql1 "SELECT COUNT(*) FROM wallet_point WHERE address='$Addr';") -eq 2)

try {
  Step "测试账号与余额就绪（TM/FIBO 各 1000 枚）" $ok

  # ---------- 2. 单笔上限 ----------
  Write-Host "[2/5] 单笔上限"
  Apply-Limits 0.5 0 0
  $r = Withdraw 10
  Step "单笔上限 0.5 时提 10 → 被拒（单笔提现超过上限 0.5）" ($r -match '单笔提现超过上限') "响应=$r"
  $r2 = Withdraw 0.3
  Step "同一阈值下提 0.3 → 不被单笔上限拦下" ($r2 -notmatch '单笔提现超过上限') "响应=$r2"

  # ---------- 3. 单日累计 ----------
  Write-Host "[3/5] 单日累计上限"
  Apply-Limits -1 5 0
  # 造一笔「今天已提 4 枚」的流水（额度统计口径：wallet_tx from=本人 + type=BlockOut + 今天）
  Sql "INSERT INTO wallet_tx (created_at,updated_at,``from``,``to``,symbol,amount,``desc``,hash,success,type) VALUES (NOW(),NOW(),'$Addr','$Addr','TM',$(4 * $TM),'额度测试流水','wdlimit-seed',0,$BlockOut);" | Out-Null
  $seeded = [double]([long](Sql1 "SELECT COALESCE(SUM(amount),0) FROM wallet_tx WHERE ``from``='$Addr' AND type=$BlockOut AND created_at>=CURDATE();")) / $TM
  Step "已造一笔今日流水 4 枚（额度统计能读到）" ([Math]::Abs($seeded - 4) -lt 0.001) "统计到=$seeded"
  $r = Withdraw 3
  Step "单日累计上限 5、今日已提 4 时再提 3 → 被拒（今日提现累计已达上限 5）" ($r -match '今日提现累计已达上限') "响应=$r"
  $r2 = Withdraw 0.5
  Step "今日累计 4 + 0.5 ≤ 5 → 不被累计上限拦下" ($r2 -notmatch '今日提现累计已达上限') "响应=$r2"

  # ---------- 4. 单日笔数 ----------
  Write-Host "[4/5] 单日笔数上限"
  Apply-Limits -1 0 2
  Sql "INSERT INTO wallet_tx (created_at,updated_at,``from``,``to``,symbol,amount,``desc``,hash,success,type) VALUES (NOW(),NOW(),'$Addr','$Addr','TM',$TM,'额度测试流水2','wdlimit-seed2',0,$BlockOut);" | Out-Null
  $cnt = [int](Sql1 "SELECT COUNT(*) FROM wallet_tx WHERE ``from``='$Addr' AND type=$BlockOut AND created_at>=CURDATE();")
  Step "今日已有 2 笔提现流水" ($cnt -eq 2) "笔数=$cnt"
  $r = Withdraw 0.5
  Step "单日笔数上限 2、今日已 2 笔 → 被拒（今日提现笔数已达上限 2）" ($r -match '今日提现笔数已达上限') "响应=$r"

  # ---------- 5. 取消限制 / 还原 ----------
  Write-Host "[5/5] 取消限制（-1 = 显式不限制）后应放行到后续业务校验"
  Apply-Limits -1 -1 -1
  $r = Withdraw 999999
  Step "取消三项限制后，超额请求不再被额度拦下（报到余额/手续费不足即证明放行）" `
    (($r -notmatch '提现超过上限') -and ($r -notmatch '已达上限') -and ($r -match '不足|余额|手续费')) "响应=$r"
  $r2 = Withdraw 1
  Step "取消限制后小额提现不再出现任何额度类拒绝" `
    (($r2 -notmatch '提现超过上限') -and ($r2 -notmatch '已达上限')) "响应=$r2"
} finally {
  # 还原：把三个额度键写回测试前的取值；备份里没有该键（= 代码默认不限）时用 -1 显式取消
  $restore = @{}
  foreach ($k in $paramsBackup.Keys) { $restore[$k] = $paramsBackup[$k] }
  foreach ($pair in @(@('WithdrawSingleMax', -1), @('WithdrawDailyMax', -1), @('WithdrawDailyCountMax', -1))) {
    $key = $pair[0]
    $restore[$key] = if ($paramsBackup.ContainsKey($key)) { $paramsBackup[$key] } else { $pair[1] }
  }
  Set-Params $opsToken $restore
  Start-Sleep -Seconds 1
  # 清掉测试账号与它造的流水
  Sql "DELETE FROM wallet_tx WHERE ``from``='$Addr' OR ``to``='$Addr'; DELETE FROM wallet_point WHERE address='$Addr'; DELETE FROM wallet WHERE evm_address='$Addr';" | Out-Null
  $got = Get-Params $opsToken
  Step "运行参数已还原（三项额度写回备份值或显式取消）" `
    (($got['WithdrawSingleMax'] -eq (Get-ParamValue $paramsBackup 'WithdrawSingleMax')) -or (-not $paramsBackup.ContainsKey('WithdrawSingleMax'))) `
    ($got | ConvertTo-Json -Compress)
}

Write-Host ("=" * 74)
Write-Host "结果：PASS=$pass / FAIL=$fail"
Write-Host ("=" * 74)
exit $fail
