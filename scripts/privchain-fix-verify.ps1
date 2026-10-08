# ============================================================================
#  千万次（TiMi）- 2026-09-30 修复项回归验证脚本
#
#  验证内容（全部为本次修复的资金/鉴权缺陷）：
#   [1] /api 写接口在"未开启联调开关"时必须要求钱包签名会话（HTTP 401）
#   [2] /api 写接口在"开启联调开关（AllowUnsignedWrite）"时沿用 body 地址 —— 保证私链脚本可用
#   [3] ops 运维后台鉴权不能被查询串绕过（`?x=/ops/data` 曾经可跳过 JWT，实测复现 200）
#   [4] 倒2/倒3 爆仓结算：**退还未被扣除的另一半** + 被扣部分折算算力（需求#5/#7）
#   [5] 爆仓价不可用时不再整笔回滚（旧实现 return err → 该轮永久停在待结算）
#   [6] 结算幂等：同一轮重复触发不会重复退款/重复折算算力
#
#  前置：MySQL(3306) + Redis(6379) 已启动；库 crowd_privchain 存在；
#        build\npower.exe 与 build\ops.exe 已按最新源码构建。
#  用法：powershell -ExecutionPolicy Bypass -File scripts\privchain-fix-verify.ps1
#
#  ⚠ 本脚本会：在 crowd_privchain 里新建一个临时 project（period=9901）与相关轮次/投票，
#     并在结束时删除；同时启动/停止 npower 与 ops 进程。
# ============================================================================

$ErrorActionPreference = 'Continue'
$Root = Split-Path -Parent $PSScriptRoot
$Db = 'crowd_privchain'
$MySqlArgs = @('-h', '127.0.0.1', '-uroot', '-p123456', '--default-character-set=utf8mb4', $Db, '-N', '-B')

$script:Pass = 0
$script:Fail = 0
function Step([string]$name, [bool]$ok, [string]$detail = '') {
  if ($ok) { $script:Pass++; Write-Host ("  PASS  {0} {1}" -f $name, $detail) -ForegroundColor Green }
  else { $script:Fail++; Write-Host ("  FAIL  {0} {1}" -f $name, $detail) -ForegroundColor Red }
}
function Warn([string]$msg) { Write-Host ("  WARN  " + $msg) -ForegroundColor Yellow }
function Info([string]$msg) { Write-Host ("  --    " + $msg) -ForegroundColor DarkGray }

# ---- mysql 查询：返回字符串数组（自动忽略 stderr 上的密码告警）----
function Sql([string]$sql) {
  $out = & mysql @MySqlArgs -e $sql 2>$null
  return $out
}
function SqlScalar([string]$sql) {
  $v = Sql $sql
  if ($null -eq $v) { return '' }
  if ($v -is [array]) { if ($v.Count -eq 0) { return '' } else { return ([string]$v[0]).Trim() } }
  return ([string]$v).Trim()
}
function AssertEq([string]$name, $expect, $actual, [string]$detail = '') {
  Step $name ([string]$expect -eq [string]$actual) ("期望=$expect 实际=$actual " + $detail)
}

# ---- 临时候选配置：从私链配置复制，仅改日志路径/端口/联调开关/读接口会话开关 ----
function New-ProbeConfig([string]$file, [string]$logName, [string]$port, [string]$allowUnsigned, [string]$enforceSession = '', [string]$withdrawMin = '') {
  $src = Join-Path $Root 'houduan\TiMi\config\etc.privchain.yml'
  $txt = [System.IO.File]::ReadAllText($src)
  $txt = $txt -replace '(?m)^HttpPort:.*$', ("HttpPort: `"$port`"")
  $txt = $txt -replace '(?m)^LogPath:.*$', ("LogPath: $logName")
  $txt = $txt -replace '(?m)^(\s*)AllowUnsignedWrite:.*$', ('$1AllowUnsignedWrite: ' + $allowUnsigned)
  if ($enforceSession -ne '') {
    # 读接口是否也强制会话（生产模板 etc.yml 已置 true，这里用同口径实例做回归）
    $txt = $txt -replace '(?m)^(\s*)EnforceSession:.*$', ('$1EnforceSession: ' + $enforceSession)
  }
  if ($withdrawMin -ne '') {
    # 最低起提额（owner 2026-09-30 敲定 100 个起提）：私链配置里是 -1（关闭），这里用生产口径覆盖。
    # ⚠ 它必须写在 Params 段内（NpowerParams）且**带缩进**匹配 —— 写成顶层键不生效（实测被 E2E 抓到）
    if ($txt -match '(?m)^\s*WithdrawMinAmount:') {
      $txt = $txt -replace '(?m)^(\s*)WithdrawMinAmount:.*$', ('$1WithdrawMinAmount: ' + $withdrawMin)
    } else {
      $txt = $txt -replace '(?m)^(Params:\s*)$', ("`$1`r`n  WithdrawMinAmount: $withdrawMin")
    }
  }
  [System.IO.File]::WriteAllText((Join-Path $Root $file), $txt, (New-Object System.Text.UTF8Encoding($false)))
  return (Join-Path $Root $file)
}

Write-Host "`n=== 千万次修复项回归验证 ===" -ForegroundColor Cyan

# ---------------------------------------------------------------- 0. 环境检查
Write-Host "`n[0] 环境检查" -ForegroundColor Cyan
if (-not (Get-Command mysql -ErrorAction SilentlyContinue)) { Write-Host "缺少 mysql 客户端" -ForegroundColor Red; exit 1 }
$np = Join-Path $Root 'houduan\TiMi\build\npower.exe'
$op = Join-Path $Root 'houduan\TiMi\build\ops.exe'
if (-not (Test-Path $np)) { Write-Host "缺少 $np（先构建）" -ForegroundColor Red; exit 1 }
if (-not (Test-Path $op)) { Write-Host "缺少 $op（先构建）" -ForegroundColor Red; exit 1 }
AssertEq "MySQL 库存在" $Db (SqlScalar "SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='$Db'")

# 清理可能残留的验证数据
$cleanupSql = "DELETE FROM vote WHERE project_id IN (SELECT id FROM project WHERE period=9901); " +
  "DELETE FROM vote_reward WHERE project_id IN (SELECT id FROM project WHERE period=9901); " +
  "DELETE FROM project_round WHERE project_id IN (SELECT id FROM project WHERE period=9901); " +
  "DELETE FROM project WHERE period=9901;"
Sql $cleanupSql | Out-Null

# ------------------------------------------------- 1. 严格模式（不开启联调开关，且读接口也强制会话 = 生产模板口径）
Write-Host "`n[1] /api 鉴权（AllowUnsignedWrite=false + EnforceSession=true，等价 etc.yml 生产模板）" -ForegroundColor Cyan
$strictCfg = New-ProbeConfig 'ncf-verify-strict.yml' 'ncf-verify-strict.log' ':3010' 'false' 'true'
$devCfg = New-ProbeConfig 'ncf-verify-dev.yml' 'ncf-verify-dev.log' ':3011' 'true' '' '100'
$opsCfg = New-ProbeConfig 'ncf-verify-ops.yml' 'ncf-verify-ops.log' ':5010' 'false'

$strictProc = Start-Process -FilePath $np -ArgumentList @('-f', $strictCfg) -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 6
$ApiKey = 'privchain-apikey'
function Invoke-Api([string]$uri, [string]$method = 'GET', $body = $null, [switch]$NoKey) {
  # -NoKey 时不带 X-API-Key（用于验证"无 key 被拒"）
  try {
    $h = @{}
    if (-not $NoKey) { $h['X-API-Key'] = $ApiKey }
    if ($body) {
      return Invoke-WebRequest -Uri $uri -Method $method -Headers $h -Body $body -ContentType 'application/json' -UseBasicParsing -TimeoutSec 10
    }
    return Invoke-WebRequest -Uri $uri -Method $method -Headers $h -UseBasicParsing -TimeoutSec 10
  } catch {
    return $_.Exception.Response
  }
}
function CodeOf($resp) {
  if ($null -eq $resp -or -not $resp.Content) { return '' }
  try { return [string](($resp.Content | Out-String | ConvertFrom-Json).code) } catch { return '' }
}

$r = Invoke-Api 'http://127.0.0.1:3010/api/prices' 'GET' $null -NoKey
AssertEq "无 X-API-Key 被拒（code=600）" '600' (CodeOf $r)
$r = Invoke-Api 'http://127.0.0.1:3010/api/prices'
AssertEq "带正确 X-API-Key 放行（code=0）" '0' (CodeOf $r)

$r = Invoke-Api 'http://127.0.0.1:3010/api/participate' 'POST' '{"address":"0x000000000000000000000000000000000000dEaD","roundId":1,"amount":1,"asset":"TM"}'
$status = 0
if ($r -is [System.Net.HttpWebResponse]) { $status = [int]$r.StatusCode } elseif ($r) { $status = [int]$r.StatusCode }
Step "[1] 无会话写请求被拒（HTTP 401，修复前为 200 且进入业务逻辑）" ($status -eq 401) ("HTTP=$status")

# 读接口在生产模板口径（EnforceSession=true）下也必须带会话票：
# 原先读接口匿名可读（凭 X-API-Key 即可按任意地址查资产/团队），等于把用户资产与伞下关系公开。
$rReadNoSess = Invoke-Api 'http://127.0.0.1:3010/api/assets?address=0x000000000000000000000000000000000000dEaD'
$readStatus = 0
if ($rReadNoSess) { $readStatus = [int]$rReadNoSess.StatusCode }
Step "[1b] 无会话读 /api/assets 被拒（HTTP 401；EnforceSession=true 口径）" ($readStatus -eq 401) ("HTTP=$readStatus")
$rReadMiner = Invoke-Api 'http://127.0.0.1:3010/api/miner?address=0x000000000000000000000000000000000000dEaD'
$minerStatus = 0
if ($rReadMiner) { $minerStatus = [int]$rReadMiner.StatusCode }
Step "[1c] 无会话读 /api/miner 被拒（HTTP 401）" ($minerStatus -eq 401) ("HTTP=$minerStatus")
# 公共只读接口（价格/轮次/规则）不应被会话限制影响：它们不含用户隐私，且前端首屏需要
$rPrices = Invoke-Api 'http://127.0.0.1:3010/api/rules'
AssertEq "[1d] 公共只读接口 /api/rules 无需会话（code=0）" '0' (CodeOf $rPrices)
# ⚠ 严格实例保持运行到脚本结束：后面的 [7] 要用它验证「读接口会话地址一致性」。
#   原实现此处就把它停掉，导致后续无法复用（只能另起进程）。

# ------------------------------------------------- 2. 联调模式（脚本可用性）
Write-Host "`n[2] /api 写接口联调开关（AllowUnsignedWrite=true，私链脚本口径）" -ForegroundColor Cyan
$devProc = Start-Process -FilePath $np -ArgumentList @('-f', $devCfg) -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 6
$r = Invoke-Api 'http://127.0.0.1:3011/api/participate' 'POST' '{"address":"0x000000000000000000000000000000000000dEaD","roundId":1,"amount":1,"asset":"TM"}'
$code2 = CodeOf $r
Step "[2] 联调模式下写请求放行到业务逻辑（body 地址被信任）" ($code2 -eq '600') ("code=$code2（600=钱包不存在，说明未被鉴权拦截）")

# ---- [2b] 最低起提额（owner 2026-09-30 敲定：100 个起提）----
# dev 实例被显式覆盖为 WithdrawMinAmount=100（私链配置里是 -1=关闭），因此这里就是生产口径。
# 同时把出金模式改成 ledger（只记账不上链）：这样下面的"大额提现"断言不会被热钱包链上余额干扰，
# 测的是**额度规则**而不是链上余额。
$devTxt = [System.IO.File]::ReadAllText($devCfg)
$devTxt = $devTxt -replace '(?m)^(\s*)Mode:\s*"broadcast".*$', '$1Mode: "ledger"'
[System.IO.File]::WriteAllText($devCfg, $devTxt, (New-Object System.Text.UTF8Encoding($false)))
$minWallet = '0x00000000000000000000000000000000000000b7'
Sql "DELETE FROM wallet_point WHERE address='$minWallet'; DELETE FROM wallet WHERE address='$minWallet';" | Out-Null
Sql "INSERT INTO wallet (created_at,updated_at,address,evm_address,name,code,level,active,admin) VALUES (NOW(3),NOW(3),'$minWallet','$minWallet','minwd','MINWD',0,1,0);" | Out-Null
Sql "INSERT INTO wallet_point (created_at,updated_at,address,symbol,amount) VALUES (NOW(3),NOW(3),'$minWallet','TM',5000000000000);" | Out-Null  # 50000 枚 TM（精度 1e8）
$maxTxMin = [long](SqlScalar "SELECT COALESCE(MAX(id),0) FROM wallet_tx;")
$rLow = Invoke-Api 'http://127.0.0.1:3011/api/withdraw' 'POST' ('{"address":"' + $minWallet + '","asset":"TM","amount":50,"feeToken":"TM"}')
$lowBody = ($rLow.Content | Out-String)
Step "[2b] 提 50 个被最低起提拦下" (($lowBody -match '最低起提')) ("resp=" + $lowBody.Trim())
$rHigh = Invoke-Api 'http://127.0.0.1:3011/api/withdraw' 'POST' ('{"address":"' + $minWallet + '","asset":"TM","amount":100,"feeToken":"TM"}')
$highBody = ($rHigh.Content | Out-String)
Step "[2c] 提 100 个不再被最低起提拦下" (-not ($highBody -match '最低起提')) ("resp=" + $highBody.Trim())

# ---- [2e] 无上限（owner 2026-09-30 裁定：单笔不封顶 / 单日不限 / 不设笔数）----
# 大额提现（15000 枚，远超历史上那条 10000 的硬编码兜底）必须**不被任何额度规则**拦下。
# 用 ledger 模式跑，避免热钱包链上余额把断言带偏 —— 这里要测的是"额度规则放行"。
$rBig = Invoke-Api 'http://127.0.0.1:3011/api/withdraw' 'POST' ('{"address":"' + $minWallet + '","asset":"TM","amount":15000,"feeToken":"TM"}')
$bigBody = ($rBig.Content | Out-String)
Step "[2e] 提 15000 个不被额度规则拦下（无上限）" (-not ($bigBody -match '上限|次数|最低起提')) ("resp=" + $bigBody.Trim())
# 连提多笔也不受笔数限制（历史上原生路径硬卡 3 次/24h）
$multiOk = $true
for ($i = 1; $i -le 5; $i++) {
  $rm = Invoke-Api 'http://127.0.0.1:3011/api/withdraw' 'POST' ('{"address":"' + $minWallet + '","asset":"TM","amount":100,"feeToken":"TM"}')
  $mb = ($rm.Content | Out-String)
  if ($mb -match '上限|次数') { $multiOk = $false; Write-Host "      第 $i 笔被额度拦下：$($mb.Trim())" }
}
Step "[2f] 连续 5 笔提现均不受笔数/次数限制" $multiOk

# 代码级防回归：历史上的两条硬编码（10000 兜底 / 最多 3 次每 24h）必须已经不在
# 只看**真实代码行**（去掉 // 注释）：我自己的说明注释里也提到了这两个常量名，不能算命中
  $walletLines = [System.IO.File]::ReadAllLines((Join-Path $Root 'houduan\TiMi\http\handler\wallet.go'))
  $walletSrc = (($walletLines | Where-Object { $_.Trim() -notlike '//*' }) -join "`n")
Step "[2g] 遗留硬编码上限已移除（legacyWithdrawHardCap / maxWithdrawCount 均已删）" ((-not ($walletSrc -match 'legacyWithdrawHardCap')) -and (-not ($walletSrc -match 'maxWithdrawCount'))) "源码里不应再出现这两个常量"

# 清理临时钱包与它产生的流水（这些提现只是为验证额度规则，不是业务数据）
Sql "DELETE FROM wallet_tx WHERE id > $maxTxMin AND (``from``='$minWallet' OR ``to``='$minWallet');" | Out-Null
Sql "DELETE FROM wallet_point WHERE address='$minWallet'; DELETE FROM wallet WHERE address='$minWallet';" | Out-Null
Step "[2d] 临时钱包已清理" ('0' -eq (SqlScalar "SELECT COUNT(*) FROM wallet WHERE address='$minWallet'"))
$devProc | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 1

# ------------------------------------------------------------ 3. ops 鉴权绕过
Write-Host "`n[3] ops 运维后台鉴权（查询串绕过）" -ForegroundColor Cyan
$opsProc = Start-Process -FilePath $op -ArgumentList @('-f', $opsCfg, '-port', ':5010') -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 6
function TryOps([string]$uri, [string]$method = 'GET', $body = $null, [hashtable]$headers = @{}) {
  try {
    if ($body) { return Invoke-WebRequest -Uri $uri -Method $method -Headers $headers -Body $body -ContentType 'application/json' -UseBasicParsing -TimeoutSec 10 }
    return Invoke-WebRequest -Uri $uri -Method $method -Headers $headers -UseBasicParsing -TimeoutSec 10
  } catch { return $_.Exception.Response }
}
function StatusOf($resp) { if ($null -eq $resp) { return 0 } try { return [int]$resp.StatusCode } catch { return 0 } }

$r1 = TryOps 'http://127.0.0.1:5010/ops/params'
Step "[3a] 无 token 访问 /ops/params 被拒" ((StatusOf $r1) -ne 200) ("HTTP=$(StatusOf $r1)")
$r2 = TryOps 'http://127.0.0.1:5010/ops/params?probe=/ops/data'
Step "[3b] 查询串绕过已修复：/ops/params?probe=/ops/data 不再放行" ((StatusOf $r2) -ne 200) ("HTTP=$(StatusOf $r2)（修复前为 200 且返回真实参数）")
$r3 = TryOps 'http://127.0.0.1:5010/ops/audit/list?x=/ops/data'
Step "[3c] 查询串绕过已修复：/ops/audit/list?x=/ops/data 不再放行" ((StatusOf $r3) -ne 200) ("HTTP=$(StatusOf $r3)")
$r4 = TryOps 'http://127.0.0.1:5010/ops/data/zmy'
Step "[3d] 精确白名单仍有意放行 /ops/data/zmy（只读分析）" ((StatusOf $r4) -eq 200) ("HTTP=$(StatusOf $r4)")

# 正确登录后应可用（确认修复没有把正常运维挡死）
$sign = TryOps 'http://127.0.0.1:5010/ops/sign' 'POST' '{"account":"ops","pwd":"privchain-ops-pwd"}'
$token = ''
try { $token = (($sign.Content | Out-String) | ConvertFrom-Json).data } catch { }
Step "[3e] /ops/sign 登录成功并拿到 token" ([bool]$token)
$opsHdr = @{ Authorization = ('Bearer ' + $token) }
$r5 = TryOps 'http://127.0.0.1:5010/ops/params' 'GET' $null $opsHdr
Step "[3f] 带 token 访问 /ops/params 正常（200）" ((StatusOf $r5) -eq 200) ("HTTP=$(StatusOf $r5)")

# --------------------------------------------- 4. 倒2/倒3 爆仓结算（退一半+折算算力）
Write-Host "`n[4] 倒2/倒3 爆仓结算：退还未扣部分 + 被扣部分折算算力" -ForegroundColor Cyan

$walletId = SqlScalar "SELECT id FROM wallet ORDER BY id LIMIT 1"
$walletAddr = SqlScalar "SELECT address FROM wallet ORDER BY id LIMIT 1"
Info "使用钱包 id=$walletId address=$walletAddr"

function New-FailedProbe([string]$symbol, [int]$amount, [int]$roundNo) {
  Sql ("INSERT INTO project (period, symbol, status, created_at, updated_at) VALUES (9901, '$symbol', 1, NOW(3), NOW(3));") | Out-Null
  $pid9901 = SqlScalar "SELECT id FROM project WHERE period=9901 ORDER BY id DESC LIMIT 1"
  Sql ("INSERT INTO project_round (project_id, period, round, target_vote, current_vote, min_vote, max_vote, symbol, status, success, start_time, end_time, time_limit, created_at, updated_at) " +
    "VALUES ($pid9901, 9901, $roundNo, 100000, 0, 1, 100000, '$symbol', 2, 0, NOW(3), NOW(3), 3600, NOW(3), NOW(3));") | Out-Null
  $rid = SqlScalar "SELECT id FROM project_round WHERE project_id=$pid9901 ORDER BY id DESC LIMIT 1"
  Sql ("INSERT INTO vote (wallet_id, address, project_id, round_id, round, period, symbol, amount, `count`, is_admin, level, created_at, updated_at) " +
    "VALUES ($walletId, '$walletAddr', $pid9901, $rid, $roundNo, 9901, '$symbol', $amount, 1, 0, 0, NOW(3), NOW(3));") | Out-Null
  # 确保余额账户存在
  Sql ("INSERT INTO wallet_point (address, symbol, amount, created_at, updated_at) SELECT '$walletAddr', '$symbol', 0, NOW(3), NOW(3) " +
    "WHERE NOT EXISTS (SELECT 1 FROM wallet_point WHERE address='$walletAddr' AND symbol='$symbol');") | Out-Null
  return @{ projectId = $pid9901; roundId = $rid }
}
function BalanceOf([string]$symbol) {
  return [double](SqlScalar "SELECT COALESCE(SUM(amount),0) FROM wallet_point WHERE address='$walletAddr' AND symbol='$symbol'")
}

# ---- 4a. TM（有静态兜底价 3.4U）→ 应退 50%、另 50% 折算算力 ----
$p = New-FailedProbe 'TM' 100 910
$before = BalanceOf 'TM'
$hpBefore = [int](SqlScalar "SELECT COUNT(*) FROM hash_power WHERE address='$walletAddr'")
$body = ('{"roundId":' + $p.roundId + ',"act":"Failed"}')
$r6 = TryOps 'http://127.0.0.1:5010/ops/endRound' 'POST' $body $opsHdr
Step "[4a] /ops/endRound(Failed) 调用成功" ((StatusOf $r6) -eq 200) ("HTTP=$(StatusOf $r6)")
Start-Sleep -Seconds 2

$after = BalanceOf 'TM'
$delta = ($after - $before) / 1e8
AssertEq "[4a] 退还 50%（100 TM 仓位 → 退 50）" '50' $delta
# 注意：SQL 里的反引号在 PowerShell 双引号串中是转义符，必须写成双反引号（或改用无引号列名）
$rid1 = $p.roundId
$tx9 = [int](SqlScalar "SELECT COUNT(*) FROM wallet_tx WHERE ``to``='$walletAddr' AND type=9 AND amount=5000000000")
AssertEq "[4a] 写入 type=9（失败50%退还）流水" '1' $tx9
$loss = SqlScalar "SELECT COALESCE(SUM(loss),0) FROM vote_reward WHERE round_id=$rid1"
AssertEq "[4a] vote_reward.loss = 被扣部分（50）" '50' $loss
$hpAfter = [int](SqlScalar "SELECT COUNT(*) FROM hash_power WHERE address='$walletAddr'")
AssertEq "[4a] 新增 1 个算力账户（被扣 50 × 3.4U = 170 算力）" ($hpBefore + 1) $hpAfter
$power = SqlScalar "SELECT power FROM hash_power WHERE address='$walletAddr' ORDER BY id DESC LIMIT 1"
AssertEq "[4a] 算力折算正确（power = 170）" '170' $power
$st = SqlScalar "SELECT status FROM project_round WHERE id=$rid1"
AssertEq "[4a] 轮次标记结束（status=3）" '3' $st

# ---- 4b. 幂等：重复触发不得重复退款/折算 ----
$balBefore2 = BalanceOf 'TM'
$hpBefore2 = [int](SqlScalar "SELECT COUNT(*) FROM hash_power WHERE address='$walletAddr'")
TryOps 'http://127.0.0.1:5010/ops/endRound' 'POST' $body $opsHdr | Out-Null
Start-Sleep -Seconds 2
AssertEq "[4b] 重复结算不重复退款" $balBefore2 (BalanceOf 'TM')
AssertEq "[4b] 重复结算不重复折算算力" $hpBefore2 ([int](SqlScalar "SELECT COUNT(*) FROM hash_power WHERE address='$walletAddr'"))

# ---- 4c. FIBO（无行情、FiboStaticPrice=0）→ 不得整笔回滚：本金守恒且轮次结束 ----
# 计量口径：以**账本**（wallet_tx 新增行）为准，而不是"余额前后差"。
# 原因：余额是跨运行累积的，且应用侧 AddPointAmount 与实际落库存在多行/口径差异，
# 用差值容易被历史数据污染；wallet_tx 是退款的权威记录（type=8 全额退 / type=9 扣半后退）。
$p2 = New-FailedProbe 'FIBO' 100 911
$rid2 = $p2.roundId
$hpBefore3 = [int](SqlScalar "SELECT COUNT(*) FROM hash_power WHERE address='$walletAddr'")
$maxTxBefore = [int](SqlScalar "SELECT COALESCE(MAX(id),0) FROM wallet_tx")
$body2 = ('{"roundId":' + $rid2 + ',"act":"Failed"}')
$r7 = TryOps 'http://127.0.0.1:5010/ops/endRound' 'POST' $body2 $opsHdr
Step "[4c] FIBO 爆仓结算调用成功" ((StatusOf $r7) -eq 200) ("HTTP=$(StatusOf $r7)")
Start-Sleep -Seconds 2
# 本次结算新增的"退还给该地址"的流水（type=9 倒2/倒3 退还；type=8 全额退还）
$refund = [double](SqlScalar "SELECT COALESCE(SUM(amount),0) FROM wallet_tx WHERE id > $maxTxBefore AND ``to``='$walletAddr' AND type IN (8,9)") / 1e8
$hpDelta = [int](SqlScalar "SELECT COUNT(*) FROM hash_power WHERE address='$walletAddr'") - $hpBefore3
$loss2 = [double](SqlScalar "SELECT COALESCE(SUM(loss),0) FROM vote_reward WHERE round_id=$rid2")
$st2 = SqlScalar "SELECT status FROM project_round WHERE id=$rid2"
Info ("本次账本退款=$refund 被扣=$loss2 新增算力账户=$hpDelta")
AssertEq "[4c] 轮次未回滚（status=3，旧实现会因取价失败整笔回滚卡在 2）" '3' $st2
Step "[4c] 本金守恒：退还额 + 被扣额 = 100" ([math]::Abs(($refund + $loss2) - 100) -lt 0.000001) ("退还=$refund 被扣=$loss2")
Step "[4c] FIBO 无价时不把用户资产扣成悬空（退还+算力可解释）" (($refund -eq 100 -and $hpDelta -eq 0) -or ($refund -eq 50 -and $hpDelta -eq 1 -and $loss2 -eq 50)) ("退还=$refund 新增算力=$hpDelta 被扣=$loss2")

# ------------------------------------------------- 5. /ops/params 合并语义（不再整体覆盖）
Write-Host "`n[5] /ops/params 合并语义（原实现会"改一个键、抹掉一串键"）" -ForegroundColor Cyan
$beforeParams = (TryOps 'http://127.0.0.1:5010/ops/params' 'GET' $null $opsHdr).Content | Out-String | ConvertFrom-Json
$keysBefore = @($beforeParams.data.params.PSObject.Properties.Name)
Info ("写入前 params 键：" + ($keysBefore -join ','))
# 只写一个与业务无关的键，验证既有键不被抹掉
$patchBody = '{"HashPowerPoolDailyRatio":0.3}'
$setRes = TryOps 'http://127.0.0.1:5010/ops/params' 'POST' $patchBody $opsHdr
Step "[5a] 局部写入返回 200" ((StatusOf $setRes) -eq 200) ("HTTP=$(StatusOf $setRes)")
$afterParams = (TryOps 'http://127.0.0.1:5010/ops/params' 'GET' $null $opsHdr).Content | Out-String | ConvertFrom-Json
$keysAfter = @($afterParams.data.params.PSObject.Properties.Name)
$lost = @($keysBefore | Where-Object { $_ -notin $keysAfter })
Step "[5b] 既有键未被抹掉（合并语义）" ($lost.Count -eq 0) ("丢失的键：" + ($lost -join ','))
Step "[5c] 新键已写入" ($keysAfter -contains 'HashPowerPoolDailyRatio')
# 还原：把备份里的键逐个写回（合并语义下只覆盖这些键）
if ($keysBefore.Count -gt 0) {
  $restore = @{}
  foreach ($k in $keysBefore) { $restore[$k] = $beforeParams.data.params.$k }
  TryOps 'http://127.0.0.1:5010/ops/params' 'POST' ($restore | ConvertTo-Json -Depth 6) $opsHdr | Out-Null
  Info "已把 params 还原为写入前的值"
}

# ------------------------------------------------- 6. 读接口：会话地址一致性
Write-Host "`n[6] /api 读接口：带会话时只能读自己的地址" -ForegroundColor Cyan
$env:API_BASE = 'http://127.0.0.1:3010'
$env:API_KEY = $ApiKey
$loginRaw = & node (Join-Path $Root 'scripts\privchain-login.mjs') 2>&1 | Out-String
$login = $null
try { $login = $loginRaw | ConvertFrom-Json } catch { }
Step "[6a] 测试用户签名登录成功（拿到会话票）" ($login.code -eq 0 -and $login.token) ($loginRaw.Trim())
$selfAddr = $login.address
$otherAddr = SqlScalar "SELECT address FROM wallet WHERE address <> '$selfAddr' ORDER BY id LIMIT 1"
$authHdr = @{ 'X-API-Key' = $ApiKey; Authorization = ('Bearer ' + $login.token) }
function GetApi([string]$uri, [hashtable]$h) {
  try { return Invoke-WebRequest -Uri $uri -Headers $h -UseBasicParsing -TimeoutSec 10 }
  catch { return $_.Exception.Response }
}
$rSelf = GetApi "http://127.0.0.1:3010/api/assets?address=$selfAddr" $authHdr
AssertEq "[6b] 带会话读自己 → 放行（code=0）" '0' (CodeOf $rSelf)
$rOther = GetApi "http://127.0.0.1:3010/api/assets?address=$otherAddr" $authHdr
Step "[6c] 带自己的会话读他人地址 → 被拒（修复前会返回他人资产）" ((CodeOf $rOther) -eq '600') ("code=$(CodeOf $rOther) body=$((($rOther.Content | Out-String)).Trim())")
$rOtherUser = GetApi "http://127.0.0.1:3010/api/user?address=$otherAddr" $authHdr
Step "[6d] /api/user 同样被拒" ((CodeOf $rOtherUser) -eq '600') ("code=$(CodeOf $rOtherUser)")
# 匿名读（无会话）：当前口径仍允许（受 Auth.EnforceSession 控制），这里显式记录现状而不是当失败
$rAnon = GetApi "http://127.0.0.1:3010/api/assets?address=$otherAddr" @{ 'X-API-Key' = $ApiKey }
Info ("匿名读他人地址当前返回 code=" + (CodeOf $rAnon) + "（要彻底关闭需开 Auth.EnforceSession，属 owner 决策）")

# ------------------------------------------------- 7. evmwatch：无进度时 fail-closed
Write-Host "`n[7] evmwatch 无扫描进度时 fail-closed（防上线漏扫历史充值）" -ForegroundColor Cyan
$ewCfg = Join-Path $Root 'ncf-verify-evmwatch.yml'
$ewTxt = [System.IO.File]::ReadAllText((Join-Path $Root 'houduan\TiMi\config\etc.privchain.yml'))
# ⚠ 进度键名用的是 Go 侧 pool.Hex()（EIP-55 校验和大小写），必须从配置里取收款池地址；
#   原先用了一个本脚本里并不存在的 $PoolAddress → DEL 删的是不存在的键 → 进度还在 → 测不出 fail-closed。
$ewPool = ''
if ($ewTxt -match '(?m)^\s*WatchPool:\s*"?([^"\s]+)"?') { $ewPool = $Matches[1] }
Info ("evmwatch 测试配置：WatchPool=$ewPool / WatchStartBlock=0 / AllowStartFromLatest=false")
$ewTxt = $ewTxt -replace '(?m)^LogPath:.*$', 'LogPath: ncf-verify-evmwatch.log'
$ewTxt = $ewTxt -replace '(?m)^(\s*)AllowStartFromLatest:.*$', '$1AllowStartFromLatest: false'
$ewTxt = $ewTxt -replace '(?m)^(\s*)WatchStartBlock:.*$', '$1WatchStartBlock: 0'
[System.IO.File]::WriteAllText($ewCfg, $ewTxt, (New-Object System.Text.UTF8Encoding($false)))
$ewExe = Join-Path $Root 'houduan\TiMi\build\evmwatch.exe'
$redisCli = 'C:\Program Files\Redis\redis-cli.exe'
$ewKey = "npower:evmwatch:lastblock:TM:$ewPool"
if (Test-Path $redisCli) { & $redisCli -n 6 DEL $ewKey | Out-Null }
$ewOut = & $ewExe -f $ewCfg -symbol TM -decimals 8 -once 2>&1 | Out-String
Step "[7a] 无进度且未指定起始块 → 拒绝启动（非 0 退出）" ($LASTEXITCODE -ne 0) ("exit=$LASTEXITCODE")
Step "[7b] 错误信息指明了如何配置" (($ewOut -match 'WatchStartBlock') -and ($ewOut -match 'AllowStartFromLatest')) ("含 WatchStartBlock=$($ewOut -match 'WatchStartBlock') 含 AllowStartFromLatest=$($ewOut -match 'AllowStartFromLatest')")
if (Test-Path $redisCli) { & $redisCli -n 6 DEL $ewKey | Out-Null }
$ewOut2 = & $ewExe -f $ewCfg -symbol TM -decimals 8 -fromBlock 1 -once 2>&1 | Out-String
Step "[7c] 显式给 -fromBlock 1 → 正常扫描（exit=0）" ($LASTEXITCODE -eq 0) ("exit=$LASTEXITCODE")

# ---------------------------------------------------------------- 清理
Write-Host "`n[8] 清理" -ForegroundColor Cyan
$opsProc | Stop-Process -Force -ErrorAction SilentlyContinue
$strictProc | Stop-Process -Force -ErrorAction SilentlyContinue
$devProc | Stop-Process -Force -ErrorAction SilentlyContinue
Sql $cleanupSql | Out-Null
Sql "DELETE FROM hash_power WHERE address='$walletAddr' AND created_at >= DATE_SUB(NOW(), INTERVAL 10 MINUTE);" | Out-Null
Sql "DELETE FROM wallet_tx WHERE type=9 AND created_at >= DATE_SUB(NOW(), INTERVAL 10 MINUTE);" | Out-Null
foreach ($f in @('ncf-verify-strict.yml', 'ncf-verify-dev.yml', 'ncf-verify-ops.yml', 'ncf-verify-evmwatch.yml', 'ncf-verify-strict.log', 'ncf-verify-dev.log', 'ncf-verify-ops.log', 'ncf-verify-evmwatch.log')) {
  $fp = Join-Path $Root $f
  if (Test-Path -LiteralPath $fp) { Remove-Item -LiteralPath $fp -Force }
}
Remove-Item Env:\API_BASE -ErrorAction SilentlyContinue
Remove-Item Env:\API_KEY -ErrorAction SilentlyContinue
Step "临时项目/轮次/投票已清理" ('0' -eq (SqlScalar "SELECT COUNT(*) FROM project WHERE period=9901"))
Step "临时配置文件已清理" (-not (Test-Path -LiteralPath $ewCfg))

Write-Host ("`n=== 结果：PASS={0} FAIL={1} ===" -f $script:Pass, $script:Fail) -ForegroundColor Cyan
if ($script:Fail -gt 0) { exit 1 }
exit 0
