# Kubernetes deployment

StormRelay ships a Helm chart at `deploy/helm/stormrelay`. The chart deploys separate stateless server and worker workloads. PostgreSQL remains the system of record and NATS JetStream remains the durable at-least-once transport.

## Production topology

Use externally operated PostgreSQL and NATS for production. The chart's bundled StatefulSets are intentionally single-node demo dependencies and do not provide HA, backup automation, TLS, hardened authentication, topology awareness, or production upgrade ownership.

Recommended production components:

- at least two server replicas;
- at least two worker replicas;
- HA PostgreSQL with tested backup/restore and the separately retained StormRelay master key;
- HA NATS JetStream with storage and recovery objectives coordinated with PostgreSQL;
- an external OpenTelemetry Collector;
- an ingress controller with TLS termination;
- an external secret-management controller;
- a network-policy implementation and explicit egress rules.

## Secrets

The chart does not create Kubernetes Secret objects. Supply:

- `STORMRELAY_MASTER_KEY` and the bootstrap API key through `secrets.existingSecret`;
- PostgreSQL URL through `database.existingSecret` or, for non-production experiments, `database.url`;
- NATS URL through `nats.existingSecret` or `nats.url`.

Prefer External Secrets Operator, SOPS, Sealed Secrets, workload identity, or the platform's approved secret-management workflow. Plaintext values in Helm release history are not an acceptable production secret store.

Back up the master key separately from PostgreSQL. A database backup without the matching key preserves ciphertext but cannot recover encrypted source, plugin, OIDC, and acknowledgement credentials.

## Network policy

The default NetworkPolicy permits DNS and same-namespace pod traffic only. This supports demo dependencies while remaining fail-closed for external destinations.

Production values must explicitly allow:

- PostgreSQL;
- NATS;
- the OTLP collector;
- notification providers;
- approved plugin endpoints;
- approved runbook HTTP targets;
- ingress-controller traffic when it runs outside the release namespace.

Use narrow namespace/pod selectors or CIDR and port rules. Do not use unrestricted `0.0.0.0/0` egress merely to make an integration work.

## Probes and disruption

Server and worker use:

- startup probe: `/healthz`;
- readiness probe: `/readyz`, including PostgreSQL, JetStream, and migration compatibility;
- liveness probe: `/healthz`.

The default replica count is two for each workload. Independent PodDisruptionBudgets keep at least one replica available during voluntary disruption. Review PDBs against node count, maintenance windows, autoscaling, and cluster-upgrade policy.

Readiness failure removes a pod from service but does not replace data recovery or queue monitoring. During PostgreSQL or NATS incidents, inspect JetStream lag, DLQ state, duplicate counters, notification ambiguity, and runbook leases.

## Migrations and upgrades

Migrations are forward-only and guarded by a PostgreSQL advisory lock. Server and worker currently start with automatic migration enabled; one replica applies pending migrations while the others wait.

Before upgrade:

1. create and verify a PostgreSQL backup;
2. verify separate master-key recovery;
3. review release notes, compatibility matrix, and `docs/upgrade-guide.md`;
4. render and server-side validate the new chart;
5. deploy during an observed window;
6. verify readiness, migration version, event ingestion, JetStream lag, notification delivery, and runbook recovery;
7. roll forward or restore a compatible recovery point rather than running destructive down migrations.

## Ingress and public URL

Enable `ingress.enabled` only after configuring the ingress controller, TLS, body-size limits, timeout policy, and NetworkPolicy selectors. `config.publicBaseURL` must match the externally reachable HTTPS origin because acknowledgement links use it.

Do not enable reverse-proxy request-body logging on webhook endpoints. Preserve `traceparent` and request IDs without logging authorization, signatures, tokens, secret URLs, or raw payloads.

## Validate locally

```bash
make helm-lint
make kind-smoke
```

The kind smoke validates external and demo rendering, performs Kubernetes server-side dry-run, installs the chart, asserts application security contexts, submits a signed webhook, observes the correlated incident, performs an idempotent upgrade, and uninstalls the release.
