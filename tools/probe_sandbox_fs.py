#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""探测沙箱代理的文件/命令类端点。

目的：确认「模型写在沙箱里的文件」有没有取回通道。
判定基准：404/405 = 路径不存在（无此能力）；200/4xx带JSON = 端点存在。
"""

import io
import json
import sys
import urllib.error
import urllib.request

BASE = "https://prism.openai.com"
UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36")

COOKIE_FILE = r"F:\Code\Active\OAIprism\secrets\cookies.txt"
SB_URL = "https://prism.openai.com/s/sandboxes/proxy/"
SB_TOKEN = sys.argv[1] if len(sys.argv) > 1 else ""

with io.open(COOKIE_FILE, encoding="utf-8") as f:
    COOKIE = f.read().strip()


def call(url, method="GET", payload=None, timeout=20):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    for k, v in {"accept": "application/json", "content-type": "application/json",
                 "origin": BASE, "referer": BASE + "/", "user-agent": UA,
                 "cookie": COOKIE, "X-Crixet-Sandbox-Token": SB_TOKEN}.items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            body = r.read().decode("utf-8", "replace")
            return r.status, body[:300]
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")[:300]
    except Exception as e:
        return 0, "%s: %s" % (type(e).__name__, e)


PATHS_GET = [
    "/", "/healthz", "/health", "/status",
    "/files", "/files/", "/api/files", "/api/fs", "/fs", "/fs/",
    "/list", "/workspace", "/workspace/", "/tree",
    "/api/workspace", "/api/list-files", "/list-files",
    "/download", "/api/download", "/snapshot", "/api/snapshot",
    "/export", "/api/export", "/archive", "/api/archive",
    "/api/session", "/session", "/api/info", "/info",
    "/wait-for-sync",
]
PATHS_POST = [
    "/files/list", "/fs/list", "/fs/read", "/read", "/files/read",
    "/exec", "/command", "/run", "/api/exec", "/execute",
    "/files/download", "/fs/download", "/tar", "/zip",
]

print("== GET 探测（沙箱代理）==")
for p in PATHS_GET:
    code, body = call(SB_URL.rstrip("/") + p)
    mark = "  " if code in (404, 405, 501) else "->"
    print("%s GET %-24s %s  %s" % (mark, p, code, body[:120].replace("\n", " ")))

print()
print("== POST 探测（JSON 体）==")
for p in PATHS_POST:
    code, body = call(SB_URL.rstrip("/") + p, method="POST", payload={})
    mark = "  " if code in (404, 405, 501) else "->"
    print("%s POST %-24s %s  %s" % (mark, p, code, body[:120].replace("\n", " ")))
