@echo off
REM ============================================================
REM OAIprism 桥一键启动：OAIprism(8787) + 浏览器通道 sidecar(8790)
REM
REM 用途：重启电脑后或服务掉线时执行一次，之后 codex 即可直接用
REM       gpt-6.1-sol（Prism 满血模型）。
REM
REM 依赖：node（sidecar）、oaiprism.exe、Chrome
REM ============================================================
setlocal

set REPO=F:\Code\Active\OAIprism
set NODE=C:\Users\13080\.workbuddy\binaries\node\versions\22.22.2-3\node.exe
set OAIPRISM=C:\Users\13080\AppData\Local\Temp\oaiprism.exe
set LOGDIR=%TEMP%

echo [1/3] 检查 8787 (OAIprism) ...
curl -s --max-time 2 http://127.0.0.1:8787/healthz >nul 2>&1
if %errorlevel%==0 (
  echo    已在运行，跳过
) else (
  if not exist "%OAIPRISM%" (
    echo    二进制不存在，先构建...
    pushd "%REPO%"
    go build -o "%OAIPRISM%" ./cmd/oaiprism || (echo   构建失败 & popd & exit /b 1)
    popd
  )
  echo    启动 OAIprism ...
  start "oaiprism-8787" /min cmd /c ""%OAIPRISM%" serve -config "%REPO%\configs\config.yaml" -port 8787 > "%LOGDIR%\oaiprism_8787.log" 2>&1"
)

echo [2/3] 检查 8790 (browser sidecar) ...
curl -s --max-time 2 -o nul -X POST http://127.0.0.1:8790/api/maintenance >nul 2>&1
if %errorlevel%==0 (
  echo    已在运行，跳过
) else (
  echo    启动 sidecar（会打开一个 Chrome 窗口，属正常）...
  start "sidecar-8790" /min cmd /c ""%NODE%" "%REPO%\tools\browser_sidecar.js" auto 8790 > "%LOGDIR%\sidecar.log" 2>&1"
)

echo [3/3] 等待就绪并验证 ...
timeout /t 12 /nobreak >nul
curl -s --max-time 5 http://127.0.0.1:8787/healthz && echo  " <- 8787 OK"
curl -s --max-time 5 -X POST http://127.0.0.1:8790/api/maintenance && echo " <- 8790 OK (405 也算通)"
echo.
echo 完事。现在可直接运行： codex
echo   模型 gpt-6.1-sol 经 8787 -^> 8790 -^> Prism。
echo   sidecar 若因 cookie 过期失效，删掉其窗口重启本脚本即可自愈。
endlocal
