# Contributing

StormRelay accepts focused changes that improve real operational behavior. Start with an issue for architectural changes, new durable state, authentication, plugin protocol changes, or runbook semantics.

## Workflow

1. Fork or branch from `main`.
2. Add implementation, tests, and documentation together.
3. Run format, vet, unit, race, and relevant integration tests.
4. Open a PR using the template. Keep unrelated refactors separate.
5. Address review without force-pushing over reviewer context unless necessary.

## Review rules

At least one maintainer approval is required. Security-sensitive and migration changes require review by a CODEOWNER. Authors do not approve their own PRs. Reviewers verify failure boundaries, idempotency, tenant scoping, sensitive-data handling, and compatibility—not only the happy path.

## RFCs and ADRs

Open an issue labelled `rfc` with problem, constraints, alternatives, migration impact, threat analysis, and rollout plan. Accepted decisions become an ADR in `docs/adr/`. Superseded ADRs remain in history.

## Maintainers

A contributor may be nominated after sustained, respectful contributions across implementation, review, documentation, and incident/security handling. Existing maintainers decide by lazy consensus; objections must include a concrete concern and path to resolution. See GOVERNANCE.md.
