"""看 astra 失败时 codexRequestDebug 的 error 完整内容与 proxy_request_debug。"""
import io
import json
import threading
import time
import urllib.request

BASE = "https://prism.openai.com"
UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36")
with io.open(r"F:\Code\Active\OAIprism\secrets\cookies.txt", encoding="utf-8") as f:
    COOKIE = f.read().strip()
SB = "https://prism.openai.com/s/sandboxes/proxy/"
TOK = [l.split("=", 1)[1].strip()
       for l in io.open(r"C:\Users\13080\AppData\Local\Temp\sbx_probe6.log")
       if l.startswith("PRISM_SANDBOX_TOKEN")][0]
PID = [l.split("=", 1)[1].strip()
       for l in io.open(r"C:\Users\13080\AppData\Local\Temp\sbx_probe6.log")
       if l.startswith("PRISM_PROJECT_ID")][0]

stop = False


def heartbeat():
    while not stop:
        try:
            req = urllib.request.Request(SB + "heartbeat?cb=%d" % time.time_ns())
            for k, v in {"accept": "application/json", "origin": BASE, "referer": BASE + "/",
                         "user-agent": UA, "cookie": COOKIE,
                         "X-Crixet-Sandbox-Token": TOK}.items():
                req.add_header(k, v)
            urllib.request.urlopen(req, timeout=10)
        except Exception:
            pass
        time.sleep(5)


threading.Thread(target=heartbeat, daemon=True).start()

body = {
    "input": [{"type": "message", "role": "user",
               "content": [{"type": "input_text", "text": "say PONG"}]}],
    "metadata": {"model": "gpt-6-astra", "reasoning_effort": "medium", "projectId": PID,
                 "frontend_origin": BASE, "sandbox_url": SB, "sandbox_token": TOK},
}
req = urllib.request.Request(BASE + "/api/llm/response_with_tools_start",
                             data=json.dumps(body).encode(), method="POST")
for k, v in {"accept": "application/json", "content-type": "application/json",
             "origin": BASE, "referer": BASE + "/", "user-agent": UA,
             "cookie": COOKIE}.items():
    req.add_header(k, v)
with urllib.request.urlopen(req, timeout=90) as r:
    d = json.loads(r.read().decode())

payload = (d.get("response") or {}).get("payload") or {}
dbg = payload.get("codexRequestDebug") or {}
print("== codexRequestDebug 完整内容 ==")
print(json.dumps(dbg, ensure_ascii=False, indent=1)[:4000])
print()
print("== payload 除 debug 外的键 ==", sorted(payload.keys()))
