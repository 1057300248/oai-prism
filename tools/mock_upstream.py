"""上游 mock：确定性验证心跳与 response.failed。

用法：python tools/mock_upstream.py <port> <mode>
  mode=slow     所有 start 延迟 40s 后返回 started（验证等待期心跳）
  mode=fail     所有 start 立即返回失败（验证 response.failed 事件）
"""
import json
import sys
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 8791
MODE = sys.argv[2] if len(sys.argv) > 2 else "slow"


class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _json(self, obj, code=200):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        # 心跳/健康类端点：一律 200
        if "wait-for-sync" in self.path:
            self._json({"status": "synced"})
        elif "heartbeat" in self.path:
            self._json({"ok": True})
        else:
            self._json({})

    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        _ = self.rfile.read(n) if n else b""
        p = self.path

        if p.endswith("/api/projects"):
            self._json({"uuid": "00000000-0000-4000-8000-000000000001", "title": "mock"})
        elif p.endswith("/api/backend/1/new"):
            self._json({"url": "https://prism.openai.com/s/sandboxes/proxy",
                        "token": "mock-token", "sandbox_session_id": "mock-session"})
        elif "resources-token" in p:
            self._json({"access_token": "mock-rt", "resources_base_url":
                        "https://prism.openai.com/s/sandbox-resources",
                        "expires_at": "2099-01-01T00:00:00Z", "max_age_seconds": 3600})
        elif p.endswith("/api/y"):
            self._json({"url": "wss://mock/y/d/0000/ws", "baseUrl": "https://mock",
                        "authorization": "mock", "token": "mock-y"})
        elif p.endswith("/token"):
            self._json({"success": True})
        elif "response_with_tools_start" in p:
            if MODE == "fail":
                self._json({
                    "status": "completed",
                    "request_id": "mock-req-1",
                    "conversation_id": None,
                    "response": {"status": "error", "payload": {
                        "reason": "mock_failure",
                        "message": "mock 上游确定性失败（验证 response.failed）",
                        "httpStatus": 502}},
                })
            else:  # slow
                time.sleep(40)  # 关键：40s 静默，验证心跳保活
                self._json({
                    "status": "started",
                    "request_id": "mock-req-2",
                    "conversation_id": "mock-conv",
                    "turn_state": {"version": 1, "conversation_id": "mock-conv"},
                })
        elif "response_with_tools_status" in p:
            self._json({
                "status": "completed",
                "request_id": "mock-req-2",
                "turn_state": {"version": 1},
                "response": {"status": "success", "payload": {"output": [
                    {"type": "message", "role": "assistant",
                     "content": [{"type": "output_text", "text": "MOCK-OK"}]}]}},
            })
        elif "response_with_tools_stop" in p:
            self._json({"ok": True})
        else:
            self._json({})

    def log_message(self, *a):
        print("[mock]", self.path, flush=True)


print(f"[mock] listening :{PORT} mode={MODE}", flush=True)
HTTPServer(("127.0.0.1", PORT), H).serve_forever()
