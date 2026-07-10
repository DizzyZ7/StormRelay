# Development

Use Go 1.26.x. Start PostgreSQL and NATS through Docker Compose, or provide `STORMRELAY_TEST_DATABASE_URL` and `STORMRELAY_TEST_NATS_URL`.

```bash
make fmt
make vet
make test
make test-race
make build
go test -tags=integration -count=1 ./tests/integration
```

Keep SQL explicit. A repository method that changes incident or event state should state its transaction and locking behavior in code or an ADR. Tests should be table-driven where cases share structure. Add fuzz seeds for untrusted parsers.

Do not commit credentials, production payloads, screenshots containing incident data, or benchmark numbers not produced by the documented harness.
