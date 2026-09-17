import io
import json
import threading
import time
import urllib.request
import urllib.error

BASE = "https://prism.openai.com"
UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36")
with io.open(r"F:\Code\Active\OAIprism\secrets\cookies.txt", encoding="utf-8") as f:
    COOKIE = f.read().strip()
SB = "https://prism.openai.com/s/sandboxes/proxy/"
TOK = [l.split('=', 1)[1].strip()
       for l in io.open(r"C:\Users\13080\AppData\Local\Temp\sbx_probe4.log")
       if l.startswith("PRISM_SANDBOX_TOKEN")][0]
PID = [l.split('=', 1)[1].strip()
       for l in io.open(r"C:\Users\13080\AppData\Local\Temp\sbx_probe4.log")
       if l.startswith("PRISM_PROJECT_ID")][0]
# sol 会话（runtime/debug 之前确认存在的 cdx1_ 会话）
KNOWN_CONV = "cdx1_e8acf272-75ab-4379-99c8-4e83c1b1d10d"

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


def start(model, extra_meta=None, conversation_id=None, effort="medium"):
    body = {
        "input": [{"type": "message", "role": "user",
                   "content": [{"type": "input_text", "text": "say PONG"}]}],
        "metadata": {"model": model, "reasoning_effort": effort, "projectId": PID,
                     "frontend_origin": BASE, "sandbox_url": SB, "sandbox_token": TOK},
    }
    if conversation_id:
        body["conversationId"] = conversation_id
    if extra_meta:
        body["metadata"].update(extra_meta)
    req = urllib.request.Request(BASE + "/api/llm/response_with_tools_start",
                                 data=json.dumps(body).encode(), method="POST")
    for k, v in {"accept": "application/json", "content-type": "application/json",
                 "origin": BASE, "referer": BASE + "/", "user-agent": UA,
                 "cookie": COOKIE}.items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=90) as r:
            return json.loads(r.read().decode())
    except urllib.error.HTTPError as e:
        return {"_http": e.code, "_body": e.read().decode()[:200]}


def summarize(d):
    st = d.get("status")
    robj = d.get("response") or {}
    payload = robj.get("payload") or {}
    dbg = payload.get("codexRequestDebug") or {}
    err = dbg.get("error") or {}
    text = "".join(c.get("text", "") for it in (payload.get("output") or [])
                   for c in (it.get("content") or []))
    conv = d.get("conversation_id") or payload.get("conversationId") or ""
    return "start=%s resp=%s conv=%s text=%r err=%s" % (
        st, robj.get("status"), conv[:28], text[:40],
        (err.get("url") or "无")[:70])


print("== 基线 ==")
print("1 astra 纯           :", summarize(start("gpt-6-astra")))
print("2 astra + convId(sol会话):", summarize(start("gpt-6-astra", conversation_id=KNOWN_CONV)))
print("3 astra + v2 标志    :", summarize(start("gpt-6-astra", extra_meta={"codex_v2": True})))
print("4 astra + backendConvId:", summarize(start("gpt-6-astra",
      extra_meta={"backend_conversation_id": KNOWN_CONV})))
print("5 terra 对照         :", summarize(start("gpt-5.6-terra")))
print("6 terra + convId     :", summarize(start("gpt-5.6-terra", conversation_id=KNOWN_CONV)))
