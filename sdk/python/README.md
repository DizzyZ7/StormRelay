# StormRelay Python SDK

The Python client uses only the standard library and supports Python 3.11+.

```python
from stormrelay import Client

client = Client("https://stormrelay.example", api_key)
for incident in client.list_incidents(state="detected")["items"]:
    print(incident["id"], incident["title"])
```

`APIError` preserves HTTP status, stable error code, request ID, and bounded details. The client performs no automatic retries.
