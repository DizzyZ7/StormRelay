#!/usr/bin/env python3
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PROTOCOL = "stormrelay.plugin/v1"


class Handler(BaseHTTPRequestHandler):
    server_version = "stormrelay-echo-plugin/0.1"

    def log_message(self, fmt, *args):
        return

    def _send(self, status, payload):
        body = json.dumps(payload, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path != "/stormrelay/plugin/v1/manifest":
            self._send(404, {"error": "not found"})
            return
        self._send(
            200,
            {
                "plugin_id": "echo-python",
                "version": "0.1.0",
                "protocol_version": PROTOCOL,
                "actions": [
                    {
                        "name": "echo",
                        "description": "Returns the provided input without side effects",
                        "permissions": [],
                    }
                ],
            },
        )

    def do_POST(self):
        if self.path != "/stormrelay/plugin/v1/actions/echo":
            self._send(404, {"error": "unknown action"})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if length < 1 or length > 1024 * 1024:
                raise ValueError("invalid body length")
            request = json.loads(self.rfile.read(length))
            if request.get("protocol_version") != PROTOCOL:
                raise ValueError("protocol mismatch")
            key = request["idempotency_key"]
            self._send(
                200,
                {
                    "protocol_version": PROTOCOL,
                    "status": "succeeded",
                    "idempotency_key": key,
                    "output": {"echo": request.get("input", {})},
                },
            )
        except (ValueError, KeyError, json.JSONDecodeError):
            self._send(400, {"error": "invalid request"})


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", 8090), Handler).serve_forever()
