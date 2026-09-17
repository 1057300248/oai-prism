"""
mitmdump addon：抓 Codex CLI <-> OAIprism 的 /v1/responses 流量。

用法：mitmdump --mode reverse:http://127.0.0.1:8788/ --listen-port 8787 -s tools/mitm_capture_codex.py
输出：.workbuddy/codex_capture.jsonl
"""

import json
import time

from mitmproxy import http

OUT = r"F:\Code\Active\OAIprism\.workbuddy\codex_capture.jsonl"


def response(flow: http.HTTPFlow):
    p = flow.request.path
    if not p.startswith("/v1/"):
        return
    rec = {
        "ts": time.time(),
        "method": flow.request.method,
        "path": p,
        "req_headers": {k: v for k, v in flow.request.headers.items()
                        if k.lower() not in ("authorization",)},
        "req_body": (flow.request.get_text() or "")[:200000] or None,
        "status": flow.response.status_code if flow.response else None,
        "resp_body": (flow.response.get_text() or "")[:80000] or None,
    }
    with open(OUT, "a", encoding="utf-8") as f:
        f.write(json.dumps(rec, ensure_ascii=False) + "\n")
