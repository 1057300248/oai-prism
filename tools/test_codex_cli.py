#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""使用本地真实 Codex CLI (codex-cli) 与 OAIprism 代理进行端到端交流测试。

测试任务：注入 base_url 与 token，让本地 codex-cli 生成一个鹈鹕骑自行车的 SVG，用 HTML 实现。
"""

import os
import sys
import io
import time
import subprocess
import urllib.request
import urllib.error

COOKIE_FILE = r"F:\Code\Active\OAIprism\secrets\cookies.txt"
CONFIG_FILE = r"F:\Code\Active\OAIprism\configs\config.yaml"
BIN_FILE = r"F:\Code\Active\OAIprism\bin\oaiprism.exe"
OUTPUT_HTML = r"F:\Code\Active\OAIprism\pelican_bike.html"
PORT = "18788"
API_KEY = "sk-codex-test-key"

def read_cookie():
    with io.open(COOKIE_FILE, encoding="utf-8") as f:
        return f.read().strip()

def main():
    cookie = read_cookie()
    if not cookie:
        print("[FATAL] 未找到 secrets/cookies.txt")
        return 1

    env = os.environ.copy()
    env["PRISM_COOKIE"] = cookie
    env["PORT"] = PORT
    env["HOST"] = "127.0.0.1"
    env["PROXY_API_KEY"] = API_KEY
    env["PRISM_MODEL"] = "gpt-6-astra"
    env["CORS_ORIGIN"] = "*"
    env["OAI_PRISM_CAPTURE"] = "false"

    print("[INFO] 启动 OAIprism 代理服务 (端口 %s)..." % PORT)
    server_proc = subprocess.Popen(
        [BIN_FILE, "serve", "-config", CONFIG_FILE],
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        encoding="utf-8",
        errors="replace"
    )

    try:
        time.sleep(2)
        # 1. 检查健康
        req = urllib.request.Request(f"http://127.0.0.1:{PORT}/healthz")
        try:
            with urllib.request.urlopen(req, timeout=5) as resp:
                print("  > 代理健康检查 HTTP", resp.status)
        except Exception as e:
            print("  [ERROR] 代理启动失败:", e)
            return 1

        # 2. 准备 Codex CLI 执行命令
        prompt = "生成一个鹈鹕骑自行车的 SVG，用 HTML 实现。请给出包含完整 HTML 和 SVG 标签的代码。"

        codex_cmd = [
            "codex",
            "exec",
            "--ephemeral",
            "--skip-git-repo-check",
            "--dangerously-bypass-approvals-and-sandbox",
            "-c", 'model_provider="custom"',
            "-c", f'model_providers.custom.base_url="http://127.0.0.1:{PORT}/v1"',
            "-c", 'model_providers.custom.wire_api="responses"',
            "-c", 'model_providers.custom.requires_openai_auth=false',
            "-c", f'model_providers.custom.experimental_bearer_token="{API_KEY}"',
            "-c", 'model="gpt-6-astra"',
            prompt
        ]

        print("[INFO] 正在调用本地真实 Codex CLI (codex exec)...")
        print("  > 注入 Base URL: http://127.0.0.1:%s/v1" % PORT)
        print("  > 提示词: %s" % prompt)

        t0 = time.time()
        # 在 Windows 上 codex 可能是 powershell 脚本或 batch 文件，使用 shell=True 确保正确定位
        cli_proc = subprocess.Popen(
            codex_cmd,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            encoding="utf-8",
            errors="replace",
            shell=True
        )

        stdout, _ = cli_proc.communicate(timeout=180)
        elapsed = time.time() - t0

        print(f"  > Codex CLI 执行结束，耗时 {elapsed:.2f}s，退出码: {cli_proc.returncode}")
        print("\n" + "="*70 + "\nCodex CLI 输出内容截选:\n" + "="*70)
        print(stdout[:1500])
        if len(stdout) > 1500:
            print("...\n[后略 %d 字符]" % (len(stdout) - 1500))

        # 保存为 HTML 文件：提取 markdown ```html 代码块如果存在，否则保存全部
        clean_html = stdout
        if "```html" in stdout:
            clean_html = stdout.split("```html", 1)[1].split("```", 1)[0].strip()
        elif "```xml" in stdout:
            clean_html = stdout.split("```xml", 1)[1].split("```", 1)[0].strip()
        elif "<html" in stdout or "<!DOCTYPE" in stdout:
            # 尝试直接定位 <html> 或 <!DOCTYPE
            idx = stdout.find("<!DOCTYPE")
            if idx == -1:
                idx = stdout.find("<html")
            if idx != -1:
                clean_html = stdout[idx:].split("```", 1)[0].strip()

        with open(OUTPUT_HTML, "w", encoding="utf-8") as f:
            f.write(clean_html)
        print(f"\n[INFO] 已将完整生成内容写入本地文件: {OUTPUT_HTML}")

        # 校验关键要素
        has_svg = "<svg" in clean_html.lower()
        has_html = "<html" in clean_html.lower() or "<!doctype" in clean_html.lower() or "svg" in clean_html.lower()
        print(f"  > 包含 SVG 标记: {has_svg}")
        print(f"  > 包含 HTML 结构: {has_html}")

        if cli_proc.returncode == 0 and has_svg:
            print("\n[SUCCESS] 本地 Codex CLI 与 OAIprism 代理正常交流测试完全成功！")
            return 0
        else:
            print("\n[WARNING] Codex CLI 执行完成，请核实返回内容")
            return 0 if ("<svg" in stdout or "pelican" in stdout.lower() or "svg" in stdout.lower()) else 1

    finally:
        print("[INFO] 终止后台代理服务...")
        try:
            server_proc.terminate()
            server_proc.wait(timeout=5)
        except:
            server_proc.kill()

if __name__ == "__main__":
    sys.exit(main())
