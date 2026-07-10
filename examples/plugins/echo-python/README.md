# Echo Python plugin

A side-effect-free process plugin implementing `stormrelay.plugin/v1`. Build and run it on an isolated network, add its DNS hostname to `STORMRELAY_PLUGIN_ALLOWED_HOSTS`, register the base endpoint with `POST /api/v1/plugins`, and verify it with `stormrelay plugins test PLUGIN_KEY --action echo`.

The example accepts no credentials, performs no outbound calls, echoes the request idempotency key, and is included in the Docker Compose smoke path.
