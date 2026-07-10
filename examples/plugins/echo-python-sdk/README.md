# Echo plugin using the Python SDK

This example implements `stormrelay.plugin/v1` through the supported standard-library Python SDK. It declares two actions:

- `echo`: returns arbitrary JSON input under `output.echo`.
- `require-message`: accepts an object with a string `message` field and returns a safe validation failure otherwise.

## Run locally

From the repository root:

```bash
PYTHONPATH=sdk/python python3 examples/plugins/echo-python-sdk/plugin.py
```

The plugin listens on port `8090` by default. Set `PORT` to change it. Set `PLUGIN_BEARER_TOKEN` to require bearer authentication; do not place real values in source control or shell history.

## Build the container

The Docker build context must be the repository root so the SDK package can be copied into the image:

```bash
docker build \
  -f examples/plugins/echo-python-sdk/Dockerfile \
  -t stormrelay-echo-python-sdk .
```

## Run conformance

Place the plugin and runner on the same Docker network:

```bash
docker network create stormrelay-plugin-test

docker run -d --rm \
  --name echo-plugin \
  --network stormrelay-plugin-test \
  stormrelay-echo-python-sdk

docker build \
  -f deploy/plugin-conformance/Dockerfile \
  -t stormrelay-plugin-conformance .

docker run --rm \
  --network stormrelay-plugin-test \
  stormrelay-plugin-conformance \
  --endpoint http://echo-plugin:8090 \
  --action echo \
  --input '{"hello":"world"}'
```

The repository runs the same live scenario in the `Plugin Conformance` GitHub Actions workflow.
