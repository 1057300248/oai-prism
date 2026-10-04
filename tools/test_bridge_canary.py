"""Offline tests only. No production URL, model key or inference is used."""
import json
import re
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from bridge_canary import CanaryError, Client, run


class FakeClient:
    def __init__(self, fail_second=False):
        self.requests = []
        self.markers = []
        self.fail_second = fail_second
        self.generations = 0

    def request(self, method, path, payload=None):
        self.requests.append((method, path, payload))
        if method == "GET" and path == "/models":
            return {"data": [{"id": "test-model"}]}, {}
        if method == "GET":
            return {"upload_mode": "raw", "continuation_enabled": True}, {}
        if method == "DELETE":
            return {"deleted": True}, {}
        self.generations += 1
        if self.generations == 1:
            text = json.dumps(payload)
            self.markers = re.findall(r"[IDU]_[a-f0-9]{16}", text)
        if self.generations == 2 and self.fail_second:
            raise CanaryError(409, "api_error", "continuation")
        text = " ".join(dict.fromkeys(self.markers))
        return {"id": "resp_" + str(self.generations), "status": "completed", "output": [
            {"type": "message", "content": [{"type": "output_text", "text": text}]}],
            "usage": {"input_tokens": 12, "output_tokens": 3, "total_tokens": 15,
                      "x_oaiprism_source": "upstream"}}, {"X-Oaiprism-Continuation": "delta" if self.generations == 2 else "new-stored-conversation"}


class CanaryTests(unittest.TestCase):
    def test_default_is_read_only(self):
        client = FakeClient()
        report = run(client, "test-model", False, False)
        self.assertEqual(report["model_calls"], 0)
        self.assertEqual(report["inference"], "not_requested")
        self.assertTrue(report["model_declared"])
        self.assertTrue(all(method == "GET" for method, _, _ in client.requests))

    def test_explicit_model_checks_are_bounded_and_cleaned(self):
        client = FakeClient()
        report = run(client, "test-model", True, True)
        posts = [p for method, _, p in client.requests if method == "POST"]
        self.assertEqual(len(posts), 2)
        self.assertEqual(report["model_calls"], 2)
        self.assertGreater(len(posts[0]["instructions"]), 2000)
        self.assertEqual(posts[0]["input"][0]["role"], "developer")
        self.assertEqual(posts[1]["previous_response_id"], "resp_1")
        self.assertTrue(report["instruction_and_history_check"])
        self.assertTrue(report["continuation_history_check"])
        self.assertTrue(report["delta_mode_observed"])
        deleted = [path for method, path, _ in client.requests if method == "DELETE"]
        self.assertEqual(deleted, ["/responses/resp_1", "/responses/resp_2"])
        serialized = json.dumps(report)
        for marker in client.markers:
            self.assertNotIn(marker, serialized)
        self.assertFalse(report["live_model_identity_verified"])
        self.assertFalse(report["visual_understanding_verified"])
        self.assertFalse(report["native_cache_verified"])

    def test_single_generation_does_not_store(self):
        client = FakeClient()
        report = run(client, "test-model", True, False)
        self.assertEqual(report["model_calls"], 1)
        posts = [p for method, _, p in client.requests if method == "POST"]
        self.assertFalse(posts[0]["store"])
        self.assertFalse(any(method == "DELETE" for method, _, _ in client.requests))

    def test_second_failure_still_cleans_first_snapshot_and_does_not_retry(self):
        client = FakeClient(fail_second=True)
        report = run(client, "test-model", True, True)
        self.assertEqual(report["inference_error"]["stage"], "continuation")
        self.assertEqual(report["model_calls"], 2)
        self.assertEqual([path for method, path, _ in client.requests if method == "DELETE"], ["/responses/resp_1"])

    def test_configuration_rejects_credential_urls_and_header_injection(self):
        for base in ("file:///etc/passwd", "https://user:password@example.test/v1",
                     "https://example.test/v1?key=secret", "http://example.test/v1", "https://example.test/v1#fragment"):
            with self.subTest(base=base), self.assertRaises(ValueError):
                Client(base, "key")
        for name, tenant in (("Authorization", "x"), ("Host", "x"), ("X-Tenant\r\n", "x"), ("X-Tenant", "x\nsecret")):
            with self.subTest(name=name), self.assertRaises(ValueError):
                Client("http://127.0.0.1:1/v1", "key", name, tenant)
        with self.assertRaises(ValueError):
            Client("https://example.test/v1", "")

    def test_http_errors_are_redacted_and_redirects_are_not_followed(self):
        hits = []
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass
            def do_GET(self):
                hits.append(self.path)
                if self.path == "/v1/redirect":
                    self.send_response(302)
                    self.send_header("Location", "/v1/credential-leak")
                    self.end_headers()
                    return
                self.send_response(503)
                self.end_headers()
                self.wfile.write(b'{"error":"private-cookie-in-upstream-body"}')
        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            client = Client(f"http://127.0.0.1:{server.server_port}/v1", "test-only-key")
            for path in ("/redirect", "/failure"):
                with self.assertRaises(CanaryError) as captured:
                    client.request("GET", path)
                self.assertNotIn("private-cookie", str(captured.exception))
                self.assertNotIn("test-only-key", str(captured.exception))
            self.assertEqual(hits, ["/v1/redirect", "/v1/failure"])
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=3)


if __name__ == "__main__":
    unittest.main(verbosity=2)
