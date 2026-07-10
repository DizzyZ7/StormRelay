from __future__ import annotations

import dataclasses
import datetime as dt
import hmac
import json
import re
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any, Callable, Mapping

PROTOCOL_VERSION = "stormrelay.plugin/v1"
MAX_REQUEST_BYTES = 1 << 20
MAX_RESPONSE_BYTES = 1 << 20
MAX_ERROR_BYTES = 1000
_IDENTIFIER = re.compile(r"^[a-z][a-z0-9._-]{0,63}$")


@dataclasses.dataclass(frozen=True)
class PluginRequest:
    protocol_version: str
    execution_id: str
    step_id: str
    request_id: str
    traceparent: str
    deadline: dt.datetime
    idempotency_key: str
    input: Any


class ActionError(Exception):
    """A safe action failure whose message may be returned to StormRelay."""


def fail(message: str) -> ActionError:
    cleaned = str(message).strip() or "plugin action failed"
    return ActionError(cleaned[:MAX_ERROR_BYTES])


Handler = Callable[[PluginRequest], Any]


@dataclasses.dataclass(frozen=True)
class Action:
    name: str
    handler: Handler
    description: str = ""
    permissions: tuple[str, ...] = ()


class Plugin:
    def __init__(
        self,
        plugin_id: str,
        version: str,
        actions: list[Action],
        *,
        bearer_token: str | None = None,
    ) -> None:
        self.plugin_id = plugin_id
        self.version = version
        self.actions = {action.name: action for action in actions}
        self.bearer_token = bearer_token.strip() if bearer_token else None
        self._validate(actions)

    def _validate(self, actions: list[Action]) -> None:
        if not _IDENTIFIER.fullmatch(self.plugin_id):
            raise ValueError("plugin_id must be a lowercase protocol identifier")
        if not self.version.strip() or len(self.version) > 100:
            raise ValueError("version must contain 1 to 100 bytes")
        if not actions or len(actions) > 100:
            raise ValueError("plugin must declare between 1 and 100 actions")
        if len(self.actions) != len(actions):
            raise ValueError("action names must be unique")
        for action in actions:
            if not _IDENTIFIER.fullmatch(action.name):
                raise ValueError(f"invalid action name: {action.name!r}")
            if not callable(action.handler):
                raise ValueError(f"action {action.name!r} has no callable handler")
            if len(action.description) > 1000:
                raise ValueError(f"action {action.name!r} description exceeds 1000 bytes")
            if len(action.permissions) > 100 or len(set(action.permissions)) != len(action.permissions):
                raise ValueError(f"action {action.name!r} permissions are invalid")
            for permission in action.permissions:
                if not _IDENTIFIER.fullmatch(permission):
                    raise ValueError(f"invalid permission: {permission!r}")
        if self.bearer_token is not None:
            if not self.bearer_token or len(self.bearer_token) > 4096 or any(c in self.bearer_token for c in "\r\n"):
                raise ValueError("bearer_token must contain 1 to 4096 safe bytes")

    def manifest(self) -> dict[str, Any]:
        return {
            "plugin_id": self.plugin_id,
            "version": self.version,
            "protocol_version": PROTOCOL_VERSION,
            "actions": [
                {
                    "name": action.name,
                    "description": action.description,
                    "permissions": list(action.permissions),
                }
                for action in self.actions.values()
            ],
        }

    def make_handler(self) -> type[BaseHTTPRequestHandler]:
        plugin = self

        class HandlerClass(BaseHTTPRequestHandler):
            server_version = f"stormrelay-plugin/{plugin.version}"

            def log_message(self, _format: str, *_args: object) -> None:
                return

            def _send(self, status: int, payload: Mapping[str, Any]) -> None:
                try:
                    body = json.dumps(payload, separators=(",", ":"), ensure_ascii=False).encode("utf-8")
                except (TypeError, ValueError):
                    status = 500
                    body = b'{"error":"encode response"}'
                if len(body) > MAX_RESPONSE_BYTES:
                    status = 500
                    body = b'{"error":"response exceeds limit"}'
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.send_header("X-Content-Type-Options", "nosniff")
                self.send_header("Cache-Control", "no-store")
                self.end_headers()
                self.wfile.write(body)

            def _authorized(self) -> bool:
                if plugin.bearer_token is None:
                    return True
                header = self.headers.get("Authorization", "")
                if not header.startswith("Bearer "):
                    return False
                presented = header.removeprefix("Bearer ").strip()
                return hmac.compare_digest(presented.encode(), plugin.bearer_token.encode())

            def _require_authorized(self) -> bool:
                if self._authorized():
                    return True
                self.send_response(401)
                self.send_header("WWW-Authenticate", 'Bearer realm="stormrelay-plugin"')
                self.send_header("Content-Type", "application/json")
                body = b'{"error":"valid bearer token required"}'
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
                return False

            def do_GET(self) -> None:
                if not self._require_authorized():
                    return
                if self.path != "/stormrelay/plugin/v1/manifest":
                    self._send(404, {"error": "not found"})
                    return
                self._send(200, plugin.manifest())

            def do_POST(self) -> None:
                if not self._require_authorized():
                    return
                prefix = "/stormrelay/plugin/v1/actions/"
                if not self.path.startswith(prefix):
                    self._send(404, {"error": "not found"})
                    return
                action_name = self.path[len(prefix) :]
                if not action_name or "/" in action_name or "?" in action_name:
                    self._send(404, {"error": "unknown action"})
                    return
                action = plugin.actions.get(action_name)
                if action is None:
                    self._send(404, {"error": "unknown action"})
                    return
                if not self.headers.get("Content-Type", "").lower().startswith("application/json"):
                    self._send(415, {"error": "content type must be application/json"})
                    return
                try:
                    length = int(self.headers.get("Content-Length", "0"))
                except ValueError:
                    self._send(400, {"error": "invalid content length"})
                    return
                if length < 1 or length > MAX_REQUEST_BYTES:
                    self._send(413 if length > MAX_REQUEST_BYTES else 400, {"error": "invalid body length"})
                    return
                raw = self.rfile.read(length)
                if len(raw) != length:
                    self._send(400, {"error": "incomplete request body"})
                    return
                try:
                    document = json.loads(raw)
                    request = _parse_request(document)
                except (ValueError, TypeError, json.JSONDecodeError):
                    self._send(400, {"error": "invalid request body"})
                    return
                try:
                    output = action.handler(request)
                except ActionError as exc:
                    self._send(
                        200,
                        {
                            "protocol_version": PROTOCOL_VERSION,
                            "status": "failed",
                            "idempotency_key": request.idempotency_key,
                            "error": str(exc)[:MAX_ERROR_BYTES],
                        },
                    )
                    return
                except Exception:
                    self._send(
                        200,
                        {
                            "protocol_version": PROTOCOL_VERSION,
                            "status": "failed",
                            "idempotency_key": request.idempotency_key,
                            "error": "plugin action failed",
                        },
                    )
                    return
                response = {
                    "protocol_version": PROTOCOL_VERSION,
                    "status": "succeeded",
                    "idempotency_key": request.idempotency_key,
                    "output": {} if output is None else output,
                }
                try:
                    encoded = json.dumps(response, separators=(",", ":")).encode()
                except (TypeError, ValueError):
                    response = {
                        "protocol_version": PROTOCOL_VERSION,
                        "status": "failed",
                        "idempotency_key": request.idempotency_key,
                        "error": "plugin output is not valid bounded JSON",
                    }
                else:
                    if len(encoded) > MAX_RESPONSE_BYTES:
                        response = {
                            "protocol_version": PROTOCOL_VERSION,
                            "status": "failed",
                            "idempotency_key": request.idempotency_key,
                            "error": "plugin output is not valid bounded JSON",
                        }
                self._send(200, response)

        return HandlerClass

    def serve(self, address: str = "0.0.0.0", port: int = 8090) -> None:
        ThreadingHTTPServer((address, port), self.make_handler()).serve_forever()


_ALLOWED_REQUEST_KEYS = {
    "protocol_version",
    "execution_id",
    "step_id",
    "request_id",
    "traceparent",
    "deadline",
    "idempotency_key",
    "input",
}
_REQUIRED_REQUEST_KEYS = _ALLOWED_REQUEST_KEYS - {"traceparent"}


def _parse_request(document: Any) -> PluginRequest:
    if not isinstance(document, dict):
        raise ValueError("request must be an object")
    keys = set(document)
    if not _REQUIRED_REQUEST_KEYS.issubset(keys) or not keys.issubset(_ALLOWED_REQUEST_KEYS):
        raise ValueError("request keys are invalid")
    if document["protocol_version"] != PROTOCOL_VERSION:
        raise ValueError("protocol mismatch")
    for key in ("execution_id", "step_id", "request_id", "idempotency_key"):
        value = document[key]
        if not isinstance(value, str) or not value.strip() or len(value) > 500:
            raise ValueError(f"invalid {key}")
    traceparent = document.get("traceparent", "")
    if not isinstance(traceparent, str) or len(traceparent) > 200:
        raise ValueError("invalid traceparent")
    deadline = _parse_deadline(document["deadline"])
    now = dt.datetime.now(dt.timezone.utc)
    if deadline <= now or deadline > now + dt.timedelta(hours=24):
        raise ValueError("deadline is outside the accepted window")
    input_value = document["input"]
    json.dumps(input_value, separators=(",", ":"))
    return PluginRequest(
        protocol_version=PROTOCOL_VERSION,
        execution_id=document["execution_id"],
        step_id=document["step_id"],
        request_id=document["request_id"],
        traceparent=traceparent,
        deadline=deadline,
        idempotency_key=document["idempotency_key"],
        input=input_value,
    )


def _parse_deadline(value: Any) -> dt.datetime:
    if not isinstance(value, str):
        raise ValueError("deadline must be a string")
    normalized = value[:-1] + "+00:00" if value.endswith("Z") else value
    deadline = dt.datetime.fromisoformat(normalized)
    if deadline.tzinfo is None:
        raise ValueError("deadline must include timezone")
    return deadline.astimezone(dt.timezone.utc)
