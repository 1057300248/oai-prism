#!/usr/bin/env python3
"""从本机浏览器取出 openai.com 域的 Cookie，供 oaiprism 导入凭据。

为什么需要这个脚本
------------------
Chrome / Edge 127+ 对 Cookie 启用了 **App-Bound Encryption**（值是 `v20` 前缀），
密钥被绑定到浏览器可执行文件，单纯用 Windows DPAPI 解不开
（这是 Google 专门为防"恶意程序偷 Cookie"加的）。

所以本脚本提供两条路径：

  dpapi — 直接读 Cookie 数据库 + DPAPI 解密。
          只对旧版浏览器有效，遇到 v20 会明确报错而不是静默失败。
  cdp   — 用 DevTools 协议让**浏览器自己**把 Cookie 解密后交出来。
          对任意版本有效，原理是 CDP 的 Storage.getCookies 会返回明文
          （包括 HttpOnly 的会话 Cookie）。

默认 auto：先试 dpapi，失败再走 cdp。

安全边界（明确写清楚）
----------------------
* 只读取本机、当前用户、openai.com / chatgpt.com 域的 Cookie；
* 不发起任何网络请求（cdp 模式只连本机 127.0.0.1 的调试端口）；
* 不改动浏览器数据（数据库以只读 URI 打开；cdp 模式只读不写）；
* 用完即退出，不驻留任何后台进程；
* 输出等同账号密码，脚本默认只往标准输出/指定文件写，不打日志。

用法
----
    python tools/browser_cookies.py --check              # 只报告找到哪些 Cookie 名
    python tools/browser_cookies.py --json --out secrets/accounts.json
    python tools/browser_cookies.py --browser edge --method cdp
"""

from __future__ import annotations

import argparse
import base64
import ctypes
import json
import os
import shutil
import socket
import sqlite3
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from ctypes import wintypes
from pathlib import Path

# 只关心这些域，其它一律不碰。
TARGET_SUFFIXES = ("openai.com", "chatgpt.com", "oaistatic.com")

# 关键 Cookie 名（按重要性排序）。
KEY_COOKIES = [
    "__Secure-next-auth.session-token",
    "__Secure-authjs.session-token",
    "next-auth.session-token",
    "oai-did",
    "oai-client-auth-session",
    "cf_clearance",
    "__cf_bm",
    "_cfuvid",
]

# 缺少这些就没法反代。
ESSENTIAL = KEY_COOKIES[:3]

BROWSERS = {
    "edge": {
        "label": "Microsoft Edge",
        "rel": "Microsoft/Edge",
        "exe": [
            r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe",
            r"C:\Program Files\Microsoft\Edge\Application\msedge.exe",
        ],
    },
    "chrome": {
        "label": "Google Chrome",
        "rel": "Google/Chrome",
        "exe": [
            r"C:\Program Files\Google\Chrome\Application\chrome.exe",
            r"C:\Program Files (x86)\Google\Chrome\Application\chrome.exe",
        ],
    },
    "brave": {
        "label": "Brave",
        "rel": "BraveSoftware/Brave-Browser",
        "exe": [
            r"C:\Program Files\BraveSoftware\Brave-Browser\Application\brave.exe",
        ],
    },
}


# ---------------------------- Windows DPAPI ----------------------------

class DATA_BLOB(ctypes.Structure):
    _fields_ = [("cbData", wintypes.DWORD),
                ("pbData", ctypes.POINTER(ctypes.c_char))]


def dpapi_unprotect(data: bytes) -> bytes:
    """用当前用户的 DPAPI 主密钥解包数据。

    不需要管理员权限 —— DPAPI 的用户作用域就是"当前登录用户"，
    也正因如此，换用户或换机器都解不开。
    """
    buf = ctypes.create_string_buffer(data, len(data))
    blob_in = DATA_BLOB(len(data), ctypes.cast(buf, ctypes.POINTER(ctypes.c_char)))
    blob_out = DATA_BLOB()

    ok = ctypes.windll.crypt32.CryptUnprotectData(
        ctypes.byref(blob_in), None, None, None, None, 0, ctypes.byref(blob_out)
    )
    if not ok:
        raise OSError(f"CryptUnprotectData 失败 (err={ctypes.GetLastError()})")

    out = ctypes.string_at(blob_out.pbData, blob_out.cbData)
    ctypes.windll.kernel32.LocalFree(blob_out.pbData)
    return out


class AppBoundEncryption(Exception):
    """浏览器启用了 App-Bound 加密，DPAPI 路径走不通。"""


def get_master_key(local_state: Path) -> bytes:
    with open(local_state, "r", encoding="utf-8") as f:
        state = json.load(f)

    enc = state.get("os_crypt", {}).get("encrypted_key")
    if not enc:
        raise RuntimeError("Local State 里没有 os_crypt.encrypted_key")

    raw = base64.b64decode(enc)
    if raw[:5] != b"DPAPI":
        raise AppBoundEncryption(f"主密钥前缀是 {raw[:5]!r} 而不是 DPAPI")
    return dpapi_unprotect(raw[5:])


# ---------------------------- 策略一：DPAPI ----------------------------

def decrypt_value_dpapi(encrypted: bytes, key: bytes) -> str:
    if not encrypted:
        return ""

    prefix = encrypted[:3]

    if prefix in (b"v10", b"v11"):
        from Crypto.Cipher import AES
        nonce, payload = encrypted[3:15], encrypted[15:]
        if len(payload) < 16:
            return ""
        cipher = AES.new(key, AES.MODE_GCM, nonce=nonce)
        try:
            return cipher.decrypt_and_verify(payload[:-16], payload[-16:]).decode("utf-8", "replace")
        except ValueError:
            return ""

    if prefix == b"v20":
        raise AppBoundEncryption("Cookie 使用 v20（App-Bound）加密")

    try:
        return dpapi_unprotect(encrypted).decode("utf-8", "replace")
    except OSError:
        return ""


def find_profiles(browser: str) -> list[Path]:
    root = Path(os.environ["LOCALAPPDATA"]) / BROWSERS[browser]["rel"] / "User Data"
    if not root.is_dir():
        return []
    out = []
    for p in [root / "Default", *sorted(root.glob("Profile *"))]:
        if (p / "Network" / "Cookies").exists() or (p / "Cookies").exists():
            out.append(p)
    return out


def read_via_dpapi(browser: str) -> list[tuple[str, str, str]]:
    profiles = find_profiles(browser)
    if not profiles:
        raise RuntimeError(f"没有找到 {BROWSERS[browser]['label']} 的用户数据目录")

    local_state = Path(os.environ["LOCALAPPDATA"]) / BROWSERS[browser]["rel"] / "User Data" / "Local State"
    key = get_master_key(local_state)

    found: list[tuple[str, str, str]] = []
    seen: set[tuple[str, str]] = set()

    for profile in profiles:
        src = profile / "Network" / "Cookies"
        if not src.exists():
            src = profile / "Cookies"

        tmp = Path(tempfile.gettempdir()) / f"_oai_ck_{browser}_{profile.name}.db"
        try:
            # 浏览器运行时这个文件是独占锁定的，复制会直接失败。
            shutil.copy2(src, tmp)
        except (OSError, PermissionError) as e:
            raise RuntimeError(
                f"Cookie 数据库被占用（{BROWSERS[browser]['label']} 正在运行）。\n"
                f"请先完全退出该浏览器，或改用 --method cdp。原始错误：{e}"
            ) from e

        try:
            con = sqlite3.connect(f"file:{tmp.as_posix()}?mode=ro", uri=True)
            try:
                rows = con.execute("SELECT host_key, name, encrypted_value FROM cookies").fetchall()
            finally:
                con.close()

            for host, name, enc in rows:
                if not any(host.endswith(s) for s in TARGET_SUFFIXES):
                    continue
                if (host, name) in seen:
                    continue
                val = decrypt_value_dpapi(enc, key)
                if not val:
                    continue
                seen.add((host, name))
                found.append((host, name, val))
        finally:
            try:
                tmp.unlink()
            except OSError:
                pass

    return found


# ---------------------------- 策略二：CDP ----------------------------

def find_exe(browser: str) -> str:
    for p in BROWSERS[browser]["exe"]:
        if Path(p).exists():
            return p
    raise RuntimeError(f"找不到 {BROWSERS[browser]['label']} 的可执行文件")


def free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def wait_for_cdp(port: int, timeout: float = 30.0) -> str:
    """轮询直到调试端口可用，返回 browser 级 WebSocket 地址。"""
    deadline = time.time() + timeout
    url = f"http://127.0.0.1:{port}/json/version"
    last = ""
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=2) as r:
                info = json.loads(r.read())
                ws = info.get("webSocketDebuggerUrl")
                if ws:
                    return ws
        except (urllib.error.URLError, OSError, json.JSONDecodeError, TimeoutError) as e:
            last = str(e)
        time.sleep(0.4)
    raise RuntimeError(
        f"浏览器启动后 {timeout:.0f} 秒内没有开放调试端口（最后错误：{last}）。\n"
        "最常见原因：浏览器正在运行，新进程被转发到已有实例而不新开端口。"
    )


def clone_minimal_profile(browser: str) -> Path:
    """把"能恢复登录态"的最小文件集复制到临时目录。

    为什么不能直接用原始 profile 目录：
    Chrome/Edge 136 起禁止对**默认用户数据目录**开启远程调试端口
    （官方就是为了防"外部程序用 CDP 偷 Cookie"）。绕过的唯一办法
    是指定一个非默认的 user-data-dir，所以要先克隆一份。

    为什么只复制这几个文件：
    Cookie 库 + 加密主密钥就足以还原登录态，全量复制 profile 动辄几百 MB
    且包含大量无关隐私数据。少复制 = 快 + 干净。

    前置条件：目标浏览器必须已退出，否则 Cookies 被独占锁定复制不了。
    """
    src_root = Path(os.environ["LOCALAPPDATA"]) / BROWSERS[browser]["rel"] / "User Data"
    dst_root = Path(tempfile.gettempdir()) / f"_oai_clone_{browser}" / "User Data"

    shutil.rmtree(dst_root.parent, ignore_errors=True)
    (dst_root / "Default" / "Network").mkdir(parents=True, exist_ok=True)

    def cp(rel: str, required: bool) -> None:
        s = src_root / rel
        if not s.exists():
            if required:
                raise RuntimeError(f"缺少必要文件 {s}")
            return
        try:
            shutil.copy2(s, dst_root / rel)
        except (OSError, PermissionError) as e:
            raise RuntimeError(
                f"无法复制 {s}：{e}\n"
                f"说明 {BROWSERS[browser]['label']} 仍在运行。请先完全退出该浏览器。"
            ) from e

    cp("Local State", True)
    cp("Default/Network/Cookies", True)
    # WAL/SHM 里可能有尚未合并的最新写入，一并带上。
    cp("Default/Network/Cookies-wal", False)
    cp("Default/Network/Cookies-shm", False)
    cp("Default/Preferences", False)

    return dst_root


def read_via_cdp(browser: str, headless: bool, timeout: float) -> list[tuple[str, str, str]]:
    """让浏览器自己解密 Cookie 并通过 DevTools 协议交出明文。

    这是对付 App-Bound 加密最干净的办法：不破解任何加密，
    只是以浏览器自己的身份向它要数据。
    """
    import websocket  # websocket-client

    exe = find_exe(browser)
    clone = clone_minimal_profile(browser)
    port = free_port()

    args = [
        exe,
        f"--remote-debugging-port={port}",
        f"--user-data-dir={clone}",
        "--profile-directory=Default",
        "--no-first-run",
        "--no-default-browser-check",
        "--disable-extensions",
        "--disable-sync",
        "--disable-background-networking",
        "about:blank",
    ]
    if headless:
        args.insert(1, "--headless=new")

    # CREATE_NO_WINDOW：不要让黑框闪出来。
    creationflags = 0x08000000 if sys.platform == "win32" else 0
    proc = subprocess.Popen(
        args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        creationflags=creationflags,
    )

    raw = None
    try:
        ws_url = wait_for_cdp(port, timeout)
        # suppress_origin：CDP 会拒绝带 Origin 头的 WebSocket 连接（403）。
        # 这里从客户端侧不发 Origin，比给浏览器加 --remote-allow-origins=*
        # 更合适 —— 后者等于放宽浏览器的调试端口保护。
        ws = websocket.create_connection(ws_url, timeout=timeout, suppress_origin=True)
        try:
            ws.send(json.dumps({"id": 1, "method": "Storage.getCookies"}))
            deadline = time.time() + timeout
            while time.time() < deadline:
                msg = json.loads(ws.recv())
                if msg.get("id") == 1:
                    raw = msg
                    break
            if raw is None or "result" not in raw:
                raise RuntimeError(f"CDP 未返回 Cookie：{raw}")
        finally:
            ws.close()
    finally:
        # 无论成败都要收尾：关掉整棵浏览器进程树，并删掉含 Cookie 库的克隆目录。
        kill_process_tree(proc)
        cleanup_clone(clone.parent)

    out: list[tuple[str, str, str]] = []
    seen: set[tuple[str, str]] = set()
    for c in raw["result"].get("cookies", []):
        domain = str(c.get("domain", "")).lstrip(".")
        if not any(domain == s or domain.endswith("." + s) for s in TARGET_SUFFIXES):
            continue
        key = (c.get("domain", ""), c.get("name", ""))
        if key in seen:
            continue
        seen.add(key)
        out.append((c.get("domain", ""), c.get("name", ""), c.get("value", "")))
    return out


def kill_process_tree(proc: subprocess.Popen) -> None:
    """终止浏览器进程树。

    只 terminate 启动器进程是不够的：Chromium 是多进程架构，
    renderer/gpu/utility 会变成孤儿继续驻留，占用内存并持有 profile 锁。
    Windows 上用 taskkill /T 按 PID 杀整棵树才是可靠做法。
    """
    if proc.poll() is not None:
        return
    if sys.platform == "win32":
        try:
            subprocess.run(
                ["taskkill", "/F", "/T", "/PID", str(proc.pid)],
                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                timeout=15, creationflags=0x08000000,
            )
        except (subprocess.TimeoutExpired, OSError):
            proc.kill()
    else:
        proc.terminate()
    try:
        proc.wait(timeout=10)
    except subprocess.TimeoutExpired:
        proc.kill()


def cleanup_clone(path: Path) -> int:
    """删除克隆目录。

    必须重试：浏览器刚退出时文件句柄可能还没释放，
    一次 rmtree 常常静默失败，把整个 Cookie 库留在临时目录里。
    """
    for _ in range(5):
        shutil.rmtree(path, ignore_errors=True)
        if not path.exists():
            return 0
        time.sleep(0.6)
    return 1


# ---------------------------- 输出 ----------------------------

def sort_cookies(cookies: list[tuple[str, str, str]]) -> list[tuple[str, str, str]]:
    order = {n: i for i, n in enumerate(KEY_COOKIES)}
    return sorted(cookies, key=lambda c: (order.get(c[1], 999), c[0], c[1]))


def to_header(cookies: list[tuple[str, str, str]]) -> str:
    return "; ".join(f"{n}={v}" for _, n, v in cookies)


def report(cookies: list[tuple[str, str, str]]) -> None:
    print(f"共找到 {len(cookies)} 个 openai 域 Cookie：\n")
    for host, name, val in cookies:
        mark = "★" if name in ESSENTIAL else " "
        print(f"  {mark} {name:<42} {len(val):>5} 字符  ({host})")

    have = {n for _, n, _ in cookies}
    print()
    if have & set(ESSENTIAL):
        name = next(n for n in ESSENTIAL if n in have)
        print(f"★ 找到了会话凭据：{name}")
        print("  → 可以直接用于反代。")
    else:
        print("✗ 没有找到任何会话凭据（__Secure-next-auth.session-token 等）。")
        print("  → 说明这个浏览器没有登录 prism.openai.com，或登录态已失效。")
        print("     请在该浏览器里打开 https://prism.openai.com 并完成登录后重试。")


def main() -> int:
    ap = argparse.ArgumentParser(
        description="读取本机浏览器里 openai.com 域名的 Cookie（仅本机、仅该域名）"
    )
    ap.add_argument("--browser", default="auto", choices=[*sorted(BROWSERS), "auto"])
    ap.add_argument("--method", default="auto", choices=["auto", "dpapi", "cdp"])
    ap.add_argument("--json", action="store_true", help="输出 accounts.json 格式")
    ap.add_argument("--id", default="main", help="配合 --json 使用的账号 ID")
    ap.add_argument("--check", action="store_true", help="只列出找到了哪些 Cookie，不输出值")
    ap.add_argument("--out", default="", help="把结果写入文件（而非标准输出）")
    ap.add_argument("--windowed", action="store_true", help="cdp 模式下显示浏览器窗口（默认无窗口）")
    args = ap.parse_args()

    if sys.platform != "win32":
        print("此脚本目前只支持 Windows（依赖 DPAPI 与本地浏览器路径）。", file=sys.stderr)
        return 2

    browsers = [args.browser] if args.browser != "auto" else ["edge", "chrome", "brave"]

    cookies: list[tuple[str, str, str]] = []
    notes: list[str] = []

    for browser in browsers:
        if not any(Path(p).exists() for p in BROWSERS[browser]["exe"]):
            continue
        if not find_profiles(browser):
            continue

        # 先试 DPAPI（如果被锁或遇 v20，会自动落到 cdp）。
        if args.method in ("auto", "dpapi"):
            try:
                cookies = read_via_dpapi(browser)
                if cookies:
                    notes.append(f"来源：{BROWSERS[browser]['label']}（DPAPI）")
                    break
            except AppBoundEncryption as e:
                notes.append(f"{BROWSERS[browser]['label']}：{e}，改用 CDP")
            except RuntimeError as e:
                notes.append(f"{BROWSERS[browser]['label']}：{e}")

        if args.method == "dpapi":
            continue

        try:
            cookies = read_via_cdp(browser, headless=not args.windowed, timeout=30)
            if cookies:
                notes.append(f"来源：{BROWSERS[browser]['label']}（DevTools 协议）")
                break
            notes.append(f"{BROWSERS[browser]['label']}：没有 openai 域 Cookie")
        except Exception as e:  # noqa: BLE001 - 要把失败原因如实报给用户
            notes.append(f"{BROWSERS[browser]['label']}：CDP 失败 - {e}")

    for n in notes:
        print(f"[i] {n}", file=sys.stderr)

    if not cookies:
        print(
            "\n所有浏览器都没取到 openai 域 Cookie。\n"
            "请先在一个浏览器里打开并登录 https://prism.openai.com，然后重跑本脚本。",
            file=sys.stderr,
        )
        return 1

    cookies = sort_cookies(cookies)

    if args.check:
        report(cookies)
        return 0

    if args.json:
        payload = {
            "_说明": "由 tools/browser_cookies.py 自动导出。此文件等同账号密码，切勿提交或分享。",
            "accounts": [{"id": args.id, "name": args.id, "cookies": to_header(cookies)}],
        }
        text = json.dumps(payload, ensure_ascii=False, indent=2)
    else:
        text = to_header(cookies)

    if args.out:
        p = Path(args.out)
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text, encoding="utf-8")
        try:
            os.chmod(p, 0o600)
        except OSError:
            pass
        have = {n for _, n, _ in cookies}
        print(f"已写入 {p}（{len(cookies)} 个 Cookie"
              f"{'，含会话凭据' if have & set(ESSENTIAL) else '，但缺少会话凭据'}）",
              file=sys.stderr)
    else:
        print(text)

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
