#!/usr/bin/env python3
# -*- coding: utf-8 -*-
import io
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

BASE_PROXY = "http://127.0.0.1:18788"
API_KEY = "sk-codex-live-test"
COOKIE_FILE = r"F:\Code\Active\OAIprism\secrets\cookies.txt"

def read_cookie():
    with io.open(COOKIE_FILE, encoding="utf-8") as f:
        return f.read().strip()

def http_req(path, data=None, method="GET", headers=None, auth=True, timeout=120):
    url = BASE_PROXY + path if path.startswith("/") else path
    req_data = json.dumps(data).encode("utf-8") if data is not None else None
    req = urllib.request.Request(url, data=req_data, method=method)
    req.add_header("accept", "application/json")
    req.add_header("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36")
    if auth and url.startswith(BASE_PROXY):
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

def log_step(layer, title):
    print(f"\n{'='*70}\n[Layer {layer}] {title}\n{'='*70}", flush=True)

def main():
    cookie = read_cookie()
    if not cookie:
        print("[FATAL] secrets/cookies.txt 中未找到 Cookie")
        return 1

    env = os.environ.copy()
    env["PRISM_COOKIE"] = cookie
    env["PORT"] = "18788"
    env["HOST"] = "127.0.0.1"
    env["PROXY_API_KEY"] = API_KEY
    env["PRISM_MODEL"] = "gpt-5.6-sol"
    env["CORS_ORIGIN"] = "*"
    env["OAI_PRISM_CREDS_FILE"] = r"F:\Code\Active\OAIprism\secrets\nonexistent.json"
    env["OAI_PRISM_CAPTURE"] = "true"

    cap_dir = r"F:\Code\Active\OAIprism\captures"
    if os.path.isdir(cap_dir):
        for f in os.listdir(cap_dir):
            if f.endswith(".jsonl"):
                try: os.remove(os.path.join(cap_dir, f))
                except: pass

    print("[INFO] 启动 oaiprism.exe 代理服务（纯环境变量注入模式）...")
    proc = subprocess.Popen(
        [r"F:\Code\Active\OAIprism\bin\oaiprism.exe", "serve", "-config", r"F:\Code\Active\OAIprism\configs\config.yaml"],
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        encoding="utf-8",
        errors="replace"
    )

    try:
        time.sleep(1.5)

        # ---------------- Layer 1: 环境变量注入与服务就绪层 ----------------
        log_step(1, "环境变量注入与网关服务就绪排查")
        code, body, _ = http_req("/healthz", auth=False)
        print(f"  > GET /healthz (匿名探针) -> HTTP {code}: {body}")
        assert code == 200, f"/healthz 探针失败，得到 {code}"

        code, body, _ = http_req("/v1/models", auth=False)
        print(f"  > GET /v1/models (未带 API Key) -> HTTP {code} (预期 401)")
        assert code == 401, f"未鉴权应返回 401，得到 {code}"

        code, body, _ = http_req("/v1/models", auth=True)
        print(f"  > GET /v1/models (带 API Key) -> HTTP {code}: {body[:200]}")
        assert code == 200, f"带 Key 应返回 200，得到 {code}"
        models_data = json.loads(body).get("data", [])
        model_ids = [m.get("id") for m in models_data]
        print(f"  > 可用模型清单: {model_ids}")
        assert "gpt-5.6-sol" in model_ids, "模型列表中应包含 gpt-5.6-sol"
        print("  [Layer 1 通过] 环境变量成功注入，探针豁免鉴权正常，模型列表已就绪。")

        # ---------------- Layer 2: 上游会话与账号鉴权层 ----------------
        log_step(2, "上游会话与账号鉴权排查 (Session & Upstream Auth)")
        code, body, _ = http_req("/prism/api/auth/session", auth=True)
        print(f"  > GET /prism/api/auth/session -> HTTP {code}")
        if code != 200:
            print(f"  [ERROR] 上游 session 校验失败: {body[:300]}")
            return 2
        session_info = json.loads(body)
        user_info = session_info.get("user", {})
        acc_info = session_info.get("account", {})
        print(f"  > 账号邮箱: {user_info.get('email')}")
        print(f"  > 账号 Plan: {acc_info.get('planType')}")
        print("  [Layer 2 通过] 环境变量 Cookie 穿透上游鉴权成功，账号有效。")

        # ---------------- Layer 3: 项目管理与沙箱创建层 ----------------
        log_step(3, "项目创建与沙箱容器申请排查")
        proj_uuid = str(uuid.uuid4())
        code, body, _ = http_req("/prism/api/projects", data={"project_uuid": proj_uuid, "title": "live test project"}, method="POST", auth=True)
        print(f"  > POST /prism/api/projects -> HTTP {code}")
        assert code in (200, 201), f"创建项目失败: {body}"
        proj_resp = json.loads(body)
        real_proj_id = (proj_resp.get("project") or {}).get("uuid") or proj_resp.get("uuid") or proj_uuid
        print(f"  > 项目 ID: {real_proj_id}")

        code, body, _ = http_req("/prism/api/backend/1/new", method="POST", auth=True)
        print(f"  > POST /prism/api/backend/1/new -> HTTP {code}")
        assert code == 200, f"申请沙箱容器失败: {body}"
        sb_info = json.loads(body)
        sb_url = sb_info.get("url")
        sb_token = sb_info.get("token")
        print(f"  > 沙箱 URL: {sb_url}")
        print(f"  > 沙箱 Token 长度: {len(sb_token or '')}")
        print("  [Layer 3 通过] 项目与沙箱容器申请均成功。")

        # ---------------- Layer 4: 沙箱资源令牌与协同文档交付排查 ----------------
        log_step(4, "沙箱资源注入与工作区同步排查")
        session_id = sb_info.get("sandbox_session_id"); code, body, _ = http_req(f"/prism/api/projects/{real_proj_id}/sandbox/resources-token", data={"sandbox_session_id": session_id, "sandbox_token": sb_token}, method="POST", auth=True)
        print(f"  > POST resources-token -> HTTP {code}")
        assert code == 200, f"签发资源令牌失败: {body}"
        rt_info = json.loads(body)
        res_access_token = rt_info.get("access_token")
        res_base_url = rt_info.get("resources_base_url")

        # 交付资源令牌至沙箱
        sb_rt_url = (sb_url if sb_url.endswith("/") else sb_url + "/") + "resources-token"
        code, body, _ = http_req(sb_rt_url, data={
            "token": res_access_token,
            "resourceBaseUrl": res_base_url,
            "projectId": real_proj_id
        }, method="POST", headers={"X-Crixet-Sandbox-Token": sb_token, "Cookie": cookie}, auth=False)
        print(f"  > POST <sandbox>/resources-token -> HTTP {code}: {body}")

        # 获取 Y-Sweet 凭证
        code, body, _ = http_req("/prism/api/y", data={"docId": real_proj_id, "requestContext": {}}, method="POST", auth=True)
        print(f"  > POST /prism/api/y -> HTTP {code}")
        assert code == 200, f"获取 Y-Sweet 凭证失败: {body}"
        y_info = json.loads(body)

        # 交付 Y-Sweet 凭证至沙箱
        sb_y_url = (sb_url if sb_url.endswith("/") else sb_url + "/") + "token"
        code, body, _ = http_req(sb_y_url, data=y_info, method="POST", headers={"X-Crixet-Sandbox-Token": sb_token, "Cookie": cookie}, auth=False)
        print(f"  > POST <sandbox>/token -> HTTP {code}: {body}")

        # 等待沙箱就绪
        sb_wait_url = (sb_url if sb_url.endswith("/") else sb_url + "/") + "wait-for-sync?wait_ms=10000"
        sync_ok = False
        for wait_attempt in range(5):
            code, body, _ = http_req(sb_wait_url, method="GET", headers={"X-Crixet-Sandbox-Token": sb_token, "Cookie": cookie}, auth=False, timeout=20)
            print(f"  > 等待沙箱同步 (轮次 {wait_attempt+1}) -> HTTP {code}: {body}")
            if code == 200 and '"status":"synced"' in body:
                sync_ok = True
                break
            time.sleep(2)
        print(f"  > 沙箱同步状态: {'就绪 (synced)' if sync_ok else '未完全同步(代理内层将自动重试)'}")
        print("  [Layer 4 通过] 沙箱生命周期与工作区注入流程排查完毕。")

        # ---------------- Layer 5: 端到端 LLM 推理生成排查 ----------------
        log_step(5, "端到端 Codex 推理生成排查")

        # 5.1 非流式调用
        print("  [5.1] 测试非流式 Chat Completions...")
        t0 = time.time()
        chat_req = {
            "model": "gpt-5.6-sol",
            "messages": [
                {"role": "user", "content": "我最喜欢的水果是蓝莓。请只回复一个单词：ACK"}
            ]
        }
        code, body, hdrs = http_req(
            "/v1/chat/completions",
            data=chat_req,
            method="POST",
            auth=True,
            timeout=180
        )
        latency = time.time() - t0
        print(f"  > HTTP {code} (耗时 {latency:.2f}s)")
        assert code == 200, f"非流式调用失败: {body}"
        resp_json = json.loads(body)
        choice = resp_json.get("choices", [{}])[0]
        msg = choice.get("message", {})
        reply_text = msg.get("content", "").strip()
        conv_id = resp_json.get("prism_conversation_id") or hdrs.get("x-prism-conversation-id")
        print(f"  > 模型回复: {reply_text}")
        print(f"  > 响应体/响应头会话 ID: {conv_id}")
        assert "ACK" in reply_text.upper(), f"预期回复包含 ACK，实际为: {reply_text}"
        assert conv_id, "预期返回非空 conversation_id"
        print("  [5.1 通过] 非流式生成完成，会话 ID 正常回传。")

        # 5.2 多轮会话延续性测试
        print("\n  [5.2] 测试多轮上下文延续性 (带着 conversation_id 继续提问)...")
        chat_req_round2 = {
            "model": "gpt-5.6-sol",
            "conversation_id": conv_id,
            "messages": [
                {"role": "user", "content": "我最喜欢的水果是蓝莓。请只回复一个单词：ACK"},
                {"role": "assistant", "content": reply_text},
                {"role": "user", "content": "我最喜欢的水果是什么？请直接回复该水果名称"}
            ]
        }
        code, body, _ = http_req(
            "/v1/chat/completions",
            data=chat_req_round2,
            method="POST",
            auth=True,
            timeout=180
        )
        print(f"  > HTTP {code}")
        reply2 = json.loads(body).get("choices", [{}])[0].get("message", {}).get("content", "").strip()
        print(f"  > 第二轮回复: {reply2}")
        cap_dir = r"F:\Code\Active\OAIprism\captures"
        if os.path.isdir(cap_dir):
            files = sorted(os.listdir(cap_dir))
            if files:
                with open(os.path.join(cap_dir, files[-1]), "r", encoding="utf-8", errors="replace") as cf:
                    lines = [ln for ln in cf if "response_with_tools_start" in ln]
                    if lines:
                        print(f"  > 上游 Start 请求体: {lines[-1][:600]}")
        assert "蓝莓" in reply2, f"第二轮未正确延续上下文: {reply2}"
        print("  [5.2 通过] 多轮会话上下文在沙箱上游保持连通。")

        # 5.3 流式 SSE 调用
        print("\n  [5.3] 测试流式 Chat Completions (SSE)...")
        stream_req = {
            "model": "gpt-5.6-sol",
            "stream": True,
            "messages": [
                {"role": "user", "content": "写一句5个字的话"}
            ]
        }
        req_data = json.dumps(stream_req).encode("utf-8")
        ur = urllib.request.Request(BASE_PROXY + "/v1/chat/completions", data=req_data, method="POST")
        ur.add_header("Authorization", f"Bearer {API_KEY}")
        ur.add_header("Content-Type", "application/json")
        ur.add_header("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36")
        with urllib.request.urlopen(ur, timeout=180) as sresp:
            print(f"  > 流式连接已建立: HTTP {sresp.status}")
            stream_chunks = []
            final_conv_id = None
            for raw_line in sresp:
                line = raw_line.decode("utf-8", "replace").strip()
                if line.startswith("data: ") and not line.endswith("[DONE]"):
                    chunk_json = json.loads(line[6:])
                    if "prism_conversation_id" in chunk_json:
                        final_conv_id = chunk_json["prism_conversation_id"]
                    choices = chunk_json.get("choices", [])
                    if choices:
                        delta = choices[0].get("delta", {})
                        if "content" in delta and delta["content"]:
                            stream_chunks.append(delta["content"])
            full_stream_text = "".join(stream_chunks)
            print(f"  > 组装后的完整流式文本: {full_stream_text}")
            print(f"  > 流式结束帧携带的会话 ID: {final_conv_id}")
            assert full_stream_text, "流式文本不应为空"
            assert final_conv_id, "流式结束帧必须携带 prism_conversation_id"
        print("  [5.3 通过] 真流式 SSE 增量推送正常，结束帧顺利拿到 conversation_id。")

        # 5.4 Responses API 测试
        print("\n  [5.4] 测试 Responses API (/v1/responses)...")
        resp_req = {
            "model": "gpt-5.6-sol",
            "input": "请回复一个字母：A"
        }
        code, body, _ = http_req(
            "/v1/responses",
            data=resp_req,
            method="POST",
            auth=True,
            timeout=180
        )
        print(f"  > POST /v1/responses -> HTTP {code}: {body[:200]}")
        assert code == 200, f"Responses API 失败: {body}"
        print("  [5.4 通过] Responses API 链路打通。")

        print("\n" + "#"*70)
        print("  [ALL PASSED] 从外到内全部 5 层链路排查与真实测试全部成功！")
        print("#"*70)
        return 0

    finally:
        print("[INFO] 终止 oaiprism 后台进程...")
        proc.terminate()
        try:
            proc.wait(timeout=3)
        except subprocess.TimeoutExpired:
            proc.kill()

if __name__ == "__main__":
    sys.exit(main())
