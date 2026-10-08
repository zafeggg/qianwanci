@echo off
rem ============================================================
rem  千万次（TiMi）Linux amd64 交叉编译 → build\linux\
rem  生产必需（缺一不可）：
rem    npower   = 主服务（HTTP + /api，首启 AutoMigrate）
rem    round    = 轮次结算器（开轮/结束轮/三进一出结算/结算补偿巡检）
rem    evmwatch = EVM(TM) 充值监听（不入账则用户充值永远不到账）
rem    hashpower= 每日算力产币（倒2/倒3 爆仓折算算力的产出）
rem    burn     = 手续费链上销毁（TM 通缩至 7777）
rem    trshash  = KTO/TRON 充值监听（非 EVM 阶段/币种）
rem  运维与联调：ops=运维后台(:5000) / recon=对账巡检 / demo=演示网关 / mining / collecting / manual
rem  生产参数：-trimpath -ldflags "-s -w" 去路径与符号，减小体积
rem  用法：build-linux.bat [单程序名]  不传参 = 全部
rem  注意：本文件必须保存为 GBK/ANSI 编码（cmd 按 ANSI 解析批处理）
rem  本机无外网（依赖已放在工作区内）时先设置：
rem    set GOMODCACHE=<仓库根>\.gomodcache
rem    set GOCACHE=<仓库根>\.gocache
rem    set GOFLAGS=-p=1
rem    set GOPROXY=off
rem ============================================================
cd /d "%~dp0"

rem ---- 定位 go：先查 PATH，再探测常见安装位置 ----
where go >nul 2>nul
if errorlevel 1 (
    if exist "D:\go\bin\go.exe" set "PATH=D:\go\bin;%PATH%"
)
where go >nul 2>nul
if errorlevel 1 (
    if exist "C:\Go\bin\go.exe" set "PATH=C:\Go\bin;%PATH%"
)
where go >nul 2>nul
if errorlevel 1 (
    echo [错误] 未找到 go，请安装或将其 bin 目录加入 PATH
    exit /b 1
)

if not exist build\linux mkdir build\linux

set GOOS=linux
set GOARCH=amd64
set CGO_ENABLED=0

if not "%~1"=="" goto :single

call :b build\linux\npower     .\cmd
call :b build\linux\round      .\cmd\round
call :b build\linux\evmwatch   .\cmd\evmwatch
call :b build\linux\hashpower  .\cmd\hashpower
call :b build\linux\burn       .\cmd\burn
call :b build\linux\trshash    .\cmd\trshash
call :b build\linux\ops        .\cmd\ops
call :b build\linux\recon      .\cmd\recon
call :b build\linux\demo       .\cmd\demo
call :b build\linux\mining     .\cmd\mining
call :b build\linux\collecting .\cmd\fundcollection
call :b build\linux\manual     .\cmd\manualcollection
echo.
echo 全部交叉编译完成：build\linux\
exit /b 0

:single
rem npower 主程序源码在 .\cmd 根，其余程序在 .\cmd\<名称> 子目录
if /i "%~1"=="npower" (set "SRC=.\cmd") else (set "SRC=.\cmd\%~1")
call :b build\linux\%~1 %SRC%
if errorlevel 1 exit /b 1
echo 完成：build\linux\%~1
exit /b 0

:b
go build -trimpath -ldflags "-s -w" -o %1 %2
if errorlevel 1 (echo [失败] %2 & exit /b 1)
echo [已构建] %1
exit /b 0
