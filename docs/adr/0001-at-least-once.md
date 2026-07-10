# ADR-0001: At-least-once delivery with idempotent consumers

Status: accepted

StormRelay uses JetStream publish acknowledgements and explicit consumer acknowledgements. A message is acknowledged only after the PostgreSQL transaction commits. Redelivery is expected and is represented as a duplicate record. Exactly-once claims are prohibited because PostgreSQL, JetStream, and external providers do not share a transaction.
