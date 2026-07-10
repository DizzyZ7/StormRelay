# Alert runbooks

These runbooks correspond to the Prometheus rules in `deploy/compose/alerts.yml`. Thresholds are safe development defaults, not universal capacity targets. Tune sustained-load thresholds only after collecting a representative baseline.

Never resolve an alert by deleting audit, deduplication, execution, or delivery records. Preserve evidence and correct the underlying availability, capacity, configuration, or integration failure.

## StormRelayTargetDown

**Impact:** Prometheus cannot scrape the API server or worker. The affected process may be unavailable, stuck, partitioned, or repeatedly restarting.

1. Check `/healthz` and `/readyz` directly from the same network as Prometheus.
2. Inspect process/container status, restart count, exit reason, and recent structured logs.
3. If readiness fails, identify whether PostgreSQL, NATS JetStream, or migration-version validation is failing.
4. Confirm DNS, service discovery, firewall, and scrape-target configuration.
5. Restore the dependency first, then restart only the affected StormRelay process if it does not recover.

**Close when:** the target has remained scrapeable and ready, restart loops have stopped, and queued work is draining normally.

## StormRelayEventPipelineStalled

**Impact:** events are entering the API, but the worker is completing none. Incident creation and automation may be delayed.

1. Check worker readiness and `stormrelay_jetstream_consumer_lag`.
2. Inspect worker logs by trace ID for PostgreSQL, decoding, lease, acknowledgement, or consumer errors.
3. Check JetStream consumer state and the `<subject>.dlq` subject.
4. Confirm PostgreSQL accepts writes and its connection pool is not saturated.
5. Fix poison events or dependency failures before replaying DLQ messages. Preserve the original idempotency key unless a deliberate new operation is required.

**Close when:** processing-count metrics resume, consumer lag trends downward, and no new poison messages are entering the DLQ.

## StormRelayJetStreamBacklogHigh

**Impact:** the durable event backlog is growing faster than workers can process it. Incident creation latency will increase.

1. Compare ingress rate with processing throughput and event-processing latency.
2. Check worker replicas, `STORMRELAY_WORKER_CONCURRENCY`, CPU, memory, and PostgreSQL pool utilization.
3. Inspect logs for repeated retries or a single poison-event pattern.
4. Scale worker capacity only after confirming PostgreSQL and downstream integrations can absorb the increase.
5. Do not purge the stream to clear the alert. Purging discards unprocessed operational evidence.

**Close when:** lag decreases consistently and processing latency returns to the deployment baseline.

## StormRelayDatabasePoolSaturated

**Impact:** more than 85% of available PostgreSQL connections are acquired for a sustained period. Requests, event processing, and runbook leases may block.

1. Identify whether the server, worker, or both are saturated.
2. Inspect database activity for long-running, blocked, or idle-in-transaction queries.
3. Check database CPU, I/O, lock waits, and maximum connection limits.
4. Resolve slow/blocked queries before increasing pool size.
5. If raising connection limits, keep total pools across all replicas below PostgreSQL capacity with headroom for administration and migrations.

**Close when:** utilization stays below the warning threshold and request/processing latency has recovered.

## StormRelayOpenIncidentLoadHigh

**Impact:** the open incident queue is above the default operating threshold for at least fifteen minutes. Responders may be overloaded or automatic closure/acknowledgement paths may be failing.

1. Break down incidents by service, environment, source, severity, and age through the API or UI tooling.
2. Check for a correlation-key change that fragmented one problem into many incidents.
3. Verify notification delivery, acknowledgement links, and responder ownership.
4. Resolve root causes in source systems; do not bulk-close incidents solely to clear the metric.
5. Tune the threshold only after establishing the normal workload and staffing model.

**Close when:** aged incidents have owners, the queue is declining, and correlation behavior is understood.

## StormRelayEventRejectionRatioHigh

**Impact:** more than 10% of attempted ingress is rejected. Monitoring or security sources may be losing coverage.

1. Inspect API logs and response error codes for authentication, signature, timestamp, replay, rate-limit, content-type, or payload-limit failures.
2. Verify source credentials and sender clock synchronization.
3. Confirm senders are using unique delivery/idempotency identifiers and supported payload formats.
4. Check whether a deployment changed source configuration or request limits.
5. Rotate credentials only through an explicit coordinated procedure; never log or paste secrets into incident comments.

**Close when:** rejection ratio returns to baseline and affected sources successfully deliver representative test events.

## StormRelayRunbookFailuresDetected

**Impact:** at least one durable automation execution failed. The requested response may be incomplete.

1. Read the execution and step states, including stored attempts, error class, and rollback results.
2. Distinguish a confirmed failure from an `ambiguous` non-idempotent outcome.
3. For ambiguous actions, verify the remote system before any retry.
4. Retry only idempotent operations or use the explicit forced retry path after human verification.
5. Repair plugin/HTTP credentials, allowlists, input, or dependency availability before resuming execution.

**Close when:** the failed execution has an explicit operator disposition and no unresolved ambiguous external effect remains.

## StormRelayPluginFailuresDetected

**Impact:** a process-plugin action failed, timed out, or violated the protocol. Runbook steps using that plugin may fail.

1. Check plugin health, manifest discovery, protocol version, and declared action name.
2. Run `stormrelay-plugin-conformance` against the same deployed image and network path.
3. Verify exact-host allowlists, DNS, TLS, bearer credentials, deadline handling, and response-size limits.
4. Inspect plugin logs using request and idempotency identifiers without logging secrets or full payloads.
5. Roll back the plugin image if a new release broke compatibility.

**Close when:** conformance succeeds, a representative action completes, and failure counters stop increasing.

## StormRelayNotificationFailuresDetected

**Impact:** responders may not receive incident notifications even though incidents continue to be created.

1. Inspect outbox/delivery state, attempt count, provider response class, and lease timestamps.
2. Verify provider availability, credentials, destination identifiers, and network egress.
3. Distinguish a confirmed failure from an ambiguous provider result.
4. For ambiguous delivery, check the provider before retrying to avoid duplicate notifications.
5. Restore provider access and allow the durable outbox retry policy to drain pending deliveries.

**Close when:** representative notifications arrive, pending deliveries drain, and failure counters remain stable.
