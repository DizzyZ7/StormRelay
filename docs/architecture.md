# Architecture

## Product definition

StormRelay is an incident control plane between unreliable event producers and human or automated response. Its first production-oriented slice focuses on preserving evidence, deterministic deduplication, correlation, acknowledgement, and audit rather than broad integration count.

## Component boundaries

- **Ingestion gateway:** validates request size, authentication, timestamp, replay cache, rate limit, content type, and event shape. It publishes a normalized envelope to JetStream and does not write incidents directly.
- **JetStream:** durable at-least-once handoff and backpressure boundary. The server waits for a publish acknowledgement.
- **Worker:** bounded pull consumer. It commits raw event, normalized event, deduplication result, incident correlation, policy decisions, notification outbox, and audit before acknowledging the message.
- **PostgreSQL:** source of truth. SQL remains explicit through pgx.
- **Notification dispatcher:** claims leased outbox rows with `FOR UPDATE SKIP LOCKED` and calls isolated adapters.
- **Runbook engine:** claims immutable execution-step snapshots with expiring PostgreSQL leases. Persisted waits and approvals do not occupy goroutines; uncertain non-idempotent outcomes become `ambiguous`.
- **Process plugins:** run outside the control-plane address space behind a versioned JSON protocol, exact host allowlists, DNS/IP validation, deadlines, bounded payloads, and idempotency-key verification.
- **API/CLI:** operational control surface. UI work is deferred and may never become a required dependency.

## Event processing sequence

```mermaid
sequenceDiagram
    participant Source
    participant API
    participant JS as JetStream
    participant Worker
    participant PG as PostgreSQL
    participant Notify as Provider

    Source->>API: signed webhook
    API->>API: size, rate, auth, replay, normalize
    API->>JS: publish(event, Nats-Msg-Id)
    JS-->>API: publish acknowledgement
    API-->>Source: 202 Accepted
    JS->>Worker: deliver event
    Worker->>PG: begin transaction
    Worker->>PG: insert raw event
    Worker->>PG: insert canonical or duplicate
    Worker->>PG: advisory lock correlation key
    Worker->>PG: incident + policy audit + outbox
    Worker->>PG: commit
    Worker-->>JS: explicit ack
    Worker->>PG: claim delivery lease
    Worker->>Notify: sanitized notification
    Worker->>PG: delivered / failed / ambiguous
```

## Incident state machine

```mermaid
stateDiagram-v2
    [*] --> detected
    detected --> acknowledged
    detected --> investigating
    detected --> mitigated
    detected --> resolved
    acknowledged --> investigating
    acknowledged --> mitigated
    acknowledged --> resolved
    investigating --> mitigated
    investigating --> resolved
    mitigated --> investigating
    mitigated --> resolved
    resolved --> closed
    resolved --> reopened
    closed --> reopened
    reopened --> acknowledged
    reopened --> investigating
    reopened --> mitigated
    reopened --> resolved
```

All transitions pass through domain validation and create both an `incident_transitions` row and an append-only audit entry.

## Data model summary

Identity tables define tenants, users, teams, memberships, and API keys. Event tables separate raw bytes, normalized fields, and duplicate observations. Incident tables link canonical events and transitions. Policy and runbook definitions are immutable versions behind mutable active-version pointers. Executions pin a concrete runbook version and store immutable per-step snapshots, retry policy, timeout, lease state, output, sanitized error, and idempotency key. Notifications use durable delivery attempts. Audit entries cannot be updated or deleted through the application role because a database trigger rejects mutation.

## Consistency model

StormRelay deliberately uses at-least-once transport. The idempotency hierarchy is explicit key, source event ID, then fingerprint. The unique tuple is tenant, source, dedupe key, and time bucket. Correlation locks only one logical key, allowing unrelated services to continue if one key is hot.

External providers are outside the transaction. Their outcome can be ambiguous. The outbox records that ambiguity rather than inventing exactly-once semantics.

## Backpressure

The gateway receives backpressure from JetStream publish failures. The worker limits pull batch, outstanding acknowledgements, and local goroutines. PostgreSQL pool size is bounded. Poison messages have a finite delivery count and a dead-letter path.

## Deferred boundaries

OIDC, tenant-aware RBAC, web UI, OpenTelemetry SDK exporters, Helm, signing, and release provenance remain separate milestones. Generic shell execution is intentionally unavailable.
