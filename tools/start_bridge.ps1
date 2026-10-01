# ============================================================
# OAIprism 桥一键启动（PowerShell 版）
#
# 与 tools/start_bridge.cmd 等价，但用原生 PowerShell 语法，
# 便于在受控终端里直接调用（cmd.exe 在部分环境被策略禁用）。
#
# 启动两个服务：
#   8787  OAIprism 网关（Go）
#   8790  浏览器通道 sidecar（node + 真实 Chrome，穿透 Cloudflare）
# ============================================================
$ErrorActionPreference = "Continue"

$Repo = "F:\Code\Active\OAIprism"
$Node = "C:\Users\13080\.workbuddy\binaries\node\versions\22.22.2-3\node.exe"
$Oaiprism = "C:\Users\13080\AppData\Local\Temp\oaiprism.exe"
$LogDir = $env:TEMP

function Test-Port([int]$Port) {
    $c = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
    return [bool]$c
}

Write-Host "[1/3] 检查 8787 (OAIprism) ..."
if (Test-Port 8787) {
    Write-Host "    已在运行，跳过"
} else {
    if (-not (Test-Path $Oaiprism)) {
        Write-Host "    二进制不存在，构建中 ..."
        Push-Location $Repo
        & go build -o $Oaiprism ./cmd/oaiprism
        Pop-Location
    }
    Write-Host "    启动 OAIprism ..."
    Start-Process -FilePath $Oaiprism `
        -ArgumentList @("serve", "-config", "$Repo\configs\config.yaml", "-port", "8787") `
        -RedirectStandardOutput "$LogDir\oaiprism_8787.log" `
        -RedirectStandardError "$LogDir\oaiprism_8787.err.log" `
        -WindowStyle Hidden
}

Write-Host "[2/3] 检查 8790 (browser sidecar) ..."
if (Test-Port 8790) {
    Write-Host "    已在运行，跳过"
} else {
    Write-Host "    启动 sidecar（会拉起一个 Chrome 窗口，属正常）..."
    Start-Process -FilePath $Node `
        -ArgumentList @("$Repo\tools\browser_sidecar.js", "auto", "8790") `
        -RedirectStandardOutput "$LogDir\sidecar.log" `
        -RedirectStandardError "$LogDir\sidecar.err.log" `
        -WindowStyle Hidden
}

Write-Host "[3/3] 等待就绪并验证 ..."
$ok787 = $false; $ok790 = $false
for ($i = 0; $i -lt 20; $i++) {
    Start-Sleep -Seconds 2
    if (-not $ok787) { try { $ok787 = (Invoke-WebRequest -Uri "http://127.0.0.1:8787/healthz" -TimeoutSec 3 -UseBasicParsing).StatusCode -eq 200 } catch {} }
    if (-not $ok790) {
        try {
            # 405/409 都算通（能到应用层即可）
            Invoke-WebRequest -Uri "http://127.0.0.1:8790/api/maintenance" -Method POST -TimeoutSec 6 -UseBasicParsing | Out-Null
            $ok790 = $true
        } catch {
            if ($_.Exception.Response) { $ok790 = $true }
        }
    }
    if ($ok787 -and $ok790) { break }
}

Write-Host ("  8787 (网关):    " + $(if ($ok787) { "OK" } else { "未就绪" }))
Write-Host ("  8790 (sidecar): " + $(if ($ok790) { "OK" } else { "未就绪" }))
Write-Host ""
if ($ok787 -and $ok790) {
    Write-Host "完成。现在可直接运行 codex（模型 gpt-6.1-sol 经 8787 -> 8790 -> Prism）。"
} else {
    Write-Host "有服务未就绪：查看 %TEMP%\oaiprism_8787.log 与 %TEMP%\sidecar.log"
    Write-Host "sidecar 若因登录态失效起不来，确认 secrets\accounts.json 里 access_token 有效后重跑本脚本。"
}
