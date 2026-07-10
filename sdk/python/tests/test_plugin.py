from __future__ import annotations

import datetime as dt
import json
import threading
import unittest
import urllib.error
import urllib.request
from http.server import ThreadingHTTPServer

from stormrelay_plugin import Action, Plugin, fail


class PluginServerTest(unittest.TestCase):
    def setUp(self) -> None:
        self.plugin = Plugin(
            "echo-python",
            "1.0.0",
            [
                Action(
                    "echo",
                    lambda request: {"echo": request.input},
                    description="Echoes input",
                ),
                Action("fail", lambda _request: (_ for _ in ()).throw(fail("safe failure"))),
            ],
            bearer_token="secret-token",
        )
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), self.plugin.make_handler())
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.base_url = f"http://127.0.0.1:{self.server.server_port}"

    def tearDown(self) -> None:
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)

    def request(self, method: str, path: str, body: object | None = None, token: str = "secret-token") -> tuple[int, dict]:
        data = None if body is None else json.dumps(body).encode()
        request = urllib.request.Request(self.base_url + path, method=method, data=data)
        request.add_header("Authorization", f"Bearer {token}")
        if data is not None:
            request.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(request, timeout=3) as response:
                return response.status, json.loads(response.read())
        except urllib.error.HTTPError as exc:
            return exc.code, json.loads(exc.read())

    def test_manifest_and_success(self) -> None:
        status, manifest = self.request("GET", "/stormrelay/plugin/v1/manifest")
        self.assertEqual(status, 200)
        self.assertEqual(manifest["protocol_version"], "stormrelay.plugin/v1")
        self.assertEqual(manifest["plugin_id"], "echo-python")
        self.assertEqual([item["name"] for item in manifest["actions"]], ["echo", "fail"])

        status, response = self.request(
            "POST",
            "/stormrelay/plugin/v1/actions/echo",
            valid_request({"hello": "world"}),
        )
        self.assertEqual(status, 200)
        self.assertEqual(response["status"], "succeeded")
        self.assertEqual(response["idempotency_key"], "idem")
        self.assertEqual(response["output"]["echo"]["hello"], "world")

    def test_auth_strict_json_and_safe_failure(self) -> None:
        status, _ = self.request("GET", "/stormrelay/plugin/v1/manifest", token="wrong")
        self.assertEqual(status, 401)

        request = valid_request({})
        request["unexpected"] = True
        status, _ = self.request("POST", "/stormrelay/plugin/v1/actions/echo", request)
        self.assertEqual(status, 400)

        status, response = self.request("POST", "/stormrelay/plugin/v1/actions/fail", valid_request({}))
        self.assertEqual(status, 200)
        self.assertEqual(response["status"], "failed")
        self.assertEqual(response["error"], "safe failure")

    def test_expired_deadline_and_unknown_action(self) -> None:
        request = valid_request({})
        request["deadline"] = (dt.datetime.now(dt.timezone.utc) - dt.timedelta(seconds=1)).isoformat()
        status, _ = self.request("POST", "/stormrelay/plugin/v1/actions/echo", request)
        self.assertEqual(status, 400)

        status, _ = self.request("POST", "/stormrelay/plugin/v1/actions/missing", valid_request({}))
        self.assertEqual(status, 404)

    def test_validation(self) -> None:
        with self.assertRaises(ValueError):
            Plugin("Bad ID", "1", [Action("echo", lambda request: request.input)])
        with self.assertRaises(ValueError):
            Plugin(
                "valid",
                "1",
                [Action("echo", lambda request: request.input), Action("echo", lambda request: request.input)],
            )


def valid_request(input_value: object) -> dict:
    return {
        "protocol_version": "stormrelay.plugin/v1",
        "execution_id": "execution",
        "step_id": "step",
        "request_id": "request",
        "traceparent": "",
        "deadline": (dt.datetime.now(dt.timezone.utc) + dt.timedelta(minutes=5)).isoformat(),
        "idempotency_key": "idem",
        "input": input_value,
    }


if __name__ == "__main__":
    unittest.main()
