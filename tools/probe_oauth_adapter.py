"""OAuth 适配测试（对照 sub2api issue #256 的"登录模块 OAuth 适配"）。

用 OAIprism 已验证的轮子，测试三件事：
  测 A  refresh_token 换新 access_token（auth.openai.com/oauth/token）
  测 B  新 access_token 直接调 Prism API（纯 OAuth、无 session cookie）
  测 C  OAuth 凭据跑完整 start+poll 对话闭环

凭据从环境变量或临时文件读，输出全部脱敏。
"""
import base64
import io
import json
import os
import sys
import urllib.request
import urllib.error

# ---- 凭据输入：优先环境变量，避免硬编码 ----
ACCESS = os.environ.get("OAUTH_ACCESS", "")
REFRESH = os.environ.get("OAUTH_REFRESH", "")
if not ACCESS or not REFRESH:
    cred_file = r"C:\Users\13080\AppData\Local\Temp\oauth_creds.json"
    if os.path.exists(cred_file):
        c = json.loads(io.open(cred_file, encoding="utf-8").read())
        ACCESS, REFRESH = c["access_token"], c["refresh_token"]
if not ACCESS:
    print("缺少凭据"); sys.exit(1)

UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36")
CLIENT_ID = "app_jqKb52JverFFcl5GP4axT8QY"  # Prism OAuth client（与 creds 默认一致）


def mask(s, keep=12):
    return s[:keep] + "...(" + str(len(s)) + " chars)" if s else "(空)"


def post_json(url, payload, headers=None, timeout=60):
    req = urllib.request.Request(url, data=json.dumps(payload).encode(), method="POST")
    for k, v in {**{"Content-Type": "application/json", "Accept": "application/json",
                    "User-Agent": UA}, **(headers or {})}.items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, json.loads(r.read().decode())
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()[:300]
    except Exception as e:
        return 0, {"_err": str(e)[:200]}


def get_json(url, cookie="", timeout=30):
    req = urllib.request.Request(url)
    for k, v in {"Accept": "application/json", "Origin": "https://prism.openai.com",
                 "Referer": "https://prism.openai.com/", "User-Agent": UA,
                 "Cookie": cookie}.items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, r.read().decode()[:300]
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()[:200]


print("== 测 A：refresh_token 刷新 ==")
code, resp = post_json("https://auth.openai.com/oauth/token", {
    "client_id": CLIENT_ID,
    "grant_type": "refresh_token",
    "refresh_token": REFRESH,
})
print("HTTP", code)
if code == 200 and isinstance(resp, dict):
    new_access = resp.get("access_token", "")
    new_refresh = resp.get("refresh_token", "")
    print("  新 access_token:", mask(new_access))
    print("  新 refresh_token:", mask(new_refresh), "(轮转)" if new_refresh and new_refresh != REFRESH else "(未轮转)")
    # JWT claim 验证
    try:
        p = new_access.split(".")[1]
        claims = json.loads(base64.urlsafe_b64decode(p + "=" * (-len(p) % 4)))
        auth = claims.get("https://api.openai.com/auth", {})
        print("  plan:", auth.get("chatgpt_plan_type"), "| user:", auth.get("user_id", "")[:24])
    except Exception as e:
        print("  claim 解析失败:", e)
    # 把新 token 存给测 B/C 用
    with io.open(r"C:\Users\13080\AppData\Local\Temp\oauth_new_token.txt", "w") as f:
        f.write(new_access)
else:
    print("  失败:", str(resp)[:200])
    new_access = ACCESS
    io.open(r"C:\Users\13080\AppData\Local\Temp\oauth_new_token.txt", "w").write(new_access)

print()
print("== 测 B：access_token 直连 Prism API（纯 OAuth，无 session cookie）==")
for name, cookie in [
    ("Cookie 形态 prism_oai_access_token", "prism_oai_access_token=" + new_access),
    ("Cookie 形态 + session_token 同值", "prism_oai_access_token=" + new_access + "; prism_session_token=" + new_access),
]:
    c1, b1 = get_json("https://prism.openai.com/api/maintenance", cookie)
    c2, b2 = get_json("https://prism.openai.com/api/projects", cookie)
    print("  %-28s maintenance=%s projects=%s | %s" % (name, c1, c2, b2[:80]))
print("  （对照：旧 cookies.txt 全套）")
with io.open(r"F:\Code\Active\OAIprism\secrets\cookies.txt", encoding="utf-8") as f:
    old_cookie = f.read().strip()
c1, b1 = get_json("https://prism.openai.com/api/maintenance", old_cookie)
c2, b2 = get_json("https://prism.openai.com/api/projects", old_cookie)
print("  %-28s maintenance=%s projects=%s | %s" % ("旧 cookie 全套", c1, c2, b2[:80]))
