"""二分定位 astra v2 restore_start 400 的缺失字段。

变体：
1 astra + 精简 start（对照，预期 restore_start 400）
2 astra + 顶层塞 CLI 字段（client_metadata/include/prompt_cache_key/
  service_tier/text/parallel_tool_calls/reasoning/store/tool_choice）
3 astra + CLI 原始 input 翻译（43KB 全量消息）
4 astra + 变体2 + 变体3
每次打印完整 codexRequestDebug（restore 可能带专属字段）。
"""
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


def start(input_items, top_extra=None, model="gpt-6-astra"):
    body = {
        "input": input_items,
        "metadata": {"model": model, "reasoning_effort": "medium", "projectId": PID,
                     "frontend_origin": BASE, "sandbox_url": SB, "sandbox_token": TOK},
    }
    if top_extra:
        body.update(top_extra)
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


def report(tag, d):
    st = d.get("status")
    robj = d.get("response") or {}
    payload = robj.get("payload") or {}
    dbg = payload.get("codexRequestDebug") or {}
    err = dbg.get("error") or {}
    keys = sorted(dbg.keys())
    print("%s -> start=%s resp=%s" % (tag, st, robj.get("status")))
    print("   debug keys:", keys)
    if err:
        print("   err.url:", str(err.get("url"))[-60:])
        print("   err.body:", str(err.get("response") or err.get("body") or "")[:150])
    text = "".join(c.get("text", "") for it in (payload.get("output") or [])
                   for c in (it.get("content") or []))
    if text:
        print("   text:", text[:60])


# CLI 真实请求（取抓包里最长的一条）
cli_lines = [json.loads(l) for l in
             io.open(r"F:\Code\Active\OAIprism\.workbuddy\codex_capture.jsonl", encoding="utf-8")]
cli = max(cli_lines, key=lambda r: len(r["req_body"]))
cli_body = json.loads(cli["req_body"])

# 复刻翻译：message -> 上游消息（developer->system）
msg_items = []
for it in cli_body.get("input", []):
    if it.get("type") != "message":
        continue
    role = it.get("role", "user")
    txt = "\n".join(c.get("text", "") for c in it.get("content", []) if isinstance(c, dict))
    if role == "developer":
        role = "system"
    msg_items.append({"type": "message", "role": role,
                      "content": [{"type": "input_text", "text": txt}]})
print("CLI 消息翻译后条数:", len(msg_items))

# CLI 顶层字段（除 input/model/stream/store）
cli_top = {}
for k in ("client_metadata", "include", "prompt_cache_key", "service_tier",
          "text", "parallel_tool_calls", "reasoning", "tool_choice"):
    if k in cli_body:
        cli_top[k] = cli_body[k]
print("CLI 顶层字段:", sorted(cli_top.keys()))

SIMPLE = [{"type": "message", "role": "user",
           "content": [{"type": "input_text", "text": "say PONG"}]}]

print()
report("1 astra+精简       ", start(SIMPLE))
report("2 astra+CLI顶层字段 ", start(SIMPLE, top_extra=cli_top))
report("3 astra+CLI全量input", start(msg_items))
report("4 astra+全量+字段   ", start(msg_items, top_extra=cli_top))
print("对照 sol+精简       :", "见上轮已验证 started")
stop = True
