from __future__ import annotations

import json
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from typing import Any, Mapping

_MAX_RESPONSE_BYTES = 8 * 1024 * 1024


@dataclass(slots=True)
class APIError(Exception):
    status_code: int
    code: str
    message: str
    request_id: str = ""
    details: Any = None

    def __str__(self) -> str:
        suffix = f" (request_id={self.request_id})" if self.request_id else ""
        return f"stormrelay API {self.status_code} {self.code}: {self.message}{suffix}"


class Client:
    def __init__(self, base_url: str, api_key: str, *, timeout: float = 30.0) -> None:
        parsed = urllib.parse.urlsplit(base_url.strip())
        if parsed.scheme not in {"http", "https"} or not parsed.netloc:
            raise ValueError("base_url must be an absolute HTTP(S) URL")
        if parsed.username or parsed.password or parsed.query or parsed.fragment:
            raise ValueError("base_url must not contain userinfo, query, or fragment")
        api_key = api_key.strip()
        if not api_key or "\r" in api_key or "\n" in api_key:
            raise ValueError("api_key is required")
        if timeout <= 0:
            raise ValueError("timeout must be positive")
        self._base_url = urllib.parse.urlunsplit((parsed.scheme, parsed.netloc, parsed.path.rstrip("/"), "", ""))
        self._api_key = api_key
        self._timeout = timeout

    def version(self) -> dict[str, Any]:
        return self._request("GET", "/api/v1/version")

    def list_incidents(self, **filters: Any) -> dict[str, Any]:
        query = urllib.parse.urlencode({key: value for key, value in filters.items() if value not in (None, "")})
        suffix = f"?{query}" if query else ""
        return self._request("GET", f"/api/v1/incidents{suffix}")

    def get_incident(self, incident_id: str) -> dict[str, Any]:
        return self._request("GET", f"/api/v1/incidents/{urllib.parse.quote(incident_id, safe='')}")

    def transition_incident(self, incident_id: str, action: str, version: int, reason: str = "") -> dict[str, Any]:
        if action not in {"ack", "resolve"}:
            raise ValueError("action must be ack or resolve")
        return self._request("POST", f"/api/v1/incidents/{urllib.parse.quote(incident_id, safe='')}/{action}", {"version": version, "reason": reason})

    def apply_runbook(self, document: str | bytes) -> dict[str, Any]:
        raw = document.encode() if isinstance(document, str) else document
        return self._request("POST", "/api/v1/runbooks", raw, content_type="application/yaml")

    def start_execution(self, runbook_key: str, *, incident_id: str = "", dry_run: bool = False, parameters: Mapping[str, Any] | None = None) -> dict[str, Any]:
        return self._request("POST", f"/api/v1/runbooks/{urllib.parse.quote(runbook_key, safe='')}/run", {"incident_id": incident_id, "dry_run": dry_run, "parameters": dict(parameters or {})})

    def get_execution(self, execution_id: str) -> dict[str, Any]:
        return self._request("GET", f"/api/v1/executions/{urllib.parse.quote(execution_id, safe='')}")

    def list_approvals(self, *, execution_id: str = "", status: str = "") -> dict[str, Any]:
        query = urllib.parse.urlencode({"execution_id": execution_id, "status": status})
        return self._request("GET", f"/api/v1/approvals?{query}")

    def decide_approval(self, approval_id: str, decision: str, reason: str = "") -> dict[str, Any]:
        if decision not in {"approve", "reject"}:
            raise ValueError("decision must be approve or reject")
        return self._request("POST", f"/api/v1/approvals/{urllib.parse.quote(approval_id, safe='')}/{decision}", {"reason": reason})

    def create_service_account(self, name: str, roles: list[str]) -> dict[str, Any]:
        return self._request("POST", "/api/v1/service-accounts", {"name": name, "roles": roles})

    def create_service_account_key(self, account_id: str, *, expires_at: str | None = None) -> dict[str, Any]:
        return self._request("POST", f"/api/v1/service-accounts/{urllib.parse.quote(account_id, safe='')}/keys", {"expires_at": expires_at})

    def revoke_service_account_key(self, key_id: str) -> dict[str, Any]:
        return self._request("POST", f"/api/v1/service-account-keys/{urllib.parse.quote(key_id, safe='')}/revoke", {})

    def _request(self, method: str, path: str, body: Any = None, *, content_type: str = "application/json") -> dict[str, Any]:
        if body is None:
            data = None
        elif isinstance(body, bytes):
            data = body
        else:
            data = json.dumps(body, separators=(",", ":")).encode()
        request = urllib.request.Request(
            self._base_url + path,
            data=data,
            method=method,
            headers={
                "Authorization": f"Bearer {self._api_key}",
                "Accept": "application/json",
                "Content-Type": content_type,
                "User-Agent": "stormrelay-python/dev",
            },
        )
        try:
            with urllib.request.urlopen(request, timeout=self._timeout) as response:
                payload = _read_bounded(response)
                return json.loads(payload) if payload else {}
        except urllib.error.HTTPError as exc:
            payload = _read_bounded(exc)
            raise _decode_error(exc.code, payload) from None


def _read_bounded(response: Any) -> bytes:
    payload = response.read(_MAX_RESPONSE_BYTES + 1)
    if len(payload) > _MAX_RESPONSE_BYTES:
        raise ValueError(f"stormrelay response exceeds {_MAX_RESPONSE_BYTES} bytes")
    return payload


def _decode_error(status: int, payload: bytes) -> APIError:
    try:
        envelope = json.loads(payload)
        value = envelope["error"]
        return APIError(status, value.get("code", "http_error"), value.get("message", "HTTP error"), value.get("request_id", ""), value.get("details"))
    except (ValueError, KeyError, TypeError):
        message = payload.decode(errors="replace").strip() or "HTTP error"
        return APIError(status, "http_error", message)
