# StormRelay Helm chart

This chart deploys separate StormRelay server and worker Deployments. PostgreSQL and NATS are external by default. Optional bundled dependencies exist only for disposable demo and kind environments.

## Requirements

- Kubernetes 1.29 or newer;
- Helm 4 or a Helm 3 release that supports `apiVersion: v2` charts;
- PostgreSQL and NATS JetStream endpoints for production;
- two existing Kubernetes Secrets: application credentials and, preferably, connection URLs.

## Secrets

The chart never renders a Kubernetes Secret. Create secrets through the platform secret manager, External Secrets Operator, Sealed Secrets, SOPS, or an equivalent workflow.

Application secret keys:

```yaml
secrets:
  existingSecret: stormrelay-app
  masterKeyKey: master-key
  bootstrapAPIKeyKey: bootstrap-api-key
```

Connection URLs can come from independent Secrets:

```yaml
database:
  existingSecret: stormrelay-database
  urlKey: database-url
nats:
  existingSecret: stormrelay-nats
  urlKey: nats-url
```

Example Secret creation is shown only for local testing:

```bash
kubectl create secret generic stormrelay-app \
  --from-literal=master-key='<base64-encoded-32-byte-key>' \
  --from-literal=bootstrap-api-key='<random-bootstrap-key>'
```

Do not store those values in `values.yaml`, Git, Helm release notes, or CI logs.

## Install with external dependencies

```bash
helm upgrade --install stormrelay deploy/helm/stormrelay \
  --namespace stormrelay --create-namespace \
  --set secrets.existingSecret=stormrelay-app \
  --set database.existingSecret=stormrelay-database \
  --set nats.existingSecret=stormrelay-nats \
  --set config.publicBaseURL=https://stormrelay.example.com
```

The default images are `ghcr.io/dizzyz7/stormrelay-server` and `ghcr.io/dizzyz7/stormrelay-worker`. Before the first tagged release, override repository and tag with images built from the same commit.

## Network policy

NetworkPolicy is enabled by default. It permits:

- ingress from pods in the release namespace;
- DNS to kube-dns;
- egress to pods in the same namespace.

External PostgreSQL, NATS, OTLP collectors, notification providers, plugins, runbook HTTP targets, and ingress controllers require explicit policy rules. Example external CIDR rule:

```yaml
networkPolicy:
  additionalEgress:
    - to:
        - ipBlock:
            cidr: 10.20.0.0/16
      ports:
        - protocol: TCP
          port: 5432
```

Ingress controllers in another namespace require `ingressNamespaceSelector` and optionally `ingressPodSelector`. Keep selectors narrow.

## Security defaults

Server and worker containers:

- run as UID/GID 65532;
- require non-root execution;
- use RuntimeDefault seccomp;
- drop every Linux capability;
- forbid privilege escalation and privileged mode;
- use a read-only root filesystem;
- do not mount a service-account token by default;
- have startup, readiness, and liveness probes;
- declare resource requests and limits.

The server and worker have independent PodDisruptionBudgets. Default replica counts are two, allowing `minAvailable: 1` during voluntary disruption.

## Demo dependencies

`demoDependencies.enabled=true` creates one PostgreSQL StatefulSet and one NATS JetStream StatefulSet. It is for kind and local demonstrations only. It does not provide production HA, backups, authentication hardening, TLS, topology spreading, or operational ownership.

Demo credentials still come from an existing Secret. The chart never generates them.

## Upgrades

StormRelay migrations are forward-only and protected by a PostgreSQL advisory lock. Both workloads currently start with automatic migration enabled so one replica applies pending migrations while others wait. Before an upgrade:

1. back up PostgreSQL and the master key separately;
2. review the compatibility and upgrade guides;
3. confirm the new chart and application versions are compatible;
4. monitor readiness, JetStream lag, duplicate rate, notification failures, and runbook ambiguity;
5. roll forward or restore a compatible recovery point rather than running destructive down migrations.

## Validate

```bash
helm lint deploy/helm/stormrelay \
  --set secrets.existingSecret=stormrelay-app \
  --set database.url='postgres://user:password@postgres:5432/stormrelay?sslmode=require' \
  --set nats.url='nats://nats:4222'

bash deploy/kind/smoke.sh
```

The kind smoke builds server and worker images, validates production and demo rendering, performs a server-side dry-run, installs the chart, sends a signed event, observes the incident, checks pod security settings, performs an idempotent upgrade, and uninstalls the release.
