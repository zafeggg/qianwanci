<#
.SYNOPSIS
  清理 git 历史里已泄漏的明文密钥（配合 scripts/export-secrets.ps1 使用）。

.DESCRIPTION
  先读这段：**清历史 ≠ 修好漏洞**。
  密钥一旦进过 git 历史就必须视为已泄漏，正确顺序是：
    ① 把资产转到新地址（轮换）→ ② 换密钥 / 改环境变量注入 → ③ 清历史。
  只做 ③ 是自我安慰；只做 ② 而不转资产则资产仍在旧地址上。
  owner 侧执行清单见 千万次-生产部署清单.md §3.1。

  本脚本用**重建仓库**的方式清历史（默认且推荐）：
    - 先做安全门禁：扫描"当前被跟踪文件"里是否还有明文私钥，有则**拒绝执行**；
    - 把旧 `.git` 改名为 `.git.bak-<时间戳>`（完整保留，可随时还原）；
    - 用当前（已清理的）工作区 `git init` + 一次提交，历史里不再有任何旧对象；
    - 校验新仓库历史中搜不到私钥痕迹。

  为什么不用 `git filter-branch` 重写历史：
    - 它在本机（Windows + PowerShell 5.1）会被 refs/original 与 stderr 行为反复咬（实测 exit 1、报错信息被吞）；
    - 本仓库**没有 remote、只有个人提交历史**，"重建 + 备份旧 .git"更彻底、可验证、可回退。
  若你确实要保留逐次提交历史：安装 git-filter-repo 后按部署清单 §3.1 的方案 A 执行（需联网装工具）。

.PARAMETER RepoPath
  要处理的仓库路径（默认：本脚本所在仓库）。**建议先在克隆上演练**。

.PARAMETER Message
  新初始提交的说明。

.PARAMETER DryRun
  只打印将要执行的命令，不做改动（仍会执行安全门禁扫描）。

.EXAMPLE
  powershell -File scripts/purge-git-history.ps1 -RepoPath $env:TEMP\try -DryRun
  powershell -File scripts/purge-git-history.ps1 -RepoPath $env:TEMP\try
#>
[CmdletBinding()]
param(
  [string]$RepoPath = '',
  [string]$Message = 'init: 千万次（TiMi）—— 历史重建（已去除明文密钥与旧提交历史）',
  [switch]$DryRun
)

$ErrorActionPreference = 'Stop'
if (-not $RepoPath) { $RepoPath = Split-Path -Parent $PSScriptRoot }
$gitDir = Join-Path $RepoPath '.git'
if (-not (Test-Path $gitDir)) { throw "不是 git 仓库：$RepoPath" }

# 原生命令包装：PS 5.1 下原生命令写 stderr 会被 $ErrorActionPreference='Stop' 当成终止错误
function Invoke-Git {
  param([Parameter(ValueFromRemainingArguments = $true)][string[]]$GitArgs)
  $prev = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  try {
    $output = & git @GitArgs 2>&1 | ForEach-Object { "$_" }
    $code = $LASTEXITCODE
  } finally { $ErrorActionPreference = $prev }
  [pscustomobject]@{ Output = @($output); Code = $code }
}

Write-Host "=== 密钥历史清理（重建仓库模式） ===" -ForegroundColor Cyan
Write-Host "  仓库: $RepoPath"

Push-Location $RepoPath
try {
  # ---- 门禁：yml 里的明文私钥必须已经迁出（这类我们能用环境变量替代，是硬性要求） ----
  # 用 git grep（而不是 Select-String + Test-Path）：git 自己处理非 ASCII 路径的引号转义，
  # 而 PowerShell 的 Test-Path 遇到 `git ls-files` 输出的八进制转义路径会直接报错（实测踩过）。
  Write-Host "`n[1/4] 安全门禁：扫描当前被跟踪文件" -ForegroundColor Cyan
  $ymlPattern = '^\s*(Private|EvmPrivate|HotWalletPrivate)\s*:\s*["'']?[0-9a-zA-Z]{32,}'
  $grepArgs = @('grep','-n','-I','-E',$ymlPattern)
  $g1 = Invoke-Git @grepArgs
  # [hardhat-public-test-key] 标记 = hardhat 标准公开测试私钥（私链联调专用，公开值无资金），
  # 不算泄漏，放行；其余明文私钥一律拒绝。
  $ymlHits = @($g1.Output | Where-Object { $_ -and ($_ -notmatch 'hardhat-public-test-key') })
  if ($ymlHits.Count -gt 0) {
    Write-Host "  yml 里仍有明文私钥，拒绝重建（先执行 scripts/export-secrets.ps1 迁到环境变量）：" -ForegroundColor Red
    $ymlHits | Select-Object -First 20 | ForEach-Object { Write-Host "    $_" }
    throw '门禁未通过'
  }
  Write-Host "  ✓ yml 里没有明文私钥字段" -ForegroundColor Green

  # 门禁 2：**密钥文件本身**不能处于被跟踪状态
  #（克隆演练实测：若 .gitignore 的 secrets 规则还没提交，`git add -A` 会把 secrets.local.env 一起提交进新仓库）
  $lsSecretsArgs = @('ls-files','--','*secrets*.env','*.local.env')
  $secretsTracked = @((Invoke-Git @lsSecretsArgs).Output | Where-Object { $_ })
  if ($secretsTracked.Count -gt 0) {
    Write-Host "  以下密钥文件处于被跟踪状态，拒绝重建（请先 git rm --cached 并确认 .gitignore）：" -ForegroundColor Red
    $secretsTracked | ForEach-Object { Write-Host "    $_" }
    throw '门禁未通过（密钥文件被跟踪）'
  }
  Write-Host "  ✓ 没有密钥文件被跟踪" -ForegroundColor Green

  # 其它仍被跟踪的凭据文件：能做的是"轮换 + 停止跟踪"，脚本只提示不阻断
  #（例如 crypto/private.pem 是客户端通信密钥，换它需要前端同步发版）
  $grepPem = @('grep','-n','-I','-E','BEGIN (RSA )?PRIVATE KEY')
  $g2 = Invoke-Git @grepPem
  $pemHits = @($g2.Output | Where-Object { $_ })
  $lsCred = @('ls-files','--','*.env.development','*.env.production','*.pem')
  $g3 = Invoke-Git @lsCred
  $envHits = @($g3.Output | Where-Object { $_ })
  if ($pemHits.Count -gt 0 -or $envHits.Count -gt 0) {
    Write-Host "  ⚠ 仍有其它凭据类文件被跟踪（本脚本不自动处理，但请一并决策）：" -ForegroundColor Yellow
    @($pemHits + $envHits) | Select-Object -Unique | Select-Object -First 10 | ForEach-Object { Write-Host "      $_" }
    Write-Host "      处置建议：轮换后停止跟踪（加入 .gitignore），或在本次重建后一并删除" -ForegroundColor DarkGray
  }

  $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
  # ⚠ 备份必须放在**仓库工作区之外**：放在仓库里（如 .git.bak-xxx）会被随后的 `git add -A`
  #   当成普通目录提交进新仓库 —— 而它里面就是旧的对象库（含明文密钥的完整历史）。
  #   实测踩过：独立复核时 `git log --all -p` 仍能搜到私钥。
  $repoLeaf = Split-Path -Leaf (Resolve-Path $RepoPath).Path
  $backupDir = Join-Path (Split-Path -Parent (Resolve-Path $RepoPath).Path) ("$repoLeaf.git.bak-$stamp")
  $rvArgs = @('rev-list','--all','--count')
  $commitCount = (Invoke-Git @rvArgs).Output[0]
  Write-Host "  旧历史提交数：$commitCount（将完整保留到工作区之外：$backupDir）"

  # 提交身份：优先沿用旧仓库的 local 配置（克隆/CI 里常常没有全局身份，
  # 不处理的话重建会卡在 commit —— 实测在克隆演练里踩到）
  $idName = ''
  $idMail = ''
  $cfgPath = Join-Path $gitDir 'config'
  if (Test-Path $cfgPath) {
    $cfgText = [System.IO.File]::ReadAllText($cfgPath)
    if ($cfgText -match '(?m)^\s*name\s*=\s*(.+)$') { $idName = $Matches[1].Trim() }
    if ($cfgText -match '(?m)^\s*email\s*=\s*(.+)$') { $idMail = $Matches[1].Trim() }
  }
  if (-not $idName) { $idName = 'qianwanci' }
  if (-not $idMail) { $idMail = 'qianwanci@local' }
  Write-Host "  提交身份：$idName <$idMail>"

  if ($DryRun) {
    Write-Host "`n[2/4] （-DryRun）将执行：Move-Item .git $backupDir（工作区之外）" -ForegroundColor Yellow
    Write-Host "[3/4] （-DryRun）将执行：git init && git add -A && git -c user.name=... -c user.email=... commit"
    Write-Host "[4/4] （-DryRun）将执行：校验新历史无密钥"
    return
  }

  # ---- 备份旧 .git 并重建（失败自动回滚，避免留下"半个仓库"） ----
  Write-Host "`n[2/4] 备份旧 .git → $backupDir" -ForegroundColor Cyan
    Move-Item -LiteralPath $gitDir -Destination $backupDir -Force
  Write-Host "  已备份（还原：Remove-Item -Recurse -Force .git; Move-Item $backupDir .git）" -ForegroundColor Green

  try {
    Write-Host "`n[3/4] 用当前工作区重建仓库并提交" -ForegroundColor Cyan
    $initArgs = @('init','-q')
    $init = Invoke-Git @initArgs
    if ($init.Code -ne 0) { throw "git init 失败：$($init.Output -join "`n")" }
    # 纵深防御：即使工作区 .gitignore 缺规则，也不要把密钥文件 add 进去
    $excludeFile = Join-Path $gitDir 'info\exclude'
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $excludeFile) | Out-Null
    Add-Content -Path $excludeFile -Value "`n# 千万次：密钥文件绝不入库（purge-git-history.ps1 追加）`n*secrets*.env`n*.local.env" -Encoding UTF8
    $addArgs = @('add','-A')
    $null = Invoke-Git @addArgs
    $commitArgs = @('-c',"user.name=$idName",'-c',"user.email=$idMail",'commit','-q','-m',$Message)
    $cm = Invoke-Git @commitArgs
    if ($cm.Code -ne 0) { throw "git commit 失败：$($cm.Output -join "`n")" }
    $lsArgs = @('ls-files')
    $files = (Invoke-Git @lsArgs).Output.Count
    Write-Host "  完成：1 个初始提交，$files 个文件被跟踪" -ForegroundColor Green
  } catch {
    Write-Host "`n  重建失败，正在回滚到原仓库…" -ForegroundColor Red
    if (Test-Path $gitDir) { Remove-Item -Recurse -Force $gitDir }
    Move-Item -LiteralPath $backupDir -Destination $gitDir -Force
    Write-Host "  已回滚（原仓库完好）" -ForegroundColor Yellow
    throw
  }

  # ---- 校验 ----
  Write-Host "`n[4/4] 校验新仓库历史" -ForegroundColor Cyan
  $logArgs = @('log','--all','-p')
  $log = Invoke-Git @logArgs
  # 要求字段名后面**跟着实际值**：否则 Go 注释里的 `Withdraw.HotWalletPrivate` 这类说明文字会被误报（实测踩过）
  $hits = @($log.Output | Select-String -Pattern 'Private\s*:\s*["'']?[0-9a-zA-Z]{32,}|EvmPrivate\s*:\s*["'']?[0-9a-fA-F]{32,}|HotWalletPrivate\s*:\s*["'']?[0-9a-fA-F]{32,}|BEGIN (RSA )?PRIVATE KEY')
  # 分两类看：① yml/Go 里的私钥**字面量**（本脚本负责清除，必须为 0；
  #           带 [hardhat-public-test-key] 标记的行是公开测试私钥，放行）
  #            ② PEM 私钥块（crypto/private.pem 等仍在被跟踪 —— 换它要前端同步发版，属 owner 决策，只告警）
  $rawLiteralHits = @($log.Output | Select-String -Pattern 'Private\s*:\s*["'']?[0-9a-zA-Z]{32,}|EvmPrivate\s*:\s*["'']?[0-9a-fA-F]{32,}|HotWalletPrivate\s*:\s*["'']?[0-9a-fA-F]{32,}')
  $literalHits = @($rawLiteralHits | Where-Object { $_ -and ("$_" -notmatch 'hardhat-public-test-key') })
  $pemBlocks = @($log.Output | Select-String -Pattern 'BEGIN (RSA )?PRIVATE KEY')
  if ($literalHits.Count -gt 0) {
    Write-Host "  ✗ 新历史里仍能搜到配置/代码中的私钥字面量（请人工复核）：" -ForegroundColor Red
    $literalHits | Select-Object -First 5 | ForEach-Object { Write-Host "    $_" }
  } else {
    Write-Host "  ✓ 新历史（git log --all -p）里已搜不到配置/代码中的私钥字面量" -ForegroundColor Green
  }
  if ($pemBlocks.Count -gt 0) {
    $pemLsArgs = @('ls-files','--','*.pem')
    $pemFiles = @((Invoke-Git @pemLsArgs).Output | Where-Object { $_ -like '*.pem' })
    Write-Host "  ⚠ 仍有 PEM 私钥文件被跟踪（不属于本次历史清理范围，需另行处置：轮换后停止跟踪）：" -ForegroundColor Yellow
    @($pemFiles | Select-Object -Unique -First 6) | ForEach-Object { Write-Host "      $_" }
    Write-Host "      提示：crypto/private.pem 是客户端通信密钥，更换需前端同步发版（见部署清单 §3.1 表格）" -ForegroundColor DarkGray
  }
  $remArgs = @('remote')
  $remotes = @((Invoke-Git @remArgs).Output | Where-Object { $_ })
  if ($remotes.Count -gt 0) {
    Write-Host "  注意：本仓库有 remote —— $($remotes -join ', ')；重写历史后必须 force push，且协作者需重新克隆" -ForegroundColor Yellow
  } else {
    Write-Host "  本仓库无 remote，无需 force push" -ForegroundColor DarkGray
  }
  Write-Host "  旧仓库备份：$backupDir（确认无误后可删：Remove-Item -Recurse -Force $backupDir）"
} finally {
  Pop-Location
}

Write-Host ""
Write-Host "提醒：历史清理**不能替代密钥轮换**。若尚未转资产/换密钥，请先按 千万次-生产部署清单.md 3.1 执行。" -ForegroundColor Yellow
Write-Host "另：crypto/private.pem 等仍被跟踪的密钥文件已在本脚本门禁里被拦下 —— 请先用 export-secrets.ps1 或手工处理。" -ForegroundColor DarkGray
