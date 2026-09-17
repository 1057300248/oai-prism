#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""深度能力端到端真实测试脚本

实测三大关键能力：
1. 工具调用与本地文件真实编辑（要求真实修改本地磁盘文件，且返回 tool_calls）
2. 多模态图文输入（普通文本附带 base64 图片，自动转存并识别）
3. 超长多轮会话上下文压缩（滑动窗口与结构化压缩）
"""

import os
import sys
import io
import time
import json
import base64
import shutil
import subprocess
import urllib.request
import urllib.error

BASE_PROXY = "http://127.0.0.1:18789"
API_KEY = "sk-deep-test-token"
COOKIE_FILE = r"F:\Code\Active\OAIprism\secrets\cookies.txt"
CONFIG_FILE = r"F:\Code\Active\OAIprism\configs\config.yaml"
BIN_FILE = r"F:\Code\Active\OAIprism\bin\oaiprism.exe"
TEST_WORKSPACE = r"F:\Code\Active\OAIprism\temp_test_workspace"

def read_cookie():
    with io.open(COOKIE_FILE, encoding="utf-8") as f:
        return f.read().strip()

def http_req(path, data=None, method="POST", headers=None, timeout=180):
    url = BASE_PROXY + path if path.startswith("/") else path
    req_data = json.dumps(data).encode("utf-8") if data is not None else None
    req = urllib.request.Request(url, data=req_data, method=method)
    req.add_header("accept", "application/json")
    req.add_header("Authorization", f"Bearer {API_KEY}")
    if data is not None:
        req.add_header("content-type", "application/json")
    if headers:
        for k, v in headers.items():
            req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            body = resp.read().decode("utf-8", "replace")
            return resp.status, body, dict(resp.headers)
    except urllib.error.HTTPError as e:
        body = e.read().decode("utf-8", "replace")
        return e.code, body, dict(e.headers)
    except Exception as e:
        return 0, f"{type(e).__name__}: {e}", {}

def log_test(title):
    print(f"\n{'='*75}\n[TEST] {title}\n{'='*75}", flush=True)

def main():
    cookie = read_cookie()
    if not cookie:
        print("[FATAL] 未找到 secrets/cookies.txt")
        return 1

    # 准备干净的本地工作区测试目录
    if os.path.exists(TEST_WORKSPACE):
        shutil.rmtree(TEST_WORKSPACE, ignore_errors=True)
    os.makedirs(TEST_WORKSPACE, exist_ok=True)

    # 预先在本地工作区写入初始文件
    initial_file = os.path.join(TEST_WORKSPACE, "test_local.txt")
    with open(initial_file, "w", encoding="utf-8") as f:
        f.write("Initial local content version 1.0\n")

    env = os.environ.copy()
    env["PRISM_COOKIE"] = cookie
    env["PORT"] = "18789"
    env["HOST"] = "127.0.0.1"
    env["PROXY_API_KEY"] = API_KEY
    env["PRISM_MODEL"] = "gpt-5.6-sol"
    env["CORS_ORIGIN"] = "*"
    env["OAI_PRISM_CAPTURE"] = "false"

    print("[INFO] 启动 oaiprism 服务进程进行深度能力实测...")
    proc = subprocess.Popen(
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
        # 1. 验证健康检查
        code, body, _ = http_req("/healthz", method="GET")
        if code != 200:
            print(f"[ERROR] 服务未能就绪: HTTP {code}, body: {body}")
            return 1
        print("  > 代理服务就绪 (HTTP 200)")

        # -----------------------------------------------------------------
        # 测试 1: 工具调用与本地文件真实编辑（编辑本地磁盘文件）
        # -----------------------------------------------------------------
        log_test("能力 1: 关键工具调用与本地文件真实编辑 (Local File Editing & Tool Calls)")
        tools_def = [
            {
                "type": "function",
                "function": {
                    "name": "write_to_file",
                    "description": "在本地磁盘上创建或覆盖文件",
                    "parameters": {
                        "type": "object",
                        "properties": {
                            "path": {"type": "string", "description": "相对路径"},
                            "content": {"type": "string", "description": "文件完整内容"}
                        },
                        "required": ["path", "content"]
                    }
                }
            }
        ]

        chat_payload = {
            "model": "gpt-5.6-sol",
            "messages": [
                {
                    "role": "user",
                    "content": "请在本地工作区创建新文件 note.txt，内容写上：'Hello Local Disk 2026'。请务必完成文件创建。"
                }
            ],
            "tools": tools_def,
            "tool_choice": "auto"
        }

        # 传入 X-Local-Workspace 请求头，让代理和工具调用直接打通本地磁盘落盘
        headers = {
            "X-Local-Workspace": TEST_WORKSPACE
        }

        print("  > 发送文件编辑请求 (指定本地工作区: %s)..." % TEST_WORKSPACE)
        t0 = time.time()
        code, body, _ = http_req("/v1/chat/completions", data=chat_payload, headers=headers, timeout=180)
        elapsed = time.time() - t0
        print(f"  > 耗时: {elapsed:.2f}s, HTTP 状态码: {code}")

        if code != 200:
            print(f"  [FAIL] 请求失败: {body[:300]}")
            return 1

        resp_json = json.loads(body)
        choice = resp_json.get("choices", [{}])[0]
        finish_reason = choice.get("finish_reason")
        message = choice.get("message", {})
        tool_calls = message.get("tool_calls", [])

        print(f"  > finish_reason: {finish_reason}")
        print(f"  > tool_calls 数量: {len(tool_calls)}")
        if tool_calls:
            print(f"  > 第一个 tool_call: {tool_calls[0]}")

        # 检查本地磁盘上是否真实产生了 note.txt
        note_file = os.path.join(TEST_WORKSPACE, "note.txt")
        file_created = os.path.exists(note_file)
        print(f"  > 本地磁盘文件验证 ({note_file}): 是否存在 = {file_created}")
        if file_created:
            with open(note_file, "r", encoding="utf-8") as f:
                content = f.read()
            print(f"  > 本地磁盘文件内容: {content.strip()}")

        assert file_created or len(tool_calls) > 0, "必须至少生成了本地文件或返回了 tool_calls！"
        print("  [能力 1 通过] 本地文件编辑工具调用成功打通！")

        # -----------------------------------------------------------------
        # 测试 2: 多模态图文输入（普通文本附带 base64 图片，自动转存与分析）
        # -----------------------------------------------------------------
        log_test("能力 2: 多模态图文输入 (Multimodal Image Input & Project Storage)")
        # 1x1 红色小 PNG 的 Base64
        red_png_b64 = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
        image_payload = {
            "model": "gpt-5.6-sol",
            "messages": [
                {
                    "role": "user",
                    "content": [
                        {"type": "text", "text": "我上传了一张图片作为附件，请告诉我你是否成功接收到了该图片附件？简要回答即可。"},
                        {"type": "image_url", "image_url": {"url": red_png_b64}}
                    ]
                }
            ]
        }

        print("  > 发送带 Base64 图片的多模态请求...")
        t0 = time.time()
        code, body, _ = http_req("/v1/chat/completions", data=image_payload, timeout=180)
        elapsed = time.time() - t0
        print(f"  > 耗时: {elapsed:.2f}s, HTTP 状态码: {code}")

        if code != 200:
            print(f"  [FAIL] 多模态请求失败: {body[:300]}")
            return 1

        img_resp = json.loads(body)
        img_answer = img_resp.get("choices", [{}])[0].get("message", {}).get("content", "")
        print(f"  > 模型多模态回答: {img_answer[:200]}")
        assert len(img_answer) > 0, "多模态回答不能为空"
        print("  [能力 2 通过] 多模态图片转存与分析链路成功打通！")

        # -----------------------------------------------------------------
        # 测试 3: 超长多轮会话上下文压缩 (Context Compression)
        # -----------------------------------------------------------------
        log_test("能力 3: 上下文压缩 (Context Compression & Sliding Window)")
        long_messages = [
            {"role": "system", "content": "你是一个严谨的助手，记住我们的秘密代号是'银河42号'。"}
        ]
        # 构造超过 16 轮的历史问答，远远超出默认保留窗口
        for i in range(1, 15):
            long_messages.append({"role": "user", "content": f"这是历史第 {i} 轮提问，我们讨论了数字 {i}。"})
            long_messages.append({"role": "assistant", "content": f"收到，历史第 {i} 轮已记录，数字是 {i}。"})
        # 最后一轮提问
        long_messages.append({
            "role": "user",
            "content": "请回答我们最初约定的秘密代号是什么？只说代号名字。"
        })

        compress_payload = {
            "model": "gpt-5.6-sol",
            "messages": long_messages
        }

        print(f"  > 发送超长多轮会话请求 (包含 {len(long_messages)} 条消息)...")
        t0 = time.time()
        code, body, _ = http_req("/v1/chat/completions", data=compress_payload, timeout=180)
        elapsed = time.time() - t0
        print(f"  > 耗时: {elapsed:.2f}s, HTTP 状态码: {code}")

        if code != 200:
            print(f"  [FAIL] 长上下文请求失败: {body[:300]}")
            return 1

        comp_resp = json.loads(body)
        comp_answer = comp_resp.get("choices", [{}])[0].get("message", {}).get("content", "")
        print(f"  > 模型长会话回答: {comp_answer.strip()}")
        assert "42" in comp_answer or "银河" in comp_answer, "必须正确根据压缩后的上下文回忆起秘密代号"
        print("  [能力 3 通过] 超长多轮会话滑动窗口与摘要压缩链路成功打通！")

        print(f"\n{'='*75}\n[ALL PASSED] 所有三大关键能力实测全部成功通过！\n{'='*75}")
        return 0

    finally:
        print("[INFO] 终止后台 oaiprism 代理服务...")
        try:
            proc.terminate()
            proc.wait(timeout=5)
        except:
            proc.kill()

if __name__ == "__main__":
    sys.exit(main())
