# ============================================================
# OAIprism 一键停止
#
# 停止三件套：网关(8787) + Go TLS 桥(8790) + Sentinel oracle(8791)
# 及其拉起的自动化 Chrome。只动本项目的进程，不影响日常浏览器。
# ============================================================
$ErrorActionPreference = "Continue"

function Stop-ByPort([int]$Port, [string]$Name) {
    $conns = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
    if (-not $conns) { Write-Host "  $Name ($Port): 未运行"; return }
    foreach ($c in ($conns | Select-Object -ExpandProperty OwningProcess -Unique)) {
        Stop-Process -Id $c -Force -ErrorAction SilentlyContinue
        Write-Host "  $Name ($Port): 已停止 PID $c"
    }
}

Write-Host "停止 OAIprism 服务栈 ..."
Stop-ByPort 8787 "网关"
Stop-ByPort 8790 "TLS 桥"
# 8791 必须停：oracle 是 node 常驻 + 一个自动化 Chrome，
# 漏掉它会留下孤儿进程（先前的 stop 脚本就有这个遗漏）。
Stop-ByPort 8791 "Sentinel oracle"

# 清理自动化 Chrome（oracle 拉起的；按特征识别，不动日常浏览器）
$killed = 0
Get-CimInstance Win32_Process -Filter "name='chrome.exe'" -ErrorAction SilentlyContinue | ForEach-Object {
    $cl = $_.CommandLine
    if ($cl -and ($cl -match 'remote-debugging-pipe' -or $cl -match 'playwright')) {
        Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue
        $killed++
    }
}
Write-Host "  自动化 Chrome: 清理 $killed 个"
Write-Host "完成。重启请运行 tools\start_bridge.ps1（或重新登录自动拉起）。"
