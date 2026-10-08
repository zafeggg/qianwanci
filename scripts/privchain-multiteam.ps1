# 千万次：动态收益 / 团队收益 / F1 晋升 —— 多账号验证（私链专用）
#
# 覆盖需求 #4（N次方介绍「动态收益」「团队奖励」）里单人链路测不到的部分：
#   1. 动态分享的**层级门槛**：一代需直推≥2、三代需直推≥5、五代需直推≥10
#      （伞下延伸再深也不替代直推数——门槛只看直推，不看伞下规模）
#   2. 三代/五代份额按门槛正确截断，比例 1.5% / 2% / 2.5%
#   3. 团队收益 = 伞下该期(project)总投资额 × 等级比例，且**每轮只发一次**（一条记录）
#   4. 团队收益「只给达到 F1/F2/F3 的人」，未达等级不发放
#   5. F1 晋升三条件：直推≥10 + 伞下≥30人 + 伞下累计≥30人各成功≥1次
#
# 账号树（5 个真实签名核心账号 A/B/C/D/E + 44 个种子伞下账号 S0..S43）：
#   A ─┬─ C ─┬─ B ── S32 ── S33 ── S34 ── S35   （B 链：给 C 造五代受益人，验证五代门槛）
#      │     ├─ D ── E                            （D 直推仅 1 人 → 一代门槛挡住；E 是真实账号）
#      │     └─ S0..S7                            （C 直推 = B + D + S0..S7 = 10，刚好同时满足 F1 与五代门槛）
#      └─ S36..S43                                （A 直推 = C + 8 = 9，差 1 人 → F1 与五代门槛均不满足）
#   S0 ── S8..S31                                 （24 人；S0 的直推，C 的二代）
#
# ⚠ 代数偏移（必须与 impl 一致，否则预期值全错）：
#   闭包表 distance = 0 即**一代**（直推），distance = 2 即**三代**，distance = 4 即**五代**。
#   即「第 k 代受益人」对应 distance = k-1，不是「向上 k 跳」。
#   （本次实测踩坑：把 distance 当跳数 → C/A 预期值算反、脚本 FAIL，而 impl 其实是正确的。）
#
# 投票人 → 各代受益人（第 1 轮 44 个种子账号 + E 各投 1 枚）：
#   S0..S7   → 一代 C
#   S8..S31  → 一代 S0、三代 A
#   S32      → 一代 B(直推1，挡)、三代 A
#   S33      → 一代 S32(挡)、三代 C
#   S34      → 一代 S33(挡)、三代 B(直推1，挡)、五代 A(直推9<10，挡)
#   S35      → 一代 S34(挡)、三代 S32(挡)、五代 C(直推10≥10，解锁)
#   S36..S43 → 一代 A
#   E        → 一代 D(直推1，挡)、三代 A
#
# 门槛覆盖矩阵：
#   一代 ≥2 ：解锁 C(10)/A(9)/S0(24) ；拦住 B(1)/D(1)/S32(1)/S33(1)/S34(1)
#   三代 ≥5 ：解锁 C(10)/A(9)       ；拦住 B(1)/S32(1)
#   五代 ≥10：解锁 C(10)            ；拦住 A(9)
#
# 用法： & .\scripts\privchain-multiteam.ps1
# 前置：npower :3000、ops :5000 已按 config/etc.privchain.yml 启动
[CmdletBinding()]
param(
  [string]$ApiBase = "http://127.0.0.1:3000",
  [string]$OpsBase = "http://127.0.0.1:5000",
  [string]$MysqlExe = "C:\Program Files\MySQL\MySQL Server 8.0\bin\mysql.exe",
  [string]$Database = "crowd_privchain",
  [string]$ApiKey = "privchain-apikey",
  [double]$Vote = 10
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
function E2F([string]$v) { return [double]::Parse($v, [Globalization.CultureInfo]::InvariantCulture) }
function Near([double]$a, [double]$b, [double]$eps = 0.0001) { return [Math]::Abs($a - $b) -lt $eps }

$SubCount = 44           # 种子伞下账号数量（S0..S43）
# 种子账号**不使用指定主键**：让 MySQL 自增分配，插完再按地址回查 id。
# 原实现写死 id 1000..1043，与真实登录创建的核心账号（同样是自增 id）撞过主键，
# 且「按 id 区间清理」会误删落在该区间的真实钱包（历史运行里 A/D/E 正好占过 1000/1003/1004）。
# 地址由固定 seed 确定性派生，所以「按地址清理」既精确又足够。

# hardhat 内置账户私钥（A/B/C/D/E 五个核心账号；账户[0] 是手续费池，不参与）
$Keys = @{
  A = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
  B = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
  C = "0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a"
  D = "0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6"
  E = "0x47e179ec197488593b187f80a00eb0da91f1b9d0b13f8733639f19c30a34926a"
}

Write-Host ("=" * 74)
Write-Host "千万次 动态/团队收益 多账号验证（$Database）"
Write-Host ("=" * 74)

# ---------- 0. 服务健康 ----------
Write-Host "[0/6] 服务健康"
try { $h = (Invoke-RestMethod "$ApiBase/health" -TimeoutSec 8).data; Step "npower 可达（mysql=$($h.checks.mysql)）" ($h.checks.mysql -eq "ok") ($h | ConvertTo-Json -Compress) } catch { Step "npower 可达" $false $_ ; exit 1 }
try { $null = Invoke-RestMethod "$OpsBase/ops/data/zmy" -TimeoutSec 8; Step "ops 可达" $true } catch { Step "ops 可达" ($null -ne $_.Exception.Response) $_.Exception.Message }
$opsToken = (Invoke-RestMethod "$OpsBase/ops/sign" -Method Post -ContentType "application/json" -Body '{"account":"ops","pwd":"privchain-ops-pwd"}' -TimeoutSec 10).data
Step "ops 登录" ($null -ne $opsToken)

# ---------- 1. 清库 ----------
Write-Host "[1/6] 清理旧数据"
# 种子钱包的清理放在第 2 步（按地址精确删，见那里），这里不动 wallet 表，
# 避免「按 id 区间删」误伤真实用户钱包。
Sql @"
DELETE FROM vote_reward; DELETE FROM vote; DELETE FROM project_round; DELETE FROM project;
DELETE FROM wallet_tree; DELETE FROM wallet_point; DELETE FROM hash_power;
"@ | Out-Null
Step "轮次/投票/收益/钱包树/算力已清空" $true

# ---------- 2. 造账号 ----------
Write-Host "[2/6] 建立 5 个核心账号 + $SubCount 个伞下账号 + 层级关系"
$acctFile = "scripts\.tmp-multiteam-accounts.json"
$env:PC_ARGS = "{`"n`":$SubCount,`"seed`":`"multiteam`",`"out`":`"scripts/.tmp-multiteam-accounts.json`"}"
& node (Join-Path $RepoRoot "scripts\privchain-accounts.mjs") | Out-Null
$seeded = Get-Content (Join-Path $RepoRoot $acctFile) -Raw | ConvertFrom-Json
Remove-Item Env:\PC_ARGS
Step "生成 $SubCount 个确定性测试账号" ($seeded.Count -eq $SubCount) "count=$($seeded.Count)"

# 种子地址由固定 seed 确定性派生：先按地址清掉历史运行留下的行，避免撞 evm_address 唯一索引
$addrList = ($seeded | ForEach-Object { "'$($_.address)'" }) -join ","
Sql "DELETE FROM wallet WHERE evm_address IN ($addrList);" | Out-Null

# 5 个核心账号走真实签名登录（首登即注册，等价于真实用户）
$addr = @{}
foreach ($k in @('A','B','C','D','E')) {
  $env:TEST_PRIVATE_KEY = $Keys.$k
  $env:API_BASE = $ApiBase; $env:API_KEY = $ApiKey
  $login = & node (Join-Path $RepoRoot "scripts\privchain-login.mjs") 2>&1 | ConvertFrom-Json
  if ($login.code -ne 0) { throw "核心账号 $k 登录失败：$($login | ConvertTo-Json -Compress)" }
  $addr[$k] = $login.address
  Write-Host "      $k = $($login.address) walletId=$($login.walletId)"
}
Remove-Item Env:\TEST_PRIVATE_KEY,Env:\API_BASE,Env:\API_KEY -ErrorAction SilentlyContinue
$wid = @{}
foreach ($k in @('A','B','C','D','E')) { $wid[$k] = [int](Sql1 "SELECT id FROM wallet WHERE evm_address='$($addr[$k])';") }

# 种子伞下账号：直接入库（它们只作为伞下人数、层级节点与投票人，不需要登录私钥）。
# 不指定 id（见文件头说明），插完再按地址回查真实 id。
$sqlInsert = "INSERT INTO wallet (created_at,updated_at,address,evm_address,name,code,level,active,admin) VALUES "
$rows = @()
for ($i = 0; $i -lt $SubCount; $i++) {
  $a = $seeded[$i].address
  $rows += "(NOW(),NOW(),'$a','$a','sub$i','SUB$( '{0:D4}' -f $i )',0,1,0)"
}
$insErr = Sql ($sqlInsert + ($rows -join ","))
if ($insErr) { throw "子账号插入失败：$insErr" }
$sub = @{}
for ($i = 0; $i -lt $SubCount; $i++) {
  $id = [int](Sql1 "SELECT id FROM wallet WHERE evm_address='$($seeded[$i].address)';")
  if ($id -le 0) { throw "子账号 S$i 插入后回查不到 id（地址 $($seeded[$i].address)）" }
  $sub[$i] = $id
}
$subIdList = ($sub.Values -join ",")
Step "$SubCount 个伞下账号入库" ([int](Sql1 "SELECT COUNT(*) FROM wallet WHERE evm_address IN ($addrList);") -eq $SubCount) "ids=$($subIdList.Substring(0, [Math]::Min(40, $subIdList.Length)))..."


# 层级关系（闭包表）。
# ⚠ 闭包表必须写**全部传递闭包**，不能只写直推边：
#   真实注册路径 impl.InviteWithTx 注册时会「复制父节点的全部祖先 + 距离 1」，再写直推边 (P,D,0)，
#   所以库里每一对祖先-后代都有一行。只写直推边会导致 FindAncestorPathAge(id, 2) 查不到三代，
#   动态收益的三代/五代份额永远发不出来（本次就是踩了这个坑，一轮实测才暴露）。
# 生成方式与生产一致：按「父在前、子在后」的注册顺序逐条复制父节点的祖先。
$parent = @{}
$parent[$wid.C] = $wid.A
$parent[$wid.B] = $wid.C
$parent[$wid.D] = $wid.C
$parent[$wid.E] = $wid.D
for ($i = 0; $i -lt 8; $i++)   { $parent[$sub[$i]] = $wid.C }     # S0..S7  → C（C 直推 2+8=10）
for ($i = 8; $i -lt 32; $i++)  { $parent[$sub[$i]] = $sub[0] }    # S8..S31 → S0（C 的三代，24 人）
$parent[$sub[32]] = $wid.B                                        # S32 → B
$parent[$sub[33]] = $sub[32]                                      # S33 → S32
$parent[$sub[34]] = $sub[33]                                      # S34 → S33（其五代受益人 = A）
$parent[$sub[35]] = $sub[34]                                      # S35 → S34（其五代受益人 = C）
for ($i = 36; $i -lt $SubCount; $i++) { $parent[$sub[$i]] = $wid.A }  # S36..S43 → A（A 直推 1+8=9）

# 生成顺序：必须「父节点先于子节点」。用「所有父已就绪」的拓扑排序，而不是按 id 或层数近似，
# 否则父节点的祖先表还没算出来，子节点会漏掉间接祖先（本次踩坑：T 排在它的父 S0/T8 之前 → 闭包缺边）。
$order = @()
$done = @{}
# 根节点 A 没有父节点，先标记为已就绪；否则所有子节点都会一直等它，循环直接空转（本次踩坑）
$done[$wid.A] = $true
do {
  $added = $false
  foreach ($k in @($parent.Keys)) {
    if ($done.ContainsKey($k)) { continue }
    if (-not $done.ContainsKey($parent[$k])) { continue }   # 父节点还没入队，下一轮
    $order += $k
    $done[$k] = $true
    $added = $true
  }
} while ($added)
# 根节点补到最前（它没有祖先，不会向 wallet_tree 写任何行）
$order = @($wid.A) + $order
if ($order.Count -ne ($parent.Count + 1)) { throw "闭包排序失败：期望 $($parent.Count + 1) 个节点，实际 $($order.Count)" }

$tree = @{}    # 每个节点的祖先表：ancestorId -> distance
Sql "DELETE FROM wallet_tree;" | Out-Null
foreach ($node in $order) {
  $anc = @{}
  if ($parent.ContainsKey($node)) {
    $p = $parent[$node]
    $anc[$p] = 0
    if ($tree.ContainsKey($p)) { foreach ($k in $tree[$p].Keys) { $anc[$k] = $tree[$p][$k] + 1 } }
  }
  $tree[$node] = $anc
  if ($anc.Count -gt 0) {
    $vals = @()
    foreach ($k in $anc.Keys) { $vals += "($k,$node,$($anc[$k]))" }
    Sql ("INSERT INTO wallet_tree (ancestor,descendant,distance) VALUES " + ($vals -join ",")) | Out-Null
  }
}
$cDirect = [int](Sql1 "SELECT COUNT(*) FROM wallet_tree WHERE ancestor=$($wid.C) AND distance=0;")
$cAll = [int](Sql1 "SELECT COUNT(*) FROM wallet_tree WHERE ancestor=$($wid.C);")
$eAnc = [int](Sql1 "SELECT COUNT(*) FROM wallet_tree WHERE descendant=$($wid.E);")
$aDirect = [int](Sql1 "SELECT COUNT(*) FROM wallet_tree WHERE ancestor=$($wid.A) AND distance=0;")
Step "闭包表已建立（C 直推 10 / 伞下 39；A 直推 9；含 5 代链）" `
  ($cDirect -eq 10 -and $cAll -eq 39 -and $aDirect -eq 9) "C直推=$cDirect C伞下=$cAll A直推=$aDirect"
Step "E 的祖先链 = D/C/A 三层（共 3 行）" ($eAnc -eq 3) "E祖先=$eAnc"

# 伞下账号补余额（它们要在第 1 轮投票，用于满足 F1 的「伞下 30 人各成功一次」）
$bp = "INSERT INTO wallet_point (created_at,updated_at,address,symbol,amount) VALUES "
$bpRows = @()
foreach ($i in 0..($SubCount - 1)) { $bpRows += "(NOW(),NOW(),'$($seeded[$i].address)','TM',100000000000)" }
foreach ($k in @('B','C','D','E')) { $bpRows += "(NOW(),NOW(),'$($addr[$k])','TM',100000000000)" }
Sql ($bp + ($bpRows -join ",")) | Out-Null
Step "测试账号余额已注入（每人 1000 TM）" ([int](Sql1 "SELECT COUNT(*) FROM wallet_point;") -ge ($SubCount + 4))

# ⚠ wallet.active 的真实语义（本次实测纠正的误解）：
#   **注册不会置 active**。`authRegister`（签名登录首登）与 `WalletManager.Registration` 建出来的钱包
#   active=0；只有 `RoundManager.Active`（某个**成功轮** End 时）才会把该轮参投人刷成 active=1。
#   而 `DirectDescendants`（动态收益门槛用的正是它）**按 active 过滤**，所以：
#     - 没在任何成功轮投过票的直推 = 不计入「直推 N 人」→ 门槛不达标（业务语义是否合理见需求文档待议项）；
#     - 本脚本必须显式把测试钱包置 active=1，否则「直推数」会被静默算成 0、全部门槛断言失真。
$coreIdList = (@($wid.A, $wid.B, $wid.C, $wid.D, $wid.E) -join ",")
Sql ("UPDATE wallet SET active=1, level=0 WHERE id IN ($coreIdList) OR id IN ($subIdList);") | Out-Null
# 断言只看**本脚本的 49 个测试钱包**：全局 active 统计会被无关历史钱包（如别处注册后从未参投的地址）干扰
$testIdList = "$subIdList,$coreIdList"
$testInactive = [int](Sql1 "SELECT COUNT(*) FROM wallet WHERE active=0 AND id IN ($testIdList);")
$coreLevel = [int](Sql1 "SELECT COUNT(*) FROM wallet WHERE level>0 AND id IN ($coreIdList);")
Step "49 个测试账号 active=1 且核心账号等级已归零（与 RoundManager.Active 后的状态一致）" `
  ($testInactive -eq 0 -and $coreLevel -eq 0) "inactive=$testInactive coreLevel>0 个数=$coreLevel"

# ---------- 3. 造轮次 ----------
Write-Host "[3/6] 建立 4 个轮次（第 4 轮结算第 1 轮：rewardRound = 4 - MinRound(3) = 1）"
Sql "INSERT INTO project (created_at,updated_at,period,symbol,status) VALUES (NOW(),NOW(),1,'TM',1);" | Out-Null
$projId = [int](Sql1 "SELECT id FROM project ORDER BY id DESC LIMIT 1;")
Sql @"
INSERT INTO project_round (created_at,updated_at,period,project_id,round,time_limit,target_vote,min_vote,max_vote,current_vote,start_time,end_time,count,symbol,success,status) VALUES
 (NOW(),NOW(),1,$projId,1,3600,1000,1,100000,0,DATE_SUB(NOW(), INTERVAL 3 HOUR),DATE_SUB(NOW(), INTERVAL 2 HOUR),0,'TM',1,3),
 (NOW(),NOW(),1,$projId,2,3600,1000,1,100000,0,DATE_SUB(NOW(), INTERVAL 2 HOUR),DATE_SUB(NOW(), INTERVAL 1 HOUR),0,'TM',1,3),
 (NOW(),NOW(),1,$projId,3,3600,1000,1,100000,0,DATE_SUB(NOW(), INTERVAL 1 HOUR),DATE_SUB(NOW(), INTERVAL 30 MINUTE),0,'TM',1,3),
 (NOW(),NOW(),1,$projId,4,3600,1000,1,100000,0,NOW(),DATE_ADD(NOW(), INTERVAL 2 HOUR),0,'TM',0,1);
"@ | Out-Null
$r = @{}
foreach ($n in 1..4) { $r[$n] = [int](Sql1 "SELECT id FROM project_round WHERE project_id=$projId AND round=$n;") }
Step "轮次已建立 id=$($r[1]),$($r[2]),$($r[3]),$($r[4])" ($r[1] -gt 0 -and $r[4] -gt 0)

# ---------- 4. 第 1 轮：44 个伞下账号 + E 真实投票 ----------
Write-Host "[4/6] 第 1 轮：$SubCount 个伞下账号 + E 投票（同时制造 F1 的历史成功记录与各代受益人）"
# ⚠ 单 IP 写限流默认 60/分（风控 D 模块），40+ 次投票会撞 429。
#   这里测试期间临时把 WriteRatePerMinute 调高，结束时还原；不是关掉风控（限流中间件仍在跑）。
#
# ⚠ 两个坑（2026-09-22 实测踩到）：
#   1. 该阈值原先**没接进 NpowerParams**，/ops/params 写了被静默忽略（已在本轮修好）；
#   2. 还原**不能靠「删掉这个键」**：ApplyNpowerParams 的约定是「0/空 = 不覆盖」，
#      删键只会让 DB 里没有它，npower 内存里仍是调高后的值。必须显式写回原数值。
#   所以这里先探测「当前实际生效值」（响应头 X-Ratelimit-Limit），结束时按它显式写回。
function Get-EffectiveWriteLimit {
  $hdr = & curl.exe -s -i -X POST "$ApiBase/api/participate" -H "X-API-Key: $ApiKey" -H "Content-Type: application/json" `
    --data-binary '{"address":"0x0000000000000000000000000000000000000000","roundId":0,"amount":1,"asset":"TM"}'
  $m = [regex]::Match(($hdr -join "`n"), '(?i)X-Ratelimit-Limit:\s*(\d+)')
  if ($m.Success) { return [int]$m.Groups[1].Value }
  return 0
}
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
function Get-ParamValue($map, [string]$key) {
  if ($map.ContainsKey($key)) { return $map[$key] }
  return $null
}
$writeLimit0 = Get-EffectiveWriteLimit
Write-Host "      测试前 write 限流 = $writeLimit0/分"
$paramsBackup = Get-Params $opsToken
$paramsTest = @{}
foreach ($k in $paramsBackup.Keys) { $paramsTest[$k] = $paramsBackup[$k] }
$paramsTest['WriteRatePerMinute'] = 600
Set-Params $opsToken $paramsTest
Start-Sleep -Seconds 2
Write-Host "      已临时提高写限流至 600/分（已实测生效：$(Get-EffectiveWriteLimit)/分）"

# round1 需处于「进行中」才能投票
Sql "UPDATE project_round SET status=1, start_time=NOW(), end_time=DATE_ADD(NOW(), INTERVAL 2 HOUR) WHERE id=$($r[1]);" | Out-Null
$bodyFile = Join-Path $env:TEMP "mt-vote.json"
function CastVote([string]$address, [int]$roundId, [double]$amount) {
  @{ address = $address; roundId = $roundId; amount = $amount; asset = "TM" } | ConvertTo-Json -Compress |
    Out-File -Encoding ascii $bodyFile
  return (& curl.exe -s -X POST "$ApiBase/api/participate" -H "X-API-Key: $ApiKey" -H "Content-Type: application/json" --data-binary "@$bodyFile")
}
$okCount = 0
for ($i = 0; $i -lt $SubCount; $i++) {
  $resp = CastVote $seeded[$i].address $r[1] 1
  if ($resp -match '"code":0') { $okCount++ } else { Write-Host "      S$i 参投失败：$resp" -ForegroundColor Yellow }
}
Step "$SubCount 个伞下账号第 1 轮参投成功（$okCount/$SubCount）" ($okCount -eq $SubCount) "ok=$okCount"
$respE = CastVote $addr.E $r[1] 1
Step "E 第 1 轮参投成功（E→D 为一代、E→A 为三代，用于验证 D 的「一代需直推≥2」门槛）" ($respE -match '"code":0') $respE
# 第 1 轮标记为已成功（Promote 统计的是「参与过 success=1 的轮」的伞下人数）
Sql "UPDATE project_round SET status=3, success=1 WHERE id=$($r[1]);" | Out-Null
$succ = [int](Sql1 @"
SELECT COUNT(DISTINCT v.wallet_id) FROM vote v JOIN project_round pr ON pr.id=v.round_id
WHERE pr.success=1 AND v.wallet_id IN ($subIdList);
"@)
Step "伞下「各成功至少 1 次」人数 = $succ（F1 需 ≥30）" ($succ -eq $SubCount) "succ=$succ"

# ---------- 5. 第 4 轮：B/D/E 投票 → 真实结算器结算第 1 轮 ----------
Write-Host "[5/6] 第 4 轮：B/D/E 投票；用真实结算器 cmd/round 结算第 1 轮"
foreach ($k in @('B','D','E')) {
  $resp = CastVote $addr[$k] $r[4] $Vote
  if ($resp -notmatch '"code":0') { throw "账号 $k 第 4 轮参投失败：$resp" }
  Write-Host "      $k 参投 $Vote TM 成功"
}
# 第 4 轮需为「进行中」且已达目标；第 1 轮需回到「待结算(2)」等结算窗口
Sql "UPDATE project_round SET status=1, current_vote=target_vote, start_time=NOW(), end_time=DATE_ADD(NOW(), INTERVAL 5 SECOND) WHERE id=$($r[4]);" | Out-Null
Sql "UPDATE project_round SET status=2, current_vote=target_vote WHERE id=$($r[1]);" | Out-Null

# ⚠ 必须走**真实结算器 cmd/round**（RoundManager.End），不能用 /ops/endRound：
#   /ops/endRound 直接调 RewardManager.Success，**不执行 Promote**，
#   所以 F1/F2/F3 晋升在那条路径上永远不会发生（与 e2e 脚本同一个坑）。
$roundExe = Join-Path $RepoRoot "houduan\TiMi\build\round.exe"
$roundCfg = Join-Path $RepoRoot "houduan\TiMi\config\round.privchain.yml"
$roundProc = Start-Process -FilePath $roundExe -ArgumentList "-f", $roundCfg `
  -WorkingDirectory (Join-Path $RepoRoot "houduan\TiMi") -WindowStyle Hidden -PassThru
Start-Sleep -Seconds 75   # 结束轮是每分钟 0 秒触发，给足一个周期
Stop-Process -Id $roundProc.Id -Force -ErrorAction SilentlyContinue
$settled = [int](Sql1 "SELECT COUNT(*) FROM vote_reward WHERE round_id=$($r[1]);")
Step "真实结算器已结算第 1 轮（RoundManager.End）" ($settled -gt 0) "第1轮收益行数=$settled"
Start-Sleep -Seconds 3

# ---------- 6. 断言 ----------
Write-Host "[6/6] 断言"
function Reward([string]$a, [string]$col) {
  $v = Sql1 "SELECT COALESCE(SUM($col),0) FROM vote_reward WHERE address='$a';"
  if (-not $v) { return 0.0 }
  return E2F $v
}
$cShard = Reward $addr.C "dynamic_shard"
$aShard = Reward $addr.A "dynamic_shard"
$s0Shard = Reward $seeded[0].address "dynamic_shard"
$bShard = Reward $addr.B "dynamic_shard"
$dShard = Reward $addr.D "dynamic_shard"
$eShard = Reward $addr.E "dynamic_shard"
$cTeam = Reward $addr.C "dynamic_team"
$cLevel = [int](Sql1 "SELECT level FROM wallet WHERE id=$($wid.C);")
$aLevel = [int](Sql1 "SELECT level FROM wallet WHERE id=$($wid.A);")

Write-Host "      动态分享：C=$cShard A=$aShard S0=$s0Shard B=$bShard D=$dShard E=$eShard"
Write-Host "      团队收益：C=$cTeam ；等级：C=$cLevel A=$aLevel ；C 直推=$cDirect"

# F1 三条件逐项可见（口径与 impl.WalletManager.Promote 完全一致，便于失败时定位是哪一条不过）
$cVoteCount = [int](Sql1 "SELECT COUNT(DISTINCT v.wallet_id) FROM vote v WHERE v.project_id=$projId AND v.wallet_id IN (SELECT descendant FROM wallet_tree WHERE ancestor=$($wid.C));")
$cSuccessCount = [int](Sql1 "SELECT COUNT(DISTINCT v.wallet_id) FROM vote v JOIN project_round pr ON pr.id=v.round_id WHERE v.wallet_id IN (SELECT descendant FROM wallet_tree WHERE ancestor=$($wid.C)) AND pr.success=1;")
Write-Host "      C 的 F1 四项：直推=$cDirect(需≥10) 伞下=$cAll(需≥30) 该期参投=$cVoteCount(需≥30) 累计成功=$cSuccessCount(需≥30)"

# 需求（N次方介绍）：一代 1.5%（需直推≥2）、三代 2%（需直推≥5）、五代 2.5%（需直推≥10）。
# 代数是「向上 k 代」，闭包表里对应 distance = k-1（见文件头代数偏移说明）。
# C 的第 1 轮应得动态分享：
#   一代 1.5%：S0..S7 共 8 人 × 1 × 1.5% = 0.120（B/D 本轮未投）
#   三代 2.0%：S33（S33→S32→B→C，C 是三代）1 人 × 2% = 0.020（C 直推 10 ≥ 5，门槛解锁）
#   五代 2.5%：S35（S35→S34→S33→S32→B→C，C 是五代）1 人 × 2.5% = 0.025（C 直推 10 ≥ 10，门槛解锁）
#   合计 0.165
Step "C 动态分享 = 8×1.5% + 1×2% + 1×2.5% = 0.165（一代/三代/五代门槛全部解锁）" `
  (Near $cShard 0.165) "实际 $cShard（C 直推=$cDirect）"
# 需求：一代需直推≥2 —— D 的直推只有 E（1 个），E 本轮投了 1 枚，但 D 不拿任何一代份额
Step "D 无一代份额（直推 1 < 2，门槛挡住）" ($dShard -eq 0) "D 动态=$dShard"
# 需求：一代需直推≥2 —— B 的直推只有 S32（1 个），S32 本轮投了 1 枚，但 B 不拿一代份额
Step "B 无一代份额（直推 1 < 2，门槛挡住）" ($bShard -eq 0) "B 动态=$bShard"
Step "E 自身无动态分享（E 伞下无人）" ($eShard -eq 0) "E=$eShard"
# 需求：三代需直推≥5 —— S34 的三代受益人 = B，但 B 直推 1 < 5，同样拿不到（B 已在上一条断言为 0）
Step "B 无三代份额（直推 1 < 5，三代门槛挡住）" ($bShard -eq 0) "B=$bShard"
# A 的动态：一代 S36..S43（8×1.5%=0.12）
#         + 三代 S8..S31（24×2%=0.48）+ S32（0.02）+ E（0.02）= 0.64
#         + 五代 S34 的受益人 = A，但 A 直推 9 < 10 → 五代门槛挡住，贡献 0
Step "A 动态分享 = 8×1.5% + 26×2% = 0.64（五代因直推 9 < 10 被截断）" `
  (Near $aShard 0.64) "实际 $aShard"
# S0 是 S8..S31 的一代受益人（直推 24 ≥ 2），且本身无上游份额
Step "S0 动态分享 = 24×1.5% = 0.36（一代门槛生效）" (Near $s0Shard 0.36) "实际 $s0Shard"
# B 链上 S32/S33/S34 的直推都只有 1 人 → 全部拿不到份额，证明门槛是「按直推人数逐级截断」而非按伞下规模
$chainShard = 0.0
foreach ($i in 32..34) { $chainShard += (Reward $seeded[$i].address "dynamic_shard") }
Step "B 链 S32/S33/S34 均无份额（各自直推 1 < 2）" ($chainShard -eq 0) "链上合计=$chainShard"

# 需求：团队收益 = 伞下该期总投资额 × 等级比例，只给达等级的人，且每轮只发一次。
# 第 1 轮结算时 C 已在本轮 Promote 中升到 F1，伞下 39 人该期累计投资 = S0..S35(36) + B(10) + D(10) + E(1+10)
#   = 67 枚 → C 团队收益 = 67 × 0.5% = 0.335
Step "C 团队收益 = 67 × 0.5% = 0.335（F1 比例）" (Near $cTeam 0.335) "实际 $cTeam"
$cTeamRows = [int](Sql1 "SELECT COUNT(*) FROM vote_reward WHERE address='$($addr.C)' AND dynamic_team>0;")
Step "C 团队收益记录 = 1 条（每轮只发一次，不按伞下投票人重复发）" ($cTeamRows -eq 1) "rows=$cTeamRows"
$othersTeam = [double](E2F (Sql1 "SELECT COALESCE(SUM(dynamic_team),0) FROM vote_reward WHERE address<>'$($addr.C)';"))
Step "未达 F1 的账号无团队收益（只给达标者）" ($othersTeam -eq 0) "othersTeam=$othersTeam"

# 需求：F1 三条件（直推≥10 + 伞下≥30人 + 该期伞下参与≥30 + 累计成功≥30）
Step "C 晋升 F1（level=1）" ($cLevel -eq 1) "level=$cLevel 直推=$cDirect 伞下=$cAll 参投=$cVoteCount 成功=$cSuccessCount"
# 负向对照：A 直推 9（差 1 人）→ 即便伞下有 48 人也不晋升，证明「直推 10」是硬条件
Step "A 未晋升 F1（直推 $aDirect < 10，硬条件不过）" ($aLevel -eq 0) "level=$aLevel 直推=$aDirect"

# 时序正确性：第 4 轮（B/D/E 各投 10）本轮尚未结算，其收益与本金退还不应提前出现
$r4Rows = [int](Sql1 "SELECT COUNT(*) FROM vote_reward WHERE round_id=$($r[4]);")
Step "第 4 轮收益尚未发放（该轮还没进结算窗口）" ($r4Rows -eq 0) "rows=$r4Rows"

Remove-Item $acctFile,$bodyFile -Force -ErrorAction SilentlyContinue

# 还原测试期间临时提高的写限流（即使上面有断言失败也要还原）
# ⚠ 必须把原数值**显式写回**：ApplyNpowerParams 是「0/空 = 不覆盖」，
#   只把键删掉的话 npower 内存里还是调高后的值（本次实测踩到）。
$paramsRestore = @{}
foreach ($k in $paramsBackup.Keys) { $paramsRestore[$k] = $paramsBackup[$k] }
if ($writeLimit0 -gt 0) { $paramsRestore['WriteRatePerMinute'] = $writeLimit0 }
Set-Params $opsToken $paramsRestore
Start-Sleep -Seconds 2
$limitAfter = Get-EffectiveWriteLimit
Step "写限流已还原为测试前的生效值（$writeLimit0/分）" ($limitAfter -eq $writeLimit0) "测试前=$writeLimit0 还原后=$limitAfter"

Write-Host ("=" * 74)
Write-Host "结果：PASS=$pass / FAIL=$fail"
Write-Host ("=" * 74)
exit $fail
