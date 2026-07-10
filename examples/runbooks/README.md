# Runbook examples

`approval-demo.yaml` is a runnable Milestone 2 example. It demonstrates a persisted wait followed by manual approval. Apply it through the API or CLI, start an execution, approve the pending request, and inspect the immutable step snapshots and audit trail.

Runbooks are versioned and use the `stormrelay.io/v1` DSL documented in `docs/runbook-dsl.md`. Inline credentials, arbitrary expressions, and generic shell commands are rejected.
