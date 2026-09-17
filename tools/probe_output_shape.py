#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""抓上游终态 output 的完整结构。

问题：模型在沙箱里"调用工具"（写文件/跑命令），这些调用会不会出现在
response.payload.output 里？如果出现（function_call / shell 之类条目），
代理至少能把"模型在云端干了什么"透明回显给客户端；
如果只有 message/reasoning，工具循环就在云端彻底黑盒消化了。
"""

import io
import json
import sys
import time
import urllib.error
import urllib.request

BASE = "https://prism.openai.com"
UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36")

COOKIE_FILE = r"F:\Code\Active\OAIprism\secrets\cookies.txt"

with io.open(COOKIE_FILE, encoding="utf-8") as f:
    COOKIE = f.read().strip()

PROJECT = "23871bb5-d926-4f12-9c82-e23becd828c2"
SB_URL = "https://prism.openai.com/s/sandboxes/proxy/"
SB_TOKEN = sys.argv[1]


def api(path, payload, timeout=120):
    data = json.dumps(payload).encode()
    req = urllib.request.Request(BASE + path, data=data, method="POST")
    for k, v in {"accept": "application/json", "content-type": "application/json",
                 "origin": BASE, "referer": BASE + "/", "user-agent": UA,
                 "cookie": COOKIE}.items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, json.loads(r.read().decode("utf-8", "replace"))
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")[:300]


PROMPT = ("Use your shell tool to run exactly: echo marker-abc123 > /workspace/marker.txt "
          "and then run: cat /workspace/marker.txt. Report the command output.")

body = {
    "input": [{"type": "message", "role": "user",
               "content": [{"type": "input_text", "text": PROMPT}]}],
    "metadata": {
        "model": "gpt-5.6-sol",
        "reasoning_effort": "low",
        "projectId": PROJECT,
        "frontend_origin": BASE,
        "sandbox_url": SB_URL,
        "sandbox_token": SB_TOKEN,
    },
}

code, resp = api("/api/llm/response_with_tools_start", body)
print("start HTTP %s" % code)
if code != 200:
    print(resp)
    sys.exit(1)
print("start.status = %s" % resp.get("status"))

rid = resp.get("request_id")
ts = resp.get("turn_state")
if not rid:
    print("无 request_id，直接终态？dump：")
    print(json.dumps(resp, ensure_ascii=False, indent=2)[:3000])
    sys.exit(0)

envelope = resp
for i in range(60):
    time.sleep(1.5)
    code, envelope = api("/api/llm/response_with_tools_status",
                         {"request_id": rid, "turn_state": ts})
    if code != 200:
        print("status HTTP %s: %s" % (code, envelope))
        sys.exit(1)
    st = envelope.get("status")
    ts = envelope.get("turn_state") or ts  # 逐轮取最新
    print("poll #%d status=%s" % (i + 1, st))
    if st == "completed":
        break

resp_obj = envelope.get("response") or {}
payload = resp_obj.get("payload") or {}
output = payload.get("output") or []

print()
print("=" * 70)
print("response.status = %s" % resp_obj.get("status"))
print("output 条目数 = %d" % len(output))
for i, item in enumerate(output):
    keys = sorted(item.keys())
    typ = item.get("type")
    role = item.get("role", "")
    print("- [%d] type=%s role=%s keys=%s" % (i, typ, role, keys))
    if typ == "message":
        for c in item.get("content", []) or []:
            t = (c.get("text") or "")[:200]
            print("      content: type=%s text=%r" % (c.get("type"), t))
    else:
        s = json.dumps(item, ensure_ascii=False)
        print("      raw: %s" % s[:500])
