"""测 B'：access_token 以 cookie 形态跑 POST /api/projects + 完整对话闭环。
对照旧 cookies.txt。"""
import io
import json
import os
import time
import urllib.request
import urllib.error
import uuid

BASE = "https://prism.openai.com"
UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36")

NEW_AT = ""
if os.path.exists(r"C:\Users\13080\AppData\Local\Temp\oauth_new_token.txt"):
    NEW_AT = io.open(r"C:\Users\13080\AppData\Local\Temp\oauth_new_token.txt",
                     encoding="utf-8").read().strip()
if not NEW_AT:
    NEW_AT = json.loads(io.open(r"C:\Users\13080\AppData\Local\Temp\oauth_creds.json",
                                encoding="utf-8").read())["access_token"]
with io.open(r"F:\Code\Active\OAIprism\secrets\cookies.txt", encoding="utf-8") as f:
    OLD = f.read().strip()


def post(path, payload, cookie, timeout=120):
    req = urllib.request.Request(BASE + path,
                                 data=json.dumps(payload).encode(), method="POST")
    for k, v in {"accept": "application/json", "content-type": "application/json",
                 "origin": BASE, "referer": BASE + "/", "user-agent": UA,
                 "cookie": cookie}.items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, r.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")[:250]
    except Exception as e:
        return 0, "%s: %s" % (type(e).__name__, e)


def probe(tag, cookie):
    print("== %s ==" % tag)
    pid = str(uuid.uuid4())
    code, body = post("/api/projects", {"project_uuid": pid, "title": "oaiprism oauth probe"}, cookie)
    print("  建项目: HTTP %s %s" % (code, body[:90]))
    if code != 200:
        return
    code, body = post("/api/backend/1/new", None, cookie)
    print("  申请沙箱: HTTP %s %s" % (code, body[:90]))
    if code != 200:
        return
    sb = json.loads(body)
    sb_url = sb["url"] if sb["url"].endswith("/") else sb["url"] + "/"
    # 跳过四步注入，直接 start —— 会不会返回 sandbox_reconnecting？
    # （判断 OAuth token 路径下沙箱注入是否必需；先看注入本身能不能做）
    code, body = post("/api/projects/%s/sandbox/resources-token" % pid,
                      {"sandbox_session_id": sb.get("sandbox_session_id"),
                       "sandbox_token": sb["token"]}, cookie)
    print("  签发资源令牌: HTTP %s %s" % (code, body[:90]))
    if code == 200:
        rt = json.loads(body)
        base = (rt.get("resources_base_url") or "").rstrip("/") + "/"
        req = urllib.request.Request(sb_url + "resources-token",
                                     data=json.dumps({"token": rt["access_token"],
                                                      "resourceBaseUrl": base,
                                                      "projectId": pid}).encode(), method="POST")
        for k, v in {"accept": "application/json", "content-type": "application/json",
                     "origin": BASE, "referer": BASE + "/", "user-agent": UA,
                     "cookie": cookie, "X-Crixet-Sandbox-Token": sb["token"]}.items():
            req.add_header(k, v)
        try:
            with urllib.request.urlopen(req, timeout=60) as r:
                print("  沙箱注入: HTTP", r.status)
        except urllib.error.HTTPError as e:
            print("  沙箱注入: HTTP", e.code, "(body 空=特征性失败)")
        code, body = post("/api/y", {"docId": pid, "requestContext": {"source": "bridge-probe"}}, cookie)
        print("  Y 凭证: HTTP %s %s" % (code, body[:80]))
        if code == 200:
            y = json.loads(body)
            data = {"token": json.dumps(y)}  # v6 前端直接把整个对象放 token 字段
            req = urllib.request.Request(sb_url + "token",
                                         data=json.dumps({"token": y}).encode(), method="POST")
            for k, v in {"accept": "application/json", "content-type": "application/json",
                         "origin": BASE, "referer": BASE + "/", "user-agent": UA,
                         "cookie": cookie, "X-Crixet-Sandbox-Token": sb["token"]}.items():
                req.add_header(k, v)
            try:
                with urllib.request.urlopen(req, timeout=40) as r:
                    print("  沙箱 token 注入: HTTP", r.status)
            except urllib.error.HTTPError as e:
                print("  沙箱 token 注入: HTTP", e.code)
            req = urllib.request.Request(sb_url + "wait-for-sync?wait_ms=10000")
            for k, v in {"accept": "application/json", "user-agent": UA,
                         "cookie": cookie, "X-Crixet-Sandbox-Token": sb["token"]}.items():
                req.add_header(k, v)
            try:
                with urllib.request.urlopen(req, timeout=30) as r:
                    print("  wait-for-sync: HTTP", r.status, r.read().decode()[:80])
            except urllib.error.HTTPError as e:
                print("  wait-for-sync: HTTP", e.code)
        # 对话闭环
        code, resp = post("/api/llm/response_with_tools_start", {
            "input": [{"type": "message", "role": "user",
                       "content": [{"type": "input_text", "text": "say PONG"}]}],
            "metadata": {"model": "gpt-5.6-sol", "reasoning_effort": "low", "projectId": pid,
                         "frontend_origin": BASE, "sandbox_url": sb_url,
                         "sandbox_token": sb["token"]},
        }, cookie)
        d = json.loads(resp) if code == 200 else {}
        print("  start: HTTP %s status=%s" % (code, d.get("status")))
        if d.get("status") == "started":
            rid, ts = d.get("request_id"), d.get("turn_state")
            for i in range(30):
                time.sleep(2)
                code, env = post("/api/llm/response_with_tools_status",
                                 {"request_id": rid, "turn_state": ts}, cookie)
                if code != 200:
                    break
                env = json.loads(env)
                ts = env.get("turn_state") or ts
                if env.get("status") == "completed":
                    break
            robj = env.get("response") or {}
            text = "".join(c.get("text", "") for it in ((robj.get("payload") or {}).get("output") or [])
                           for c in (it.get("content") or []))
            print("  对话: %s | 回复: %r" % (robj.get("status"), text[:60]))
    print()


probe("A. 纯 OAuth access_token（无任何旧 cookie）",
      "prism_oai_access_token=" + NEW_AT)
probe("B. 对照：旧 cookies.txt 全套", OLD)
