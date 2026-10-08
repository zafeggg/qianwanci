# 千万次：出金热钱包私钥「环境变量注入」验证（私链专用）
#
# 背景：`Withdraw.HotWalletPrivate` 原先只能从 yml 明文读，而 yml 已入库 ——
#   私钥随 git 历史一起泄漏。本轮新增 `Withdraw.HotWalletKeyEnv`（填环境变量名），
#   程序优先从环境变量取私钥，yml 可留空。
# 本脚本验证「环境变量确实覆盖 yml」，且不依赖任何链上节点：
#   借 `cmd/evmwatch` 启动时的**配置体检日志**——它会把由私钥推导出的出金热钱包地址打出来
#   （"收款池与出金热钱包不同址 … hotWallet=0x…"）。该日志在开扫之前输出，链不通也能拿到。
#
# 用例：
#   A) 环境变量为空 → 用 yml 的私钥（账户[2] → 0x3C44CdDd…）
#   B) 环境变量给出另一个私钥（账户[3]）→ 覆盖 yml，日志里应变成 0x90F79bf6…
#
# 用法： & .\scripts\privchain-hotwallet-env.ps1
[CmdletBinding()]
param(
  [string]$YmlPath = "config\etc.privchain.yml",
  [string]$EnvName = "TITI_HOT_WALLET_PK",
  # hardhat 账户[2]/[3] 的私钥与地址（与 config/etc.privchain.yml 里的 yml 私钥一致/对照）
  [string]$YmlKeyAddress = "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC",
  [string]$AltKey = "0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6",
  [string]$AltKeyAddress = "0x90F79bf6EB2c4f870365E785982E1f101E93b906"
)

$ErrorActionPreference = "Stop"
$RepoRoot = Split-Path -Parent $PSScriptRoot
$TiMiDir = Join-Path $RepoRoot "houduan\TiMi"

$pass = 0; $fail = 0
function Step([string]$name, [bool]$ok, [string]$detail = "") {
  if ($ok) { Write-Host "  PASS  $name" -ForegroundColor Green; $script:pass++ }
  else { Write-Host "  FAIL  $name  — $detail" -ForegroundColor Red; $script:fail++ }
}

Write-Host ("=" * 74)
Write-Host "千万次 出金热钱包私钥 环境变量注入 验证"
Write-Host ("=" * 74)

# ---------- 0. 前置 ----------
Write-Host "[0/3] 前置检查"
$exe = Join-Path $TiMiDir "build\evmwatch.exe"
$src = Join-Path $TiMiDir $YmlPath
Step "evmwatch.exe 存在" (Test-Path $exe) $exe
Step "私链配置存在" (Test-Path $src) $src
if (-not (Test-Path $exe) -or -not (Test-Path $src)) { exit 1 }

# ---------- 1. 生成临时配置（在 Withdraw 段插入 HotWalletKeyEnv） ----------
Write-Host "[1/3] 生成临时配置（yml 里指定 HotWalletKeyEnv = $EnvName）"
$tmp = Join-Path $env:TEMP "evmwatch-hotwallet-$([guid]::NewGuid().ToString('N')).yml"
$yml = Get-Content $src -Raw -Encoding UTF8
if ($yml -notmatch "(?m)^Withdraw:\s*$") { throw "配置里找不到 Withdraw: 段" }
$yml2 = $yml -replace "(?m)^Withdraw:\s*$", "Withdraw:`n  HotWalletKeyEnv: $EnvName"
Set-Content -Path $tmp -Value $yml2 -Encoding UTF8
$hasEnv = (Select-String -Path $tmp -Pattern "HotWalletKeyEnv:\s*$EnvName" -Quiet)
$hasYmlKey = (Select-String -Path $tmp -Pattern "HotWalletPrivate:\s*`"?5de4111a" -Quiet)
Step "临时配置同时含 HotWalletKeyEnv 与 yml 明文私钥（构成对照）" ($hasEnv -and $hasYmlKey)

function HotWalletFromLog([string]$ymlFile) {
  $prev = $ErrorActionPreference; $ErrorActionPreference = "SilentlyContinue"
  Push-Location $TiMiDir
  try { $out = & $exe -f $ymlFile -symbol TM -decimals 8 -once 2>&1 | Out-String } finally { Pop-Location; $ErrorActionPreference = $prev }
  $m = [regex]::Match($out, 'hotWallet=(0x[0-9a-fA-F]{40})')
  if ($m.Success) { return $m.Groups[1].Value }
  return ""
}

# ---------- 2. 环境变量为空 → 用 yml ----------
Write-Host "[2/3] 用例A：环境变量为空 → 应使用 yml 里的私钥"
$env:TITI_HOT_WALLET_PK = ""
$addrA = HotWalletFromLog $tmp
Write-Host "      日志里的出金热钱包 = $addrA"
Step "未设环境变量时用 yml 私钥（$YmlKeyAddress）" `
  ($addrA -ne "" -and $addrA.ToLower() -eq $YmlKeyAddress.ToLower()) "实际=$addrA"

# ---------- 3. 环境变量给出另一个私钥 → 应覆盖 yml ----------
Write-Host "[3/3] 用例B：环境变量给出账户[3] 私钥 → 应覆盖 yml"
$env:TITI_HOT_WALLET_PK = $AltKey
$addrB = HotWalletFromLog $tmp
Write-Host "      日志里的出金热钱包 = $addrB"
Step "环境变量私钥覆盖了 yml（$AltKeyAddress）" `
  ($addrB -ne "" -and $addrB.ToLower() -eq $AltKeyAddress.ToLower()) "实际=$addrB"
Step "两次结果不同（证明确实换了私钥来源）" ($addrA -ne $addrB) "A=$addrA B=$addrB"

Remove-Item Env:\TITI_HOT_WALLET_PK -ErrorAction SilentlyContinue
Remove-Item $tmp -Force -ErrorAction SilentlyContinue

Write-Host ("=" * 74)
Write-Host "结果：PASS=$pass / FAIL=$fail"
Write-Host ("=" * 74)
exit $fail
