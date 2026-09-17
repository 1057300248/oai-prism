#!/usr/bin/env python3
"""Prism 完整链路端到端验证。

流程（每一步都是实测出来的，不是猜的）：

  1. POST /api/projects                     建项目（需要 project_uuid + title）
  2. POST /api/backend/1/new                申请沙箱 → {url, token}
  3. GET  <url>/wait-for-sync?wait_ms=10000 等容器就绪（未就绪会直接断 TLS）
  4. POST /api/llm/response_with_tools_start 发起生成（metadata 里带沙箱）
  5. POST /api/llm/response_with_tools_status 轮询直到 completed
  6. 从 response.payload.output 里倒序取最后一条 assistant 消息

用法：
  python tools/verify_full_flow.py "你的问题" [reasoning_effort]

注意：用 -u 参数运行才能实时看到进度（Python 重定向时会缓冲输出）。
"""
import io
import json
import sys
import time
import uuid
import urllib.error
import urllib.request

COOKIE_FILE = r'F:\Code\Active\OAIprism\secrets\cookies.txt'
BASE = "https://prism.openai.com"
UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36")

HEADERS = {
    "accept": "application/json",
    "content-type": "application/json",
    "origin": BASE,
    "referer": BASE + "/",
    "user-agent": UA,
    "accept-language": "zh-CN,zh;q=0.9",
    "cookie": io.open(COOKIE_FILE, encoding="utf-8").read().strip(),
}


def call(path, payload=None, method="POST", timeout=180, extra=None, url=None):
    """发一个请求，返回 (status, body_text)。网络异常不抛出，返回 (0, 错误)。"""
    target = url or (BASE + path)
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(target, data=data, method=method)
    for k, v in HEADERS.items():
        req.add_header(k, v)
    for k, v in (extra or {}).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, r.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")
    except Exception as e:
        return 0, f"{type(e).__name__}: {e}"


def step(n, title):
    print(f"\n{'='*72}\n[{n}] {title}\n{'='*72}", flush=True)


def main():
    prompt = sys.argv[1] if len(sys.argv) > 1 else "Reply with exactly: PONG"
    effort = sys.argv[2] if len(sys.argv) > 2 else "low"
    t_all = time.time()

    # ---------- 1) 建项目 ----------
    step(1, "建项目 POST /api/projects")
    proj_uuid = str(uuid.uuid4())
    code, body = call("/api/projects",
                      {"project_uuid": proj_uuid, "title": "oaiprism verification"})
    print(f"    project_uuid = {proj_uuid}")
    print(f"    HTTP {code}")
    if code not in (200, 201):
        print(f"    响应: {body[:400]}")
        # 建项目失败不致命：会话本身不强依赖项目，继续往下走。
    else:
        try:
            d = json.loads(body)
            real = (d.get("project") or {}).get("uuid") or d.get("uuid") or d.get("project_uuid")
            if real:
                proj_uuid = real
                print(f"    上游返回的 uuid = {proj_uuid}")
            print(f"    完整响应: {json.dumps(d, ensure_ascii=False)[:300]}")
        except Exception:
            print(f"    响应: {body[:300]}")

    # ---------- 2) 申请沙箱 ----------
    step(2, "申请沙箱 POST /api/backend/1/new")
    code, body = call("/api/backend/1/new")
    print(f"    HTTP {code}")
    if code != 200:
        print(f"    失败: {body[:400]}")
        return 1
    sb = json.loads(body)
    sb_url, sb_token = sb.get("url", ""), sb.get("token", "")
    print(f"    url   = {sb_url}")
    print(f"    token = {sb_token[:20]}...（{len(sb_token)} 字符）")
    if not sb_url or not sb_token:
        print("    沙箱信息不完整")
        return 1

    # ---------- 3) 等沙箱就绪 ----------
    step(3, "等沙箱就绪 GET <sandbox>/wait-for-sync")
    base = sb_url if sb_url.endswith("/") else sb_url + "/"
    t0 = time.time()
    ready = False
    last = ""
    while time.time() - t0 < 150:
        code, body = call(None, method="GET", timeout=25, url=base + "wait-for-sync?wait_ms=10000",
                          extra={"X-Crixet-Sandbox-Token": sb_token})
        el = time.time() - t0
        last = f"HTTP {code} | {body[:120]}"
        print(f"    {el:6.1f}s  {last}", flush=True)
        if code == 200:
            ready = True
            print(f"    ✅ 沙箱就绪，耗时 {el:.1f}s")
            break
        if code == 0:
            # 沙箱没起来时会直接断 TLS（EOF），属正常，继续等。
            time.sleep(3)
            continue
        if code not in (408, 425, 429, 500, 502, 503, 504):
            print(f"    ⚠️ 非可重试状态 {code}，停止等待")
            break
        time.sleep(3)
    if not ready:
        print(f"    ⚠️ 150s 内未就绪（最后: {last}），仍尝试 start")

    # ---------- 4) start ----------
    step(4, "发起生成 POST /api/llm/response_with_tools_start")
    payload = {
        "input": [
            {"type": "message", "role": "system",
             "content": [{"type": "input_text",
                          "text": "You are a helpful assistant inside Prism."}]},
            {"type": "message", "role": "user",
             "content": [{"type": "input_text", "text": prompt}]},
        ],
        "metadata": {
            "model": "gpt-5.6-sol",
            "reasoning_effort": effort,
            "frontend_origin": BASE,
            "projectId": proj_uuid,
            "sandbox_url": sb_url,
            "sandbox_token": sb_token,
        },
    }

    env = None
    for attempt in range(1, 5):
        t0 = time.time()
        code, body = call("/api/llm/response_with_tools_start", payload, timeout=300)
        dur = time.time() - t0
        print(f"    第 {attempt} 次: HTTP {code} | {dur:.1f}s", flush=True)
        if code != 200:
            print(f"      {body[:300]}")
            break
        env = json.loads(body)
        r = env.get("response") or {}
        print(f"      status={env.get('status')} request_id={env.get('request_id')}")
        if r.get("status") != "error":
            break
        pl = r.get("payload") or {}
        msg = str(pl.get("message") or "")[:160]
        print(f"      ⚠️ {pl.get('reason')}: {msg}")
        dbg = pl.get("codexRequestDebug") or {}
        if dbg:
            print(f"      debug: sandbox_url={dbg.get('sandbox_url_resolved')} "
                  f"token_present={dbg.get('sandbox_token_present')} "
                  f"auth={dbg.get('backend_auth_token_present')}")
        if attempt >= 4:
            break
        # 上游明确说 "Please submit prompt again"，照做。
        time.sleep(8)
        # 每次重试重新等一次就绪
        call(None, method="GET", timeout=20, url=base + "wait-for-sync?wait_ms=8000",
             extra={"X-Crixet-Sandbox-Token": sb_token})

    if env is None:
        print("\n❌ 未能拿到任何响应")
        return 1

    # ---------- 5) 轮询 ----------
    rid = env.get("request_id")
    turn_state = env.get("turn_state")
    if env.get("status") == "started":
        step(5, "轮询 POST /api/llm/response_with_tools_status")
        if not rid or not turn_state:
            print("    ⚠️ start 返回 started 但缺少 request_id / turn_state")
            return 1
        deadline = time.time() + 300
        n = 0
        while time.time() < deadline:
            n += 1
            time.sleep(2)
            code, body = call("/api/llm/response_with_tools_status",
                              {"request_id": rid, "turn_state": turn_state}, timeout=60)
            if code != 200:
                print(f"    #{n} HTTP {code}: {body[:200]}", flush=True)
                if code in (400, 404):
                    break
                continue
            env = json.loads(body)
            st = env.get("status")
            if env.get("turn_state"):
                turn_state = env["turn_state"]
            print(f"    #{n} status={st}", flush=True)
            if st == "completed":
                print(f"    轮询 {n} 次，总耗时 {time.time()-t_all:.1f}s")
                break
            if st != "pending":
                print(f"    未知状态: {json.dumps(env, ensure_ascii=False)[:300]}")
                break

    # ---------- 6) 出结果 ----------
    step(6, "结果")
    resp = env.get("response") or {}
    if resp.get("status") == "error":
        pl = resp.get("payload") or {}
        print(f"❌ 上游返回错误")
        print(f"   reason  = {pl.get('reason')}")
        print(f"   message = {pl.get('message')}")
        dbg = pl.get("codexRequestDebug") or {}
        if dbg:
            print(f"   debug   = {json.dumps(dbg, ensure_ascii=False)[:600]}")
        return 1

    payload_out = resp.get("payload") or {}
    output = payload_out.get("output") or []
    text = ""
    for item in reversed(output):
        if isinstance(item, dict) and item.get("type") == "message" \
                and item.get("role") == "assistant":
            parts = [c.get("text") or "" for c in (item.get("content") or [])
                     if isinstance(c, dict) and c.get("type") == "output_text"]
            text = "".join(parts)
            if text:
                break

    print(f"✅ 成功！总耗时 {time.time()-t_all:.1f}s")
    print(f"   output 条目 = {len(output)}")
    for i, it in enumerate(output):
        if isinstance(it, dict):
            print(f"     [{i}] type={it.get('type')} role={it.get('role')}")
    print(f"   usage = {payload_out.get('usage')}")
    print("-" * 72)
    print("模型回答：")
    print(text if text else "(空)")
    print("-" * 72)
    return 0


if __name__ == "__main__":
    sys.exit(main())
