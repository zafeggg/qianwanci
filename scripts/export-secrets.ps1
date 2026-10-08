<#
.SYNOPSIS
  把 config 下 yml 里的**明文私钥**导出到未跟踪的 secrets.local.env，并把 yml 改成环境变量注入。

.DESCRIPTION
  背景（2026-09-30 审计）：`houduan/TiMi/config/*.yml` 里有多个文件含明文私钥且已被 git 跟踪，
  任何人 clone 仓库即可拿走资金池私钥。完整处置需要"先转资产、再换密钥"（见
  千万次-生产部署清单.md §3.1），那一步只有 owner 能做；本脚本负责**工程侧的一半**：

    1. 从既有 yml 读出私钥 → 写入 `config/secrets.local.env`（未跟踪、已加 .gitignore）；
    2. 把 yml 里的 `Private:` / `EvmPrivate:` / `HotWalletPrivate:` 清空，
       并在**对应段**补上 `privateKeyEnv: "变量名"`（按段判断，缺哪个补哪个）；
    3. 顺带删掉已废弃的 `MinePool` / `BofiPool` / `TronNode` 段（代码里已无消费者）。

  清空 yml **不等于**密钥已安全 —— 它已经进了 git 历史。真正的修复是"视为已泄漏 → 转资产 → 换密钥
  → 清历史"。本脚本只是让"从这一刻起的 HEAD 不再泄漏"，并为后续轮换铺好环境变量通道。

  幂等：可重复执行；已迁移过的文件只会补齐缺失的 privateKeyEnv 接线，不会重复清空或覆盖已有变量。

.PARAMETER DryRun
  只打印将要做的改动，不写文件。

.PARAMETER IncludePrivateChain
  连 `etc.privchain.yml` 一起处理（默认跳过：里面是 hardhat 公开测试私钥，全网教程都在用、无资金价值，
  且私链 E2E 脚本依赖该文件里的密钥字段）。

.EXAMPLE
  powershell -File scripts/export-secrets.ps1 -DryRun     # 先看要改什么
  powershell -File scripts/export-secrets.ps1             # 真正执行
#>
[CmdletBinding()]
param(
  [switch]$DryRun,
  [switch]$IncludePrivateChain
)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$CfgDir = Join-Path $Root 'houduan\TiMi\config'
# 除了落地目录，仓库里还有一份"原始代码快照"（crowd-master-*）同样带着明文私钥，
# 是在清理脚本的门禁扫描里才发现的（2026-09-30）—— 一并迁移，否则 clone 仓库的人照样能拿到密钥。
$CfgDirs = @($CfgDir)
$CfgDirs += @(Get-ChildItem -Path (Join-Path $Root 'houduan') -Directory -Filter 'crowd-master-*' -ErrorAction SilentlyContinue |
  ForEach-Object { Join-Path $_.FullName 'config' } | Where-Object { Test-Path $_ })
$SecretsFile = Join-Path $CfgDir 'secrets.local.env'

# 段名 → 环境变量名（与 config/secrets.env.example 保持一致）
$EnvNames = @{
  'KtoPool'  = 'KTO_POOL_PRIVATE_KEY'
  'TronPool' = 'TRON_POOL_PRIVATE_KEY'
  'Burn'     = 'BURN_EVM_PRIVATE_KEY'
  'Withdraw' = 'HOT_WALLET_PRIVATE_KEY'
}
# 密钥字段 → 允许出现在哪些段（EvmPrivate 属 Burn，HotWalletPrivate 属 Withdraw）
$KeyFields = @{
  'Private'          = @('KtoPool', 'TronPool', 'Burn', 'Withdraw')
  'EvmPrivate'       = @('Burn')
  'HotWalletPrivate' = @('Withdraw')
}
# 已废弃段：代码里已无消费者（2026-09-30 复核），连同其缩进子行一起删除
$DeprecatedSections = @('MinePool', 'BofiPool', 'TronNode')

$files = @(foreach ($d in $CfgDirs) { Get-ChildItem -Path $d -Filter '*.yml' -File -ErrorAction SilentlyContinue }) | Sort-Object FullName
$found = @{}      # envName -> value
$changed = New-Object System.Collections.Generic.List[string]

foreach ($f in $files) {
  if (-not $IncludePrivateChain -and $f.Name -eq 'etc.privchain.yml') {
    Write-Host "[跳过] $($f.Name)（hardhat 公开测试私钥；如需处理加 -IncludePrivateChain）" -ForegroundColor DarkGray
    continue
  }

  $lines = [System.IO.File]::ReadAllLines($f.FullName)

  # ---- 预处理：每行所属段 + 每段是否已有 privateKeyEnv（必须**按段**判断） ----
  $sectionOf = New-Object 'string[]' $lines.Count
  $cur = ''
  for ($i = 0; $i -lt $lines.Count; $i++) {
    if ($lines[$i] -match '^([A-Za-z][A-Za-z0-9_]*)\s*:\s*$') { $cur = $Matches[1] }
    $sectionOf[$i] = $cur
  }
  $hasEnv = @{}
  for ($i = 0; $i -lt $lines.Count; $i++) {
    $s = $sectionOf[$i]
    if ($s -and $lines[$i] -match '^\s*privateKeyEnv\s*:') { $hasEnv[$s] = $true }
  }

  # ---- 主扫描 ----
  $out = New-Object System.Collections.Generic.List[string]
  $fileChanged = $false
  $cur = ''
  $skipIndented = $false
  foreach ($line in $lines) {
    if ($line -match '^([A-Za-z][A-Za-z0-9_]*)\s*:\s*$') {
      $cur = $Matches[1]
      $skipIndented = ($DeprecatedSections -contains $cur)
      if ($skipIndented) { $fileChanged = $true; continue }
      $out.Add($line)
      if ($EnvNames.ContainsKey($cur) -and -not $hasEnv[$cur]) {
        $out.Add("  privateKeyEnv: `"$($EnvNames[$cur])`"")
        $hasEnv[$cur] = $true
        $fileChanged = $true
      }
      continue
    }
    if ($skipIndented) {
      if ($line -match '^\s+\S') { $fileChanged = $true; continue }
      if ($line.Trim() -eq '') { continue }
      $skipIndented = $false
    }
    $handled = $false
    foreach ($field in $KeyFields.Keys) {
      if ($line -match "^(\s*)$field\s*:\s*(.*)$") {
        $indent = $Matches[1]
        # ⚠ 必须先剥掉"引号外的行尾注释"再判断有没有值：
        #   迁移后的行形如 `Private: ""   # 已迁出：见 KTO_POOL_PRIVATE_KEY`，
        #   直接取 (.*) 会把注释当密钥值读出来（实测踩过：干跑时把注释写成了"私钥"）。
        $rest = $Matches[2]
        $quote = $null; $cut = -1
        for ($c = 0; $c -lt $rest.Length; $c++) {
          $ch = $rest[$c]
          if ($quote) { if ($ch -eq $quote) { $quote = $null } }
          elseif ($ch -eq '"' -or $ch -eq "'") { $quote = $ch }
          elseif ($ch -eq '#') { $cut = $c; break }
        }
        if ($cut -ge 0) { $rest = $rest.Substring(0, $cut) }
        $val = $rest.Trim().Trim('"', "'")
        if ($val -and ($KeyFields[$field] -contains $cur)) {
          $envName = $EnvNames[$cur]
          if (-not $found.ContainsKey($envName)) { $found[$envName] = $val }
          $out.Add("${indent}${field}: `"`"   # 已迁出：见 $envName（config/secrets.local.env）")
          $fileChanged = $true
        } else {
          # 值为空（含已迁移过的行）→ 原样保留，保证幂等
          $out.Add($line)
        }
        $handled = $true
        break
      }
    }
    if ($handled) { continue }
    $out.Add($line)
  }

  if ($fileChanged) {
    $changed.Add($f.Name)
    if ($DryRun) {
      Write-Host "[将修改] $($f.Name)" -ForegroundColor Yellow
    } else {
      [System.IO.File]::WriteAllText($f.FullName, (($out -join "`r`n").TrimEnd() + "`r`n"), (New-Object System.Text.UTF8Encoding($false)))
      Write-Host "[已修改] $($f.Name)" -ForegroundColor Green
    }
  }
}

Write-Host ""
Write-Host "=== 从 yml 读到的私钥（将写入 $SecretsFile） ==="
if ($found.Count -eq 0) { Write-Host "（无：已迁移过或本就为空）" -ForegroundColor DarkGray }
foreach ($k in ($found.Keys | Sort-Object)) {
  $v = [string]$found[$k]
  Write-Host ("  {0} = {1}...(len={2})" -f $k, $v.Substring(0, [Math]::Min(8, $v.Length)), $v.Length)
}

if (-not $DryRun) {
  $merged = @{}
  if (Test-Path $SecretsFile) {
    foreach ($l in [System.IO.File]::ReadAllLines($SecretsFile)) {
      if ($l -match '^\s*([A-Z_]+)\s*=\s*(.*)$' -and $Matches[2].Trim()) { $merged[$Matches[1]] = $Matches[2].Trim() }
    }
  }
  foreach ($k in $found.Keys) { $merged[$k] = $found[$k] }
  if ($merged.Count -gt 0) {
    $sb = New-Object System.Text.StringBuilder
    [void]$sb.AppendLine("# 千万次 - 本机/生产密钥（不要提交；.gitignore 已忽略 config/secrets*.env）")
    [void]$sb.AppendLine("# 由 scripts/export-secrets.ps1 生成/更新；新增变量请同步 config/secrets.env.example")
    foreach ($k in ($merged.Keys | Sort-Object)) { [void]$sb.AppendLine("$k=$($merged[$k])") }
    [System.IO.File]::WriteAllText($SecretsFile, $sb.ToString(), (New-Object System.Text.UTF8Encoding($false)))
    Write-Host "[写入] $SecretsFile（$($merged.Count) 个变量）" -ForegroundColor Green
    try {
      $acl = Get-Acl $SecretsFile
      $acl.SetAccessRuleProtection($true, $false)
      $me = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
      $acl.SetAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule($me, 'FullControl', 'Allow')))
      Set-Acl -Path $SecretsFile -AclObject $acl
      Write-Host "[权限] 已收紧为仅当前用户可访问"
    } catch { Write-Host "[权限] 收紧失败（不影响功能）：$($_.Exception.Message)" -ForegroundColor DarkYellow }
  }
}

Write-Host ""
Write-Host "=== 摘要 ==="
Write-Host "  修改的 yml：$(if ($changed.Count) { $changed -join ', ' } else { '无' })"
Write-Host "  下一步："
Write-Host "    1) 本地起服务：start-local.bat 会自动加载 config\secrets.local.env"
Write-Host "    2) 生产：把变量写进 /opt/qianwanci/config/secrets.env（chmod 600）+ systemd EnvironmentFile"
Write-Host "    3) 密钥已进 git 历史，必须轮换（转资产 -> 换密钥）并清历史，见千万次-生产部署清单.md 3.1"
if ($DryRun) { Write-Host "  （当前是 -DryRun，未写入任何文件）" -ForegroundColor Yellow }
