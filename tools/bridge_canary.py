"""Operator canary for a private gateway/API root. GET-only unless explicitly enabled.

No automatic retries, redirects, credential files, or captured conversation bodies.
The --allow-model-calls flag permits at most two small inference requests. It is
not run against production by CI and cannot certify model identity or billing.
"""
from __future__ import annotations

import argparse
import json
import os
import secrets
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from typing import Any

MAX_RESPONSE = 2 << 20


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *_args: Any, **_kwargs: Any) -> None:
        return None


class CanaryError(Exception):
    def __init__(self, status: int, code: str, stage: str = "") -> None:
        # Do not retain upstream bodies or exception strings containing tokens/URLs.
        self.status, self.code, self.stage = status, code, stage
        super().__init__(f"HTTP {status}: {code}")


class Client:
    def __init__(self, base_url: str, key: str, tenant_header: str = "", tenant: str = "",
                 allow_http: bool = False, timeout: float = 90) -> None:
        url = urllib.parse.urlsplit(base_url)
        if url.scheme not in ("http", "https") or not url.hostname or url.username or url.password or url.query or url.fragment:
            raise ValueError("Use an HTTP(S) API root without credentials, query or fragment.")
        if url.scheme == "http" and url.hostname not in ("localhost", "127.0.0.1", "::1") and not allow_http:
            raise ValueError("Remote plaintext HTTP requires --allow-http; HTTPS is preferred.")
        if not key or any(c in key for c in "\r\n"):
            raise ValueError("A non-empty environment-provided API key is required.")
        if bool(tenant_header) != bool(tenant):
            raise ValueError("Provide both tenant-header and tenant, or neither.")
        if tenant_header and (not all(c.isascii() and (c.isalnum() or c == '-') for c in tenant_header)
                              or tenant_header.lower() in ("authorization", "cookie", "host", "content-length")):
            raise ValueError("Invalid diagnostic tenant header.")
        if any(c in tenant for c in "\r\n") or len(tenant) > 256:
            raise ValueError("Invalid tenant label.")
        self.base = base_url.rstrip("/")
        self.headers = {"Authorization": "Bearer " + key, "Accept": "application/json"}
        if tenant_header:
            self.headers[tenant_header] = tenant
        self.timeout = timeout
        # Never send credentials through implicit environment HTTP proxies.
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())

    def request(self, method: str, path: str, payload: dict[str, Any] | None = None) -> tuple[dict[str, Any], dict[str, str]]:
        if not path.startswith("/") or ".." in path or "://" in path or "\r" in path or "\n" in path:
            raise ValueError("Invalid relative API path.")
        data = json.dumps(payload, ensure_ascii=False).encode() if payload is not None else None
        headers = dict(self.headers)
        if data is not None:
            headers["Content-Type"] = "application/json"
        req = urllib.request.Request(self.base + path, data=data, headers=headers, method=method)
        try:
            with self.opener.open(req, timeout=self.timeout) as response:
                raw = response.read(MAX_RESPONSE + 1)
                selected = {name: response.headers.get(name, "") for name in
                            ("X-Request-ID", "X-Oaiprism-Stage", "X-Oaiprism-Continuation", "X-Oaiprism-Usage-Source")}
        except urllib.error.HTTPError as exc:
            status = exc.code
            stage = exc.headers.get("X-Oaiprism-Stage", "")
            exc.close()
            raise CanaryError(status, "api_error", stage) from None
        except (urllib.error.URLError, TimeoutError, OSError):
            raise CanaryError(0, "transport_error") from None
        if len(raw) > MAX_RESPONSE:
            raise CanaryError(0, "response_too_large")
        try:
            value = json.loads(raw)
        except (ValueError, UnicodeError):
            raise CanaryError(0, "invalid_json_response") from None
        if not isinstance(value, dict):
            raise CanaryError(0, "invalid_response_object")
        return value, selected


def output_text(response: dict[str, Any]) -> str:
    output = response.get("output", [])
    if not isinstance(output, list):
        return ""
    return "".join(part.get("text", "") for item in output if isinstance(item, dict)
                   and item.get("type") == "message" for part in item.get("content", [])
                   if isinstance(part, dict) and part.get("type") == "output_text" and isinstance(part.get("text"), str))


def report_usage(response: dict[str, Any]) -> dict[str, Any]:
    usage = response.get("usage")
    if not isinstance(usage, dict):
        return {"available": False}
    allowed = ("input_tokens", "output_tokens", "total_tokens", "x_oaiprism_source")
    return {key: usage[key] for key in allowed if key in usage}


def run(client: Client, model: str, allow_model_calls: bool, continuation: bool) -> dict[str, Any]:
    result: dict[str, Any] = {"kind": "operator_canary", "model_calls": 0, "live_model_identity_verified": False,
                              "visual_understanding_verified": False, "native_cache_verified": False}
    for path in ("/models", "/transport-status"):
        try:
            data, _ = client.request("GET", path)
            if path == "/models":
                models = data.get("data", [])
                result["model_declared"] = any(isinstance(item, dict) and item.get("id") == model for item in models)
            else:
                result["transport_profile"] = {key: data.get(key) for key in
                    ("upload_mode", "instruction_placement", "max_start_bytes", "additional_tools", "continuation_enabled")}
        except CanaryError as exc:
            result[path] = {"status": exc.status, "code": exc.code, "stage": exc.stage}
    if not allow_model_calls:
        result["inference"] = "not_requested"
        return result
    if not model:
        raise ValueError("--model is required for inference.")
    prefix, developer, memory = [label + secrets.token_hex(8) for label in ("I_", "D_", "U_")]
    instructions = ("Transport canary. Begin every final answer with " + prefix + ".\n"
                    + "Preserve the exact requested markers. Do not call tools.\n" * 60)
    first_payload = {"model": model, "instructions": instructions, "stream": False, "store": continuation,
                     "input": [{"role": "developer", "content": "Place " + developer + " after the instruction marker."},
                               {"role": "user", "content": "Remember " + memory + ". Return the instruction marker, developer marker, and this memory marker separated by spaces, with no other text."}]}
    stored: list[str] = []
    try:
        started = time.monotonic()
        result["model_calls"] += 1
        first, headers = client.request("POST", "/responses", first_payload)
        result["instruction_and_history_check"] = all(token in output_text(first) for token in (prefix, developer, memory))
        result["first"] = {"status": first.get("status"), "elapsed_seconds": round(time.monotonic() - started, 3),
                           "usage": report_usage(first), "headers": headers}
        rid = first.get("id")
        if continuation and isinstance(rid, str) and rid.startswith("resp_") and len(rid) <= 128:
            stored.append(rid)
            result["model_calls"] += 1
            second, h2 = client.request("POST", "/responses", {"model": model, "instructions": instructions,
                "previous_response_id": rid, "input": "Repeat the same three markers from the prior answer in the same order. Nothing else.",
                "store": True, "stream": False})
            rid2 = second.get("id")
            if isinstance(rid2, str) and rid2.startswith("resp_") and len(rid2) <= 128:
                stored.append(rid2)
            result["continuation_history_check"] = all(token in output_text(second) for token in (prefix, developer, memory))
            result["delta_mode_observed"] = h2.get("X-Oaiprism-Continuation") == "delta"
            result["second"] = {"status": second.get("status"), "usage": report_usage(second), "headers": h2}
    except CanaryError as exc:
        result["inference_error"] = {"status": exc.status, "code": exc.code, "stage": exc.stage}
    finally:
        failures = 0
        for rid in stored:
            try:
                client.request("DELETE", "/responses/" + urllib.parse.quote(rid, safe=""))
            except CanaryError:
                failures += 1
        result["public_snapshot_cleanup_failures"] = failures
        result["upstream_workspace_cleanup_verified"] = False
    return result


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", required=True, help="API root, usually https://gateway.example/v1")
    parser.add_argument("--key-env", default="OAI_PRISM_CANARY_KEY")
    parser.add_argument("--model", default="")
    parser.add_argument("--tenant-header", default="")
    parser.add_argument("--tenant", default="")
    parser.add_argument("--allow-http", action="store_true")
    parser.add_argument("--allow-model-calls", action="store_true", help="Explicitly permit up to two charged model calls")
    parser.add_argument("--check-continuation", action="store_true", help="Temporarily create and delete two stored Responses")
    args = parser.parse_args()
    if args.check_continuation and not args.allow_model_calls:
        parser.error("--check-continuation also requires --allow-model-calls")
    try:
        client = Client(args.base_url, os.environ.get(args.key_env, ""), args.tenant_header, args.tenant, args.allow_http)
        evidence = run(client, args.model, args.allow_model_calls, args.check_continuation)
        print(json.dumps(evidence, ensure_ascii=False, indent=2))
        if args.allow_model_calls and (not evidence.get("instruction_and_history_check") or
            (args.check_continuation and not evidence.get("continuation_history_check"))):
            return 1
        return 0
    except ValueError as exc:
        parser.error(str(exc))
    return 2


if __name__ == "__main__":
    sys.exit(main())
