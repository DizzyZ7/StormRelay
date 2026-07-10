# Releasing

Release automation and signed artifacts are Milestone 5 work. Until then, maintainers must not create a v0.1.0 tag or advertise release artifacts.

The intended process is: protected `main` commit, green required checks, SemVer tag, reproducible Go binaries for Linux/macOS/Windows, multi-architecture GHCR image, checksums, SBOM, provenance and attestations, keyless image signing, generated release notes, and compatibility/upgrade verification. A release tag must point to a commit reachable from protected `main`.
