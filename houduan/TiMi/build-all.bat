@echo off
rem ============================================================
rem  千万次/Npower 本地一键构建（Windows amd64）→ build\
rem  依赖：本机已装 Go；首次构建需能访问模块代理
rem  本机无外网（依赖已放在工作区内）时先设置：
rem    set GOMODCACHE=<仓库根>\.gomodcache
rem    set GOCACHE=<仓库根>\.gocache
rem    set GOFLAGS=-p=1
rem    set GOPROXY=off
rem  ⚠ 本文件必须保存为 CRLF 换行（cmd.exe 解析多行块/标签依赖 CRLF）
rem ============================================================
chcp 65001 >nul
cd /d "%~dp0"
where go >nul 2>nul
if errorlevel 1 (echo [错误] 未找到 go，请先安装并加入 PATH & exit /b 1)
if not exist build mkdir build

call :b build\npower.exe     .\cmd
call :b build\round.exe      .\cmd\round
call :b build\evmwatch.exe   .\cmd\evmwatch
call :b build\hashpower.exe  .\cmd\hashpower
call :b build\burn.exe       .\cmd\burn
call :b build\trshash.exe    .\cmd\trshash
call :b build\ops.exe        .\cmd\ops
call :b build\recon.exe      .\cmd\recon
call :b build\demo.exe       .\cmd\demo
call :b build\mining.exe     .\cmd\mining
call :b build\collecting.exe .\cmd\fundcollection
call :b build\manual.exe     .\cmd\manualcollection

echo.
echo 全部构建完成：build\
exit /b 0

:b
go build -o %1 %2
if errorlevel 1 (echo [失败] %2 & exit /b 1)
echo [已构建] %1
exit /b 0
