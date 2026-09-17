#!/usr/bin/env python3
"""完整链路验证：申请沙箱 → start → 轮询 → 拿到答案。

这是对协议的最终判定：如果这里能拿到真实回答，说明整条链路的
每一个环节（认证 / 沙箱申请 / start 体 / turn_state 轮询 / 答案抽取）
都是对的。
"""
import io
import json
import sys
import time
import urllib.request
import urllib.error

COOKIES = io.open(r'F:\Code\Active\OAIprism\secrets\cookies.txt', encoding='utf-8').read().strip()
BASE = "https://prism.openai.com"
UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36")

HEADERS = {
    "accept": "application/json",
    "content-type": "application/json",
    "origin": BASE,
    "referer": BASE + "/",
    "user-agent": UA,
    "cookie": COOKIES,
    "accept-language": "zh-CN,zh;q=0.9",
}


def call(path, payload=None, method="POST", timeout=120, extra=None):
    url = BASE + path
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    for k, v in HEADERS.items():
        req.add_header(k, v)
    for k, v in (extra or {}).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            raw = r.read().decode("utf-8", "replace")
            return r.status, raw
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")


def main():
    prompt = sys.argv[1] if len(sys.argv) > 1 else "Reply with exactly: PONG"
    effort = sys.argv[2] if len(sys.argv) > 2 else "low"

    # ---- 1) 申请沙箱 ----
    print("[1] 申请沙箱 POST /api/backend/1/new ...")
    code, body = call("/api/backend/1/new")
    if code != 200:
        print(f"    失败 HTTP {code}: {body[:300]}")
        return 1
    sb = json.loads(body)
    sandbox_url = sb.get("url")
    sandbox_token = sb.get("token")
    print(f"    ✅ url   = {sandbox_url}")
    print(f"    ✅ token = {sandbox_token[:24]}...({len(sandbox_token)} 字符)")

    # ---- 1.5) 等沙箱就绪 ----
    #
    # 这一步是必需的：/api/backend/1/new 只是"分配"了一个沙箱，
    # 容器真正可用还需要时间。跳过它直接 start 会得到
    # "Error while processing conversation (504 Gateway Timeout)"——
    # 因为请求打到的是一个还没起来的沙箱。
    #
    # 真实前端也是这么做的（bundle 里的 lr() 函数）：
    #   await fetchSandboxWithRetry(`${url}wait-for-sync?wait_ms=10000`,
    #        {headers: {"X-Crixet-Sandbox-Token": token}})
    # 它对整个等待过程设了 75 秒上限。
    print("\n[1.5] 等沙箱就绪 GET <sandbox>/wait-for-sync ...")
    base = sandbox_url if sandbox_url.endswith("/") else sandbox_url + "/"
    t_ready = time.time()
    ready = False
    while time.time() - t_ready < 60:
        elapsed = time.time() - t_ready
        try:
            code, body = call(base + "wait-for-sync?wait_ms=10000", None, method="GET",
                              timeout=30, extra={"X-Crixet-Sandbox-Token": sandbox_token})
            print(f"    HTTP {code} | {elapsed:.0f}s | {body[:150]}")
            if code == 200:
                ready = True
                print(f"    ✅ 沙箱就绪，耗时 {elapsed:.1f}s")
                break
            if code not in (408, 425, 429, 500, 502, 503, 504):
                break
        except Exception as e:
            # 沙箱没起来时这个端点会直接断 TLS 连接（SSLEOFError）。
            # 这不是错误，只是"还没好"——前端也是靠重试熬过去的。
            print(f"    连接被断（沙箱未就绪）| {elapsed:.0f}s | {type(e).__name__}")
        time.sleep(3)
    if not ready:
        print("    ⚠️ 沙箱未在 90 秒内就绪，仍继续尝试 start")

    # ---- 2) start ----
    print("\n[2] 发起生成 POST /api/llm/response_with_tools_start ...")
    payload = {
        "input": [
            {"type": "message", "role": "system",
             "content": [{"type": "input_text", "text": "You are a helpful assistant."}]},
            {"type": "message", "role": "user",
             "content": [{"type": "input_text", "text": prompt}]},
        ],
        "metadata": {
            "model": "gpt-5.6-sol",
            "reasoning_effort": effort,
            "frontend_origin": BASE,
            # 关键：这两个是前端在 metadata 里用的名字（snake_case）
            "sandbox_url": sandbox_url,
            "sandbox_token": sandbox_token,
        },
    }
    t0 = time.time()
    env = None
    for attempt in range(1, 4):
        code, body = call("/api/llm/response_with_tools_start", payload, timeout=180)
        print(f"    第 {attempt} 次: HTTP {code} | {time.time()-t0:.1f}s")
        if code != 200:
            print(f"      失败: {body[:400]}")
            break
        env = json.loads(body)
        # 上游在沙箱没就绪时会回 completed + error，
        # 并且明确说 "Please submit prompt again" —— 所以照做。
        r = (env.get("response") or {})
        if r.get("status") == "error":
            pl = r.get("payload") or {}
            print(f"      {pl.get('reason')}: {str(pl.get('message'))[:150]}")
            if "submit prompt again" in str(pl.get("message", "")) or                pl.get("reason") in ("sandbox_reconnecting", "unknown"):
                time.sleep(5)
                continue
        break
    if env is None:
        return 1
    rid = env.get("request_id")
    conv = env.get("conversation_id")
    turn_state = env.get("turn_state")
    status = env.get("status")
    print(f"    status        = {status}")
    print(f"    request_id    = {rid}")
    print(f"    conversation  = {conv}")
    print(f"    turn_state    = {'有' if turn_state else '无'}")

    # start 直接终态的情况
    if status != "started":
        return report(env)

    # ---- 3) 轮询 ----
    print("\n[3] 轮询 POST /api/llm/response_with_tools_status ...")
    deadline = time.time() + 180
    n = 0
    while time.time() < deadline:
        n += 1
        time.sleep(2)
        code, body = call("/api/llm/response_with_tools_status",
                          {"request_id": rid, "turn_state": turn_state}, timeout=60)
        if code != 200:
            print(f"    #{n} HTTP {code}: {body[:250]}")
            return 1
        env = json.loads(body)
        status = env.get("status")
        new_ts = env.get("turn_state")
        if new_ts:
            turn_state = new_ts
        print(f"    #{n} status={status}")
        if status == "completed":
            print(f"    轮询次数 = {n}，耗时 {time.time()-t0:.1f}s")
            return report(env)
        if status != "pending":
            print(f"    未知状态: {json.dumps(env)[:400]}")
            return 1
    print("    超时")
    return 1


def report(env):
    resp = env.get("response") or {}
    print("\n" + "=" * 70)
    if resp.get("status") == "error":
        pl = resp.get("payload") or {}
        print("❌ 上游返回错误")
        print(f"   reason  = {pl.get('reason')}")
        print(f"   message = {pl.get('message')}")
        return 1

    payload = resp.get("payload") or {}
    output = payload.get("output") or []
    text = ""
    for item in reversed(output):
        if isinstance(item, dict) and item.get("type") == "message" \
                and item.get("role") == "assistant":
            parts = []
            for c in item.get("content") or []:
                if isinstance(c, dict) and c.get("type") == "output_text":
                    parts.append(c.get("text") or "")
            text = "".join(parts)
            if text:
                break

    print("✅ 成功拿到回答")
    print(f"   output 条目数 = {len(output)}")
    for i, it in enumerate(output):
        t = it.get("type") if isinstance(it, dict) else "?"
        print(f"     [{i}] type={t}")
    print(f"   usage = {payload.get('usage')}")
    print("-" * 70)
    print("回答内容：")
    print(text if text else "(空)")
    print("=" * 70)
    return 0


if __name__ == "__main__":
    sys.exit(main())
