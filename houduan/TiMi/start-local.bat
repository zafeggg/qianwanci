@echo off
chcp 65001 >nul
title 千万次(TiMi) 本地一键启动
cd /d "%~dp0"

rem ============================================================
rem  加载本机密钥（2026-09-30 新增）
rem  yml 里的明文私钥已迁到 config\secrets.local.env（未跟踪、不进仓库）。
rem  该文件由 scripts\export-secrets.ps1 生成，或手动从 secrets.env.example 复制填写。
rem  没有该文件也能启动：此时资金池私钥为空，只影响需要签名资金池的链路（本地记账/联调不受影响）。
rem ============================================================
if exist "config\secrets.local.env" (
  echo  已加载本机密钥 config\secrets.local.env
  for /f "usebackq eol=# tokens=1,* delims==" %%A in ("config\secrets.local.env") do (
    if not "%%~A"=="" set "%%~A=%%~B"
  )
) else (
  echo  [提示] 未找到 config\secrets.local.env —— 资金池私钥为空
  echo         需要签名资金池的功能请先执行: powershell -File ..\..\scripts\export-secrets.ps1
)

echo ============================================
echo  步骤1/4  检查并启动 MySQL 与 Redis 服务
echo ============================================
sc query MySQL80 2>nul | findstr /i "RUNNING" >nul || ( echo  启动 MySQL80 ... & net start MySQL80 )
sc query Redis 2>nul | findstr /i "RUNNING" >nul || ( echo  启动 Redis ... & net start Redis )

echo.
echo ============================================
echo  步骤2/4  启动 npower 主服务   (:3000)
echo ============================================
start "npower" build\npower.exe -f config\etc.local.yml
timeout /t 4 /nobreak >nul

echo.
echo ============================================
echo  步骤3/4  启动演示网关         (:3001)
echo ============================================
start "demo" build\demo.exe -f config\demo.local.yml -api http://127.0.0.1:3000 -web cmd\demo\web
timeout /t 2 /nobreak >nul

echo.
echo ============================================
echo  步骤4/4  启动前端 DApp dev    (:3002)
echo ============================================
rem 前端位于仓库根的 dapp-vue（本脚本在 houduan\TiMi，故上跳两级）
if exist "%~dp0..\..\dapp-vue\package.json" (
  start "dapp-vue" /D "%~dp0..\..\dapp-vue" cmd /c "npm run dev"
) else (
  echo  [跳过] 未找到 %~dp0..\..\dapp-vue\package.json
)
timeout /t 2 /nobreak >nul

echo.
echo  DApp 页面: http://127.0.0.1:3002
echo  演示页面:  http://127.0.0.1:3001
echo  主服务:    http://127.0.0.1:3000
echo  健康检查:  curl http://127.0.0.1:3000/home/version
echo.
echo  可选: 轮次结算器 build\round.exe -f config\round.local.yml（开轮/整分结算）
echo  提示: 若改过 Go 代码，请先重新编译 build\*.exe 再执行本脚本。
pause
