"""
mitmdump 捕获脚本：抓 prism 前端真实请求（重点 conversation-history）。

用法：mitmdump -s tools/mitm_capture_history.py -p 18080
输出：.workbuddy/mitm_capture.jsonl（每行一个请求/响应对）
"""

import json
import time

from mitmproxy import http

OUT = r"F:\Code\Active\OAIprism\.workbuddy\mitm_capture.jsonl"
INTEREST = ("/api/llm/", "/api/codex/", "/api/projects", "/api/y",
            "/s/sandboxes/", "/api/project-files", "/api/user",
            "/api/auth", "/api/backend", "/api/file-management")


def response(flow: http.HTTPFlow):
    path = flow.request.path
    if not any(k in path for k in INTEREST):
        return
    rec = {
        "ts": time.time(),
        "method": flow.request.method,
        "url": flow.request.pretty_url,
        "req_headers": {k: v for k, v in flow.request.headers.items()
                        if k.lower() not in ("cookie",)},
        "req_body": (flow.request.get_text() or "")[:400000] or None,
        "status": flow.response.status_code if flow.response else None,
        "resp_body": (flow.response.get_text() or "")[:40000] or None,
    }
    with open(OUT, "a", encoding="utf-8") as f:
        f.write(json.dumps(rec, ensure_ascii=False) + "\n")
