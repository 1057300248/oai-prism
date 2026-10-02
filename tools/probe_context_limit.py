# -*- coding: utf-8 -*-
"""上游最大上下文窗口试探（2026-10-02）。

方法：构造随机英文词填充（token 密度约 1 词 = 1.4 token），
打网关 /v1/chat/completions（单轮、stream:false、指令"只回复 OK"），
自适应：成功 → 翻倍；失败 → 二分。记录每轮 usage 估算与错误形态。
"""
import json
import random
import urllib.request
import urllib.error

GW = "http://127.0.0.1:8787/v1/chat/completions"
SESSION = "ctxprobe-" + str(random.randint(1000, 9999))

WORDS = ("alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima mike "
         "november oscar papa quebec romeo sierra tango uniform victor whiskey xray yankee "
         "zulu mango papaya lychee durian guava kiwi peach plum berry melon fig date nut "
         "pineapple coconut apricot cherry grape lemon orange banana apple pear quince").split()


def make_fill(target_tokens: int) -> str:
    """按目标 token 数（粗估）生成随机词填充。"""
    out = []
    used = 0
    while used < target_tokens:
        w = random.choice(WORDS)
        out.append(w)
        used += max(1, round(len(w) / 4) + 0.5)  # 粗估：1 词 ≈ 1.5 token
    # 首尾放指令，中间全是填充
    return ("记住暗号：ZQC%d。收到只需回复已记住。\n\n以下是填充内容，请忽略：\n" % target_tokens
            + " ".join(out)
            + "\n\n填充结束。只回复：已记住。不要复述填充内容。")


def probe(target_tokens: int, timeout: int = 240):
    body = json.dumps({
        "model": "gpt-5.6-sol",
        "stream": False,
        "messages": [{"role": "user", "content": make_fill(target_tokens)}],
    }).encode()
    req = urllib.request.Request(GW, data=body, method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("X-Oaiprism-Session", SESSION)
    t0 = __import__("time").time()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            d = json.loads(r.read().decode("utf-8", "replace"))
        content = (d.get("choices") or [{}])[0].get("message", {}).get("content", "")
        usage = d.get("usage") or {}
        return {
            "ok": True, "secs": round(__import__("time").time() - t0, 1),
            "content": content[:60], "usage": usage,
        }
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", "replace")
        try:
            msg = json.loads(raw).get("error", {}).get("message", raw[:200])
        except Exception:
            msg = raw[:200]
        return {"ok": False, "secs": round(__import__("time").time() - t0, 1), "http": e.code, "msg": msg}
    except Exception as e:
        return {"ok": False, "secs": round(__import__("time").time() - t0, 1), "msg": str(e)[:200]}


def main():
    lo_ok, hi_fail = 0, None            # 已知成功上界 / 已知失败下界（token 目标值）
    n = 16384                            # 起步 16k
    results = []
    for round_i in range(12):
        print(f"[{round_i+1}] 试探 {n} tokens (~{n*6} chars) ...", flush=True)
        r = probe(n)
        r["target"] = n
        results.append(r)
        print("   ", json.dumps(r, ensure_ascii=False)[:240], flush=True)
        if r["ok"]:
            lo_ok = n
            n = (hi_fail * 3 // 4) if hi_fail else n * 2
        else:
            hi_fail = n
            # 错误形态一眼可见就停（context/length 关键词）
            if any(k in str(r.get("msg", "")).lower() for k in ("context", "length", "too long", "token")):
                print(">>> 疑似命中上限错误，停止", flush=True)
                break
            if lo_ok and hi_fail - lo_ok < 4096:
                print(">>> 区间收敛", flush=True)
                break
            n = (lo_ok + hi_fail) // 2
        if n <= lo_ok:
            break
    print("\n=== 结果汇总 ===")
    for r in results:
        u = r.get("usage") or {}
        print(f"target={r['target']:>7}  ok={r['ok']}  secs={r['secs']:>5}  "
              f"prompt={u.get('prompt_tokens','-')}  msg={str(r.get('msg',''))[:80]}{r.get('content','')}")
    print(f"\n结论：成功上界 ≈ {lo_ok} tokens，失败下界 ≈ {hi_fail}")


if __name__ == "__main__":
    main()
