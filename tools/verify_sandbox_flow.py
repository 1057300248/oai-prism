#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""完整链路验证：按真实前端契约跑通「沙箱资源注入 → 生成」。

这个脚本的意义是**把前端 bundle 里读出来的契约在真实上游上验证一遍**。
它不依赖任何 Yjs 实现 —— 因为沙箱拿到资源令牌后是它自己去同步文档的。

契约来源（全部来自 prism.openai.com 自己的前端 bundle）：

    POST /api/projects                          {project_uuid, title}
    POST /api/backend/1/new                     → {url, token, sandbox_id, sandbox_session_id}
    POST /api/projects/{id}/sandbox/resources-token
                                                {sandbox_session_id, sandbox_token}
                                                → {access_token, resources_base_url, expires_at}
    POST <sandbox_url>resources-token           {token, resourceBaseUrl, projectId}
    GET  <sandbox_url>wait-for-sync?wait_ms=10000
                                                Header: X-Crixet-Sandbox-Token
                                                → {status, readinessCapabilities, tokens:{...}}
    POST /api/llm/response_with_tools_start     metadata 里带 sandbox_url / sandbox_token
    POST /api/llm/response_with_tools_status    {request_id, turn_state}

用法：
    python tools/verify_sandbox_flow.py "你的问题" [reasoning_effort]
"""

import io
import json
import os
import sys
import time
import urllib.error
import urllib.request

BASE = "https://prism.openai.com"
UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36")

HERE = os.path.dirname(os.path.abspath(__file__))
COOKIE_FILE = os.path.join(os.path.dirname(HERE), "secrets", "cookies.txt")

PROMPT = sys.argv[1] if len(sys.argv) > 1 else "Reply with exactly: PONG"
EFFORT = sys.argv[2] if len(sys.argv) > 2 else "low"


def log(*a):
    print(*a, flush=True)


def cookie_header():
    with io.open(COOKIE_FILE, encoding="utf-8") as f:
        return f.read().strip()


def api(path, payload=None, method="POST", timeout=120, extra=None, root=BASE):
    """发一个「Prism 站点」请求（自动带浏览器头与 Cookie）。"""
    url = path if path.startswith("http") else root + path
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("accept", "application/json")
    req.add_header("content-type", "application/json")
    req.add_header("origin", BASE)
    req.add_header("referer", BASE + "/")
    req.add_header("user-agent", UA)
    req.add_header("cookie", cookie_header())
    for k, v in (extra or {}).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, resp.read().decode("utf-8", "replace"), dict(resp.headers)
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace"), dict(e.headers)
    except Exception as e:  # 连接被断、超时等
        return 0, "%s: %s" % (type(e).__name__, e), {}


def sandbox_call(sandbox_url, path, payload=None, method="POST", token=None, timeout=40):
    """发一个「沙箱代理」请求。

    注意：沙箱代理走的是同一个域名下的 /s/sandboxes/proxy/ 前缀。

    **认证是双重的**，两样都要带：
      1. Cookie（会话）—— 前端 fetchSandboxWithRetry 默认 credentials:"same-origin"，
         实测只带 X-Crixet-Sandbox-Token 会直接 401 且响应体为空；
      2. X-Crixet-Sandbox-Token —— 沙箱专属令牌。
    """
    url = sandbox_url + path
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("accept", "application/json")
    req.add_header("content-type", "application/json")
    req.add_header("origin", BASE)
    req.add_header("referer", BASE + "/")
    req.add_header("user-agent", UA)
    req.add_header("cookie", cookie_header())
    req.add_header("X-Crixet-Sandbox-Token", token or "")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")
    except Exception as e:
        return 0, "%s: %s" % (type(e).__name__, e)


def ensure_trailing_slash(u):
    return u if u.endswith("/") else u + "/"


def resources_base_from_sandbox_url(u):
    """从沙箱代理 URL 推导资源基地址。

    对应前端 bundle 里的 getSandboxResourcesBaseUrlFromSandboxUrl：
        /s/sandboxes/proxy...  ->  /s/sandbox-resources
        /sandboxes/proxy...    ->  /sandbox-resources
    """
    for src, dst in (("/s/sandboxes/proxy", "/s/sandbox-resources"),
                     ("/sandboxes/proxy", "/sandbox-resources")):
        if src in u:
            return BASE + dst + "/"
    return ""


def main():
    log("=" * 78)
    log("Prism 完整链路验证（沙箱资源注入路径，无需 Yjs）")
    log("=" * 78)

    # ---------- 0) 新建项目 ----------
    # 用 RFC4122 v4 风格的 uuid：上游把它当幂等键，重复用会拿回同一个项目。
    import uuid as _uuid
    proj_uuid = str(_uuid.uuid4())
    log("\n[0] 新建项目 POST /api/projects")
    code, body, _ = api("/api/projects", {"project_uuid": proj_uuid, "title": "oaiprism verify"})
    log("    HTTP %s | %s" % (code, body[:220]))
    if code == 200:
        try:
            d = json.loads(body)
            proj = d.get("project") or d
            proj_uuid = proj.get("uuid") or proj.get("project_uuid") or proj_uuid
        except Exception:
            pass
    log("    项目 = %s" % proj_uuid)

    # ---------- 1) 申请沙箱 ----------
    log("\n[1] 申请沙箱 POST /api/backend/1/new")
    code, body, _ = api("/api/backend/1/new")
    if code != 200:
        log("    失败 HTTP %s: %s" % (code, body[:400]))
        return 1
    sb = json.loads(body)
    log("    原始响应字段: %s" % sorted(sb.keys()))
    sandbox_url = ensure_trailing_slash(sb["url"])
    sandbox_token = sb["token"]
    sandbox_id = sb.get("sandbox_id")
    session_id = sb.get("sandbox_session_id")
    log("    url              = %s" % sandbox_url)
    log("    token            = %s...(%d 字符)" % (sandbox_token[:20], len(sandbox_token)))
    log("    sandbox_id       = %s" % sandbox_id)
    log("    sandbox_session_id = %s   <-- 下一步必需，之前漏了这个" % session_id)

    # ---------- 2) 后端签发资源令牌 ----------
    log("\n[2] 后端签发资源令牌 POST /api/projects/{id}/sandbox/resources-token")
    code, body, _ = api(
        "/api/projects/%s/sandbox/resources-token" % proj_uuid,
        {"sandbox_session_id": session_id, "sandbox_token": sandbox_token},
    )
    log("    HTTP %s | %s" % (code, body[:400]))
    if code != 200:
        log("    !! 这步失败就没有资源令牌，沙箱永远不会就绪")
        return 1
    rt = json.loads(body)
    access_token = rt.get("access_token")
    if not access_token:
        log("    !! 响应里没有 access_token: %s" % sorted(rt.keys()))
        return 1
    log("    access_token      = %s...(%d 字符)" % (access_token[:20], len(access_token)))
    log("    resources_base_url= %s" % rt.get("resources_base_url"))
    log("    expires_at        = %s" % rt.get("expires_at"))
    log("    max_age_seconds   = %s" % rt.get("max_age_seconds"))

    base_url = rt.get("resources_base_url") or resources_base_from_sandbox_url(sandbox_url)
    base_url = ensure_trailing_slash(base_url)
    log("    实际使用 base_url = %s" % base_url)

    # ---------- 3) 把资源令牌交给沙箱 ----------
    log("\n[3] 注入资源令牌 POST <sandbox>/resources-token")
    code, body = sandbox_call(
        sandbox_url, "resources-token",
        {"token": access_token, "resourceBaseUrl": base_url, "projectId": proj_uuid},
        token=sandbox_token, timeout=60,
    )
    log("    HTTP %s | %s" % (code, body[:300]))
    if code not in (200, 204):
        log("    !! 注入失败")
        return 1
    log("    ✓ 已注入")

    # ---------- 3.5) 取 Y-Sweet 令牌并交给沙箱 ----------
    # 这一步才是让 hasCurrentYSweetToken 变 true 的关键。
    # 沙箱拿到令牌后会**自己**去连 Y-Sweet WebSocket 同步文档 ——
    # 所以我们不需要在客户端侧实现 Yjs。
    log()
    log("[3.5] 取 Y-Sweet 令牌 POST /api/y")
    req_series = "oaiprism-%d" % int(time.time())
    code, body, _ = api("/api/y", {
        "docId": proj_uuid,
        "requestContext": {
            "source": "initial-bootstrap",
            "sandboxUrl": sandbox_url,
            "sandboxSessionId": session_id,
            "requestSeriesId": req_series,
            "maxAttempts": 5,
        },
    }, timeout=60)
    log("    HTTP %s | %s" % (code, body[:300]))
    if code != 200:
        log("    !! 拿不到 Y-Sweet 令牌")
        return 1
    ytk = json.loads(body)
    log("    令牌字段: %s" % sorted(ytk.keys()))
    for k in ("docId", "url", "baseUrl", "authorization"):
        if k in ytk:
            log("      %-14s = %s" % (k, ytk[k]))
    if "token" in ytk:
        log("      %-14s = %s...(%d 字符)" % ("token", str(ytk["token"])[:20], len(str(ytk["token"]))))

    log()
    log("[3.6] 把 Y-Sweet 令牌交给沙箱 POST <sandbox>/token")
    code, body = sandbox_call(sandbox_url, "token", ytk, token=sandbox_token, timeout=30)
    log("    HTTP %s | %s" % (code, body[:300]))
    if code in (200, 204):
        log("    ✓ 已交付")
    else:
        log("    !! 交付失败（404 表示这个沙箱不支持该端点）")

    # ---------- 4) 等沙箱就绪 ----------
    log("\n[4] 等沙箱就绪 GET <sandbox>/wait-for-sync?wait_ms=10000")
    ready = False
    t_start = time.time()
    while time.time() - t_start < 90:
        code, body = sandbox_call(
            sandbox_url, "wait-for-sync?wait_ms=10000", method="GET",
            token=sandbox_token, timeout=25,
        )
        el = time.time() - t_start
        if code == 0:
            log("    %.0fs  连接被断（沙箱未起）: %s" % (el, body[:80]))
            time.sleep(3)
            continue
        if code in (404, 501):
            # 前端把 404/501 视为「不需要同步」= 通过。
            log("    %.0fs  HTTP %s → 视为无需同步，通过" % (el, code))
            ready = True
            break
        if code != 200:
            log("    %.0fs  HTTP %s | %s" % (el, code, body[:150]))
            if code not in (408, 425, 429, 500, 502, 503, 504):
                break
            time.sleep(2)
            continue
        try:
            d = json.loads(body)
        except Exception:
            log("    %.0fs  非 JSON: %s" % (el, body[:120]))
            time.sleep(2)
            continue
        t = d.get("tokens") or {}
        log("    %.0fs  status=%-8s caps=%s" % (
            el, d.get("status"), d.get("readinessCapabilities")))
        log("            ySweetToken=%-5s syncedProvider=%-5s projId=%-5s credSrc=%s" % (
            t.get("hasCurrentYSweetToken"), t.get("hasSyncedYSweetProvider"),
            t.get("hasResourceProjectId"), t.get("fileCredentialSource")))
        caps = d.get("readinessCapabilities")
        needs = isinstance(caps, list) and "current_y_sweet_provider" in caps
        if d.get("status") == "synced":
            if needs:
                if t.get("hasCurrentYSweetToken") and t.get("hasSyncedYSweetProvider"):
                    ready = True
                    break
            else:
                if t.get("hasCurrentYSweetToken") or t.get("hasSyncedYSweetToken", True):
                    ready = True
                    break
        if d.get("status") == "failed":
            log("    !! 同步失败")
            break
        time.sleep(1)

    log("\n    沙箱就绪 = %s" % ("是 ✓" if ready else "否（仍继续尝试 start，看上游怎么说）"))

    # ---------- 5) start ----------
    log("\n[5] 发起生成 POST /api/llm/response_with_tools_start")
    payload = {
        "input": [
            {"type": "message", "role": "system",
             "content": [{"type": "input_text",
                          "text": "You are the AI assistant inside Prism, an online LaTeX editor."}]},
            {"type": "message", "role": "user",
             "content": [{"type": "input_text", "text": PROMPT}]},
        ],
        "metadata": {
            "model": "gpt-5.6-sol",
            "reasoning_effort": EFFORT,
            "frontend_origin": BASE,
            "projectId": proj_uuid,
            "sandbox_url": sb["url"],
            "sandbox_token": sandbox_token,
        },
    }
    env = None
    t0 = time.time()
    for attempt in (1, 2, 3):
        code, body, _ = api("/api/llm/response_with_tools_start", payload, timeout=200)
        log("    第 %d 次: HTTP %s | %.1fs" % (attempt, code, time.time() - t0))
        if code != 200:
            log("       %s" % body[:400])
            break
        env = json.loads(body)
        r = env.get("response") or {}
        dbg = env.get("codexRequestDebug") or (r.get("payload") or {}).get("codexRequestDebug")
        if dbg:
            log("       debug: sandbox_url_resolved=%s token_present=%s" % (
                dbg.get("sandbox_url_resolved"), dbg.get("sandbox_token_present")))
        if r.get("status") == "error":
            pl = r.get("payload") or {}
            log("       %s: %s" % (pl.get("reason"), str(pl.get("message"))[:200]))
            if "submit prompt again" in str(pl.get("message", "")):
                time.sleep(5)
                continue
        break

    if env is None:
        return 1

    # ---------- 6) 若有 request_id，轮询 ----------
    request_id = env.get("request_id")
    turn_state = env.get("turn_state")
    conv = env.get("conversation_id")
    log("\n[6] 结果 request_id=%s status=%s" % (request_id, env.get("status")))

    if (env.get("response") or {}).get("status") == "success":
        show_answer(env["response"])
        return 0

    if not request_id:
        log("    没有 request_id，无法继续轮询")
        return 1

    log("    开始轮询 status（最多 180 秒）")
    t0 = time.time()
    while time.time() - t0 < 180:
        code, body, _ = api("/api/llm/response_with_tools_status",
                            {"request_id": request_id, "turn_state": turn_state}, timeout=60)
        if code != 200:
            log("    HTTP %s | %s" % (code, body[:200]))
            time.sleep(2)
            continue
        st = json.loads(body)
        el = time.time() - t0
        if st.get("turn_state") is not None:
            turn_state = st["turn_state"]
        r = st.get("response") or {}
        log("    %.0fs  status=%s resp_status=%s" % (el, st.get("status"), r.get("status")))
        if r.get("status") == "success":
            log("\n" + "=" * 78)
            log("✅ 生成成功")
            log("=" * 78)
            show_answer(r)
            return 0
        if r.get("status") == "error":
            pl = r.get("payload") or {}
            log("    !! %s: %s" % (pl.get("reason"), str(pl.get("message"))[:300]))
            return 1
        time.sleep(1)

    log("    轮询超时")
    return 1


def show_answer(response):
    pl = response.get("payload") or {}
    out = pl.get("output") or []
    log("output 条目数 = %d，类型 = %s" % (len(out), [i.get("type") for i in out if isinstance(i, dict)]))
    text = ""
    reasoning = ""
    for it in reversed(out):
        if not isinstance(it, dict):
            continue
        if it.get("type") == "message" and it.get("role") == "assistant" and not text:
            text = "".join(c.get("text") or "" for c in (it.get("content") or [])
                           if isinstance(c, dict) and c.get("type") == "output_text")
        if it.get("type") == "reasoning" and not reasoning:
            reasoning = "".join(c.get("text") or "" for c in (it.get("summary") or [])
                                if isinstance(c, dict))
    if reasoning:
        log("\n--- 思维链 ---\n%s" % reasoning[:500])
    log("\n--- 回答 ---\n%s" % (text if text else "(空)"))
    u = pl.get("usage")
    if u:
        log("\n--- 用量 ---\n%s" % json.dumps(u, ensure_ascii=False))


if __name__ == "__main__":
    sys.exit(main())
