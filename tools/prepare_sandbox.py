#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""为「手工配置型」代理准备一个已完成同步的沙箱。

用途：对照 PrismOpenAIProxy 这类把 PRISM_SANDBOX_URL/TOKEN 当人工前置条件的实现 ——
手工把四步注入做完，再把它需要的三个环境变量打印出来。

它证明的是：这类实现**协议层没问题**，缺的是沙箱链路的**自动化**。

输出可直接用于：
    PRISM_PROJECT_ID=<uuid> PRISM_SANDBOX_URL=<url> PRISM_SANDBOX_TOKEN=<token>
"""

import io
import json
import os
import sys
import time
import urllib.error
import urllib.request
import uuid

BASE = "https://prism.openai.com"
UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36")

HERE = os.path.dirname(os.path.abspath(__file__))
COOKIE_FILE = os.path.join(os.path.dirname(HERE), "secrets", "cookies.txt")


def log(*a):
    print(*a, flush=True)


def cookie():
    with io.open(COOKIE_FILE, encoding="utf-8") as f:
        return f.read().strip()


def api(path, payload=None, method="POST", timeout=120, root=BASE):
    url = path if path.startswith("http") else root + path
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    for k, v in {"accept": "application/json", "content-type": "application/json",
                 "origin": BASE, "referer": BASE + "/", "user-agent": UA,
                 "cookie": cookie()}.items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, r.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")
    except Exception as e:
        return 0, "%s: %s" % (type(e).__name__, e)


def sandbox_call(sb_url, path, payload=None, method="POST", token="", timeout=40):
    """沙箱代理请求 —— 注意认证是双重的：Cookie + X-Crixet-Sandbox-Token。"""
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(sb_url + path, data=data, method=method)
    for k, v in {"accept": "application/json", "content-type": "application/json",
                 "origin": BASE, "referer": BASE + "/", "user-agent": UA,
                 "cookie": cookie(), "X-Crixet-Sandbox-Token": token}.items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, r.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")
    except Exception as e:
        return 0, "%s: %s" % (type(e).__name__, e)


def main():
    project = sys.argv[1] if len(sys.argv) > 1 else str(uuid.uuid4())
    log("[0] 项目 = %s" % project)

    code, body = api("/api/projects", {"project_uuid": project, "title": "oaiprism sandbox prep"})
    if code != 200:
        log("    建项目失败 HTTP %s: %s" % (code, body[:200]))
        return 1
    log("    建项目 OK")

    code, body = api("/api/backend/1/new")
    if code != 200:
        log("[1] 申请沙箱失败 HTTP %s: %s" % (code, body[:200]))
        return 1
    sb = json.loads(body)
    sb_url = sb["url"] if sb["url"].endswith("/") else sb["url"] + "/"
    sb_token = sb["token"]
    log("[1] 沙箱 = %s" % sb_url)

    code, body = api("/api/projects/%s/sandbox/resources-token" % project,
                     {"sandbox_session_id": sb.get("sandbox_session_id"), "sandbox_token": sb_token})
    if code != 200:
        log("[2] 签发资源令牌失败 HTTP %s: %s" % (code, body[:200]))
        return 1
    rt = json.loads(body)
    base = (rt.get("resources_base_url") or "").rstrip("/") + "/"
    log("[2] 资源令牌 OK, base = %s" % base)

    code, body = sandbox_call(sb_url, "resources-token",
                              {"token": rt["access_token"], "resourceBaseUrl": base,
                               "projectId": project}, token=sb_token, timeout=60)
    log("[3] 注入资源令牌 HTTP %s %s" % (code, body[:120]))
    if code not in (200, 204):
        return 1

    code, body = api("/api/y", {"docId": project, "requestContext": {
        "source": "initial-bootstrap", "requestSeriesId": "prep-" + project, "maxAttempts": 5}})
    if code != 200:
        log("[4] 取 Y-Sweet 凭证失败 HTTP %s: %s" % (code, body[:200]))
        return 1
    ytk = json.loads(body)
    log("[4] Y-Sweet 凭证 OK (%d 字符)" % len(ytk.get("token", "")))

    code, body = sandbox_call(sb_url, "token", ytk, token=sb_token, timeout=30)
    log("[5] 交付凭证 HTTP %s %s" % (code, body[:120]))

    t0 = time.time()
    ready = False
    while time.time() - t0 < 90:
        code, body = sandbox_call(sb_url, "wait-for-sync?wait_ms=10000", method="GET",
                                  token=sb_token, timeout=25)
        if code in (404, 501):
            ready = True
            break
        if code == 0:
            time.sleep(2)
            continue
        if code == 200:
            d = json.loads(body)
            t = d.get("tokens") or {}
            if d.get("status") == "synced" and t.get("hasSyncedYSweetProvider"):
                ready = True
                break
            if d.get("status") == "failed":
                break
        time.sleep(1)
    log("[6] 沙箱就绪 = %s (%.1fs)" % (ready, time.time() - t0))
    if not ready:
        return 1

    log("")
    log("=" * 70)
    log("把下面三个环境变量交给「手工配置型」代理即可让它工作：")
    log("=" * 70)
    log("PRISM_PROJECT_ID=%s" % project)
    log("PRISM_SANDBOX_URL=%s" % sb_url)
    log("PRISM_SANDBOX_TOKEN=%s" % sb_token)
    log("")
    log("（注意：沙箱令牌与资源令牌都有有效期，过期后需要重新跑一遍这个脚本。）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
