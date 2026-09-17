#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""探测 /api/codex/conversation-history 的参数形态。

背景（2026-09-17 实测）：
- 端点存在（GET 405 / POST 200），认证通过
- bundle 参数：{conversationId, order, limit, after, userId, projectId}
  userId 取自前端 metadata.userId（即 start 请求 metadata 里的 userId）
- 用真实 cdx1_* 会话 ID + 多种 userId（probe 字符串 / prism owner uuid）
  查询均返回 items=0, backendConversationFound=false

待验证假设：
- 会话需绑定"文档"（Y-Sweet doc）才可查历史 —— 裸 API 创建的会话
  未绑定文档，后端不归档
- userId 需要特定形态（OpenAI 侧 user id vs prism owner uuid）
- 最快解法：用 MITM 抓一次真实前端加载会话时的 history 请求，
  对照参数即知差异（资料包 tools/prism_mitm_interceptor.py 可用）

用法：先跑一轮真实对话拿 conversationId（见 probe_output_shape.py），
然后：python probe_conversation_history.py <conversationId> [userId] [projectId]
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

with io.open(COOKIE_FILE, encoding="utf-8") as f:
    COOKIE = f.read().strip()


def history(payload):
    req = urllib.request.Request(BASE + "/api/codex/conversation-history",
                                 data=json.dumps(payload).encode(), method="POST")
    for k, v in {"accept": "application/json", "content-type": "application/json",
                 "origin": BASE, "referer": BASE + "/", "user-agent": UA,
                 "cookie": COOKIE}.items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=60) as r:
            return r.status, json.loads(r.read().decode("utf-8", "replace"))
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")[:200]


def main():
    conv = sys.argv[1] if len(sys.argv) > 1 else ""
    uid = sys.argv[2] if len(sys.argv) > 2 else None
    pid = sys.argv[3] if len(sys.argv) > 3 else None
    if not conv:
        print("用法: probe_conversation_history.py <conversationId> [userId] [projectId]")
        return 1

    q = {"conversationId": conv, "order": "desc", "limit": 50}
    if uid:
        q["userId"] = uid
    if pid:
        q["projectId"] = pid

    code, hist = history(q)
    print("HTTP %s" % code)
    if code != 200:
        print(hist)
        return 1
    print("conversationId        = %s" % hist.get("conversationId"))
    print("backendConversationFound = %s" % hist.get("backendConversationFound"))
    print("waitingForSandbox     = %s" % hist.get("waitingForSandbox"))
    print("hasMore / nextCursor  = %s / %s" % (hist.get("hasMore"), hist.get("nextCursor")))
    items = hist.get("items") or []
    print("items = %d" % len(items))
    for i, it in enumerate(items[:10]):
        print("  [%d] %s" % (i, json.dumps(it, ensure_ascii=False)[:300]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
