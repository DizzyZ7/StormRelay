import io
import json
import unittest
import urllib.error
from unittest.mock import patch

from stormrelay import APIError, Client


class FakeResponse:
    def __init__(self, payload):
        self._body = io.BytesIO(json.dumps(payload).encode())

    def read(self, size=-1):
        return self._body.read(size)

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, tb):
        return False


class ClientTests(unittest.TestCase):
    @patch("urllib.request.urlopen")
    def test_version_sends_bearer(self, urlopen):
        urlopen.return_value = FakeResponse({"version": "dev", "api": "v1", "event_schema": "1.0"})
        client = Client("https://stormrelay.example", "secret")
        result = client.version()
        self.assertEqual("v1", result["api"])
        request = urlopen.call_args.args[0]
        self.assertEqual("Bearer secret", request.get_header("Authorization"))
        self.assertEqual("https://stormrelay.example/api/v1/version", request.full_url)

    @patch("urllib.request.urlopen")
    def test_error_envelope(self, urlopen):
        body = json.dumps({"error": {"code": "forbidden", "message": "permission denied", "request_id": "req-1"}}).encode()
        urlopen.side_effect = urllib.error.HTTPError("https://stormrelay.example", 403, "Forbidden", {}, io.BytesIO(body))
        client = Client("https://stormrelay.example", "secret")
        with self.assertRaises(APIError) as captured:
            client.get_incident("incident")
        self.assertEqual(403, captured.exception.status_code)
        self.assertEqual("forbidden", captured.exception.code)
        self.assertEqual("req-1", captured.exception.request_id)

    def test_rejects_unsafe_configuration(self):
        for base_url, key in [
            ("localhost:8080", "secret"),
            ("ftp://example.com", "secret"),
            ("https://user:pass@example.com", "secret"),
            ("https://example.com", ""),
            ("https://example.com", "bad\nkey"),
        ]:
            with self.assertRaises(ValueError):
                Client(base_url, key)


if __name__ == "__main__":
    unittest.main()
