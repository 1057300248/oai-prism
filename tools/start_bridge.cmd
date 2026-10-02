@echo off
REM ============================================================
REM OAIprism 一键启动（cmd 版；PowerShell 环境优先用 start_bridge.ps1）
REM
REM 启动三件套（顺序敏感，自下而上）：
REM   8791  Sentinel token oracle（node + 真实 Chrome，只做 SDK.token 签发）
REM   8790  Go TLS 桥（oaiprism tlsbridge）
REM   8787  OAIprism 网关（oaiprism serve：OpenAI/Anthropic 兼容 API + Dashboard）
REM 链路：客户端 → 8787 → 8790 → prism.openai.com（8790 每请求向 8791 要 token）
REM
REM 用途：重启电脑后或服务掉线时执行一次，之后 codex / Dashboard 即可直接用。
REM 依赖：node、go（构建）、Chrome、secrets\accounts.json
REM ============================================================
setlocal

set REPO=F:\Code\Active\OAIprism
set NODE=C:\Users\13080\.workbuddy\binaries\node\versions\22.22.2-3\node.exe
set OAIPRISM=%REPO%\oaiprism.exe
set LOGDIR=%TEMP%

REM 始终重新构建：TEMP/旧二进制会悄悄落后于代码（曾因此排查过"改了没生效"）
echo [0/4] 构建 oaiprism.exe ...
pushd "%REPO%"
go build -o "%OAIPRISM%" ./cmd/oaiprism || (echo   构建失败 & popd & exit /b 1)
popd

echo [1/4] 检查 8787 (网关) ...
curl -s --max-time 2 http://127.0.0.1:8787/healthz >nul 2>&1
if %errorlevel%==0 (
  echo    已在运行，跳过
) else (
  echo    启动网关 ...
  start "oaiprism-8787" /min cmd /c ""%OAIPRISM%" serve -config "%REPO%\configs\config.yaml" -port 8787 > "%LOGDIR%\oaiprism_8787.log" 2>&1"
)

echo [2/4] 检查 8791 (Sentinel oracle) ...
curl -s --max-time 2 http://127.0.0.1:8791/healthz >nul 2>&1
if %errorlevel%==0 (
  echo    已在运行，跳过
) else (
  echo    启动 oracle（会打开一个 Chrome 窗口，属正常）...
  start "oracle-8791" /min cmd /c ""%NODE%" "%REPO%\tools\sentinel_oracle.js" auto 8791 > "%LOGDIR%\oracle.log" 2>&1"
  echo    等待 oracle 就绪（~20s）...
  timeout /t 20 /nobreak >nul
)

echo [3/4] 检查 8790 (Go TLS 桥) ...
curl -s --max-time 2 -o nul -X POST http://127.0.0.1:8790/api/maintenance >nul 2>&1
if %errorlevel%==0 (
  echo    已在运行，跳过
) else (
  echo    启动 TLS 桥 ...
  start "tlsbridge-8790" /min cmd /c ""%OAIPRISM%" tlsbridge -port 8790 -oracle http://127.0.0.1:8791 -accounts "%REPO%\secrets\accounts.json" > "%LOGDIR%\tlsbridge.log" 2>&1"
)

echo [4/4] 等待就绪并验证 ...
timeout /t 10 /nobreak >nul
curl -s --max-time 5 http://127.0.0.1:8787/healthz && echo  " <- 8787 OK"
curl -s --max-time 5 http://127.0.0.1:8791/healthz && echo " <- 8791 OK"
curl -s --max-time 5 -X POST http://127.0.0.1:8790/api/maintenance && echo " <- 8790 OK (405/200 都算通)"
echo.
echo 完事。现在可直接运行： codex  或打开 http://127.0.0.1:8787/ 的 Dashboard。
echo   链路：8787 -^> 8790 -^> Prism（token 由 8791 浏览器现签）。
echo   桥若报 503（上游抖动），重跑本脚本或单独重启 8790 即可。
endlocal
