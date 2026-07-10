# Runbook DSL

StormRelay runbooks are immutable, versioned YAML documents. Version 1 is deliberately small and declarative: no templates, shell evaluation, JavaScript, CEL, or arbitrary expressions are executed.

```yaml
apiVersion: stormrelay.io/v1
kind: Runbook
metadata:
  id: checkout-latency
  version: 1
  name: Checkout latency response
spec:
  steps:
    - id: notify-owner
      type: http
      timeout: 10s
      retry:
        maxAttempts: 3
        initialBackoff: 1s
        maxBackoff: 10s
      http:
        method: POST
        url: https://automation.example.net/v1/notify
        idempotent: true
        body:
          incident: checkout-latency
    - id: wait-for-drain
      type: wait
      wait:
        duration: 30s
    - id: approve-mitigation
      type: approval
      approval:
        prompt: Apply the registered mitigation?
        expiresAfter: 30m
    - id: mitigate
      type: plugin
      when:
        field: execution.dry_run
        equals: false
      plugin:
        plugin: checkout-ops
        action: mitigate
        input:
          mode: graceful
      rollback:
        type: plugin
        plugin:
          plugin: checkout-ops
          action: restore
          input: {}
```

## Step guarantees

Every step stores an immutable input snapshot, status, timestamps, timeout, retry policy, attempt count, bounded output, sanitized error, correlation ID, and idempotency key. Workers claim steps with expiring PostgreSQL leases. Two workers cannot hold the same step attempt concurrently.

A crashed worker is recovered according to action safety:

- wait and approval state remains persisted and resumes without busy polling;
- plugin actions and explicitly idempotent HTTP actions may be retried using the same idempotency key;
- a non-idempotent HTTP action with an uncertain outcome becomes `ambiguous` and requires explicit operator retry.

## Conditions

`when` supports `equals`, `in`, and `not` over a bounded set of incident, execution, and prior-step status/output fields. Unknown fields fail validation. Conditions do not execute code.

## Outbound security

HTTP and plugin destinations must use `http` or `https`, match an exact configured host allowlist, and resolve only to permitted addresses. Redirects, URL userinfo, loopback, link-local, metadata, and multicast destinations are rejected. Inline authorization, cookie, token, secret, and API-key fields are prohibited; credentials belong in encrypted plugin/channel configuration.

## Rollback

Rollback is never inferred. A step is rollback-capable only when its immutable version explicitly defines a rollback HTTP or plugin action. `executions rollback` runs eligible successful steps in reverse order. Shell actions remain unavailable.
