# Failure injection and recovery tests

StormRelay uses at-least-once delivery. The failure suite verifies concrete persistence and acknowledgement boundaries instead of claiming exactly-once behavior.

Run the complete local foundation suite from the repository root:

```bash
make failure
```

The command creates isolated Docker Compose projects, runs deterministic adapter and state-recovery tests, stops PostgreSQL and NATS at controlled points, and removes all test volumes afterward. It does not contact Telegram, a real plugin, or any external SaaS provider.

## Confirmed acceptance boundary

An ingress response with HTTP `202 Accepted` means JetStream returned a publish acknowledgement for the event. It does not mean PostgreSQL correlation has completed yet.

An ingress response with HTTP `503 Service Unavailable` means StormRelay did not confirm durable JetStream acceptance. An HMAC replay reservation is therefore released, and the identical signed request may be retried after NATS recovers. A successful `202` keeps the replay reservation until its configured expiry.

A worker acknowledges a JetStream event only after the PostgreSQL transaction containing raw-event storage, normalization, deduplication, correlation, policy audit, notification enqueueing, and event/incident audit has committed. A database failure causes a negative acknowledgement and delayed redelivery.

## Scenarios

### PostgreSQL unavailable during event processing

`tests/failure/dependency-outages.sh` pauses the worker, publishes an event and observes HTTP `202`, stops PostgreSQL, resumes the worker, then restores PostgreSQL. The test requires exactly one normalized event and exactly one incident-event link after redelivery.

Expected behavior:

- the confirmed JetStream message is not lost;
- the worker does not acknowledge the message while PostgreSQL processing fails;
- processing resumes after PostgreSQL recovery;
- deduplication prevents multiple canonical records.

### NATS unavailable during ingress and worker processing

The dependency drill first stops NATS before ingress. The request must return `503`; after NATS restarts, the exact same HMAC timestamp, signature, event ID, and idempotency key must return `202` and be processed once.

The drill then confirms another event while the worker is paused, stops NATS, resumes the worker, restarts NATS, and verifies the durable consumer processes the preserved message.

Expected behavior:

- failed publish acknowledgement does not poison replay protection;
- confirmed messages survive a NATS process restart because the test retains the file-backed JetStream volume;
- reconnect and redelivery do not create duplicate canonical records.

### Parallel duplicate delivery

`TestConcurrentDuplicateProcessingCreatesOneCanonicalEvent` submits eight identical normalized events concurrently.

The invariant is:

- eight raw deliveries are retained;
- one normalized event is canonical;
- seven `event_duplicates` rows are stored;
- seven `event.duplicate` audit entries are appended;
- one incident is correlated.

### Poison message

`TestPoisonMessageReachesDLQWithSanitizedMetadata` publishes malformed JSON directly to a dedicated JetStream subject. Retry delays use capped exponential backoff with deterministic jitter. After the configured delivery limit, the original bytes are published to `<subject>.dlq`, the original message is acknowledged, and bounded failure metadata records the delivery count.

The test additionally proves that the failure header does not contain the payload marker. Raw poison bytes are preserved only in the DLQ message body and are not copied into logs or failure headers.

### Worker crash during a runbook step

`TestExpiredRunbookLeaseRequiresIdempotencyForAutomaticReplay` models a crashed worker by leaving a claimed step in `running` state and expiring its PostgreSQL lease from another connection.

Expected behavior:

- non-idempotent HTTP action: step and execution become `ambiguous`; no automatic replay occurs;
- explicitly idempotent HTTP action: the step becomes `retrying`, is claimed again with the same step ID and idempotency key, and increments the attempt counter;
- both decisions append `execution_step.lease_expired` audit records.

This is the intentional fail-closed boundary for unknown remote outcomes.

### Control-plane restart between steps

`TestPersistedWaitContinuesAfterControlPlaneRestart` schedules a wait through one storage/process instance and advances it through a newly opened instance with no shared in-memory state. The following approval step must be claimable after restart.

The test demonstrates that wait state, execution progress, and the next runnable step are PostgreSQL-backed rather than process-local.

### Plugin timeout and malformed response

Plugin tests inject a local HTTP transport beneath the production protocol parser. They verify bounded request timeout, malformed JSON rejection, and fail-closed idempotency-key mismatch handling. No plugin code runs in the control-plane process.

### Notification provider HTTP 500 and malformed response

Telegram adapter tests inject a local HTTP transport and return provider HTTP `500` or malformed `200` JSON. Both outcomes are delivery failures and the bot token must not appear in the returned error.

## Retry configuration

Event redelivery settings are:

- `STORMRELAY_EVENT_MAX_DELIVERIES` — total attempts before DLQ, default `5`, allowed `2..100`;
- `STORMRELAY_EVENT_RETRY_BASE_DELAY` — first retry delay, default `1s`;
- `STORMRELAY_EVENT_RETRY_MAX_DELAY` — cap for exponential backoff, default `30s`.

Retry jitter is deterministic for a given payload and delivery number, bounded to approximately ±20 percent, and capped by the maximum delay. Determinism keeps tests reproducible; payload-derived variation prevents unrelated failed events from retrying in lockstep.

## CI

`.github/workflows/failure-injection.yml` is a dedicated, path-scoped workflow. It runs:

- plugin and notification adapter failures;
- concurrent duplicate invariants;
- poison-message and runbook state recovery tests;
- the destructive PostgreSQL/NATS Compose outage drill.

Diagnostics retain only bounded test output and service logs for one day. Test code must not print HMAC source credentials, bot tokens, raw webhook bodies, database dumps, or complete sensitive payloads.
