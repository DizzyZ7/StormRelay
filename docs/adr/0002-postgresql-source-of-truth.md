# ADR-0002: PostgreSQL is the source of truth

Status: accepted

JetStream is transport, not the incident database. PostgreSQL stores raw evidence, normalized fields, deduplication, incident state, policy versions, delivery attempts, and audit. SQL is explicit through pgx. External actions use an outbox because they cannot participate in the event transaction.
