# Security policy

## Supported versions

Until the first tagged release, only the latest commit on `main` receives security fixes. After v0.1.0, the latest minor line will be supported according to release notes.

## Reporting

Do not open a public issue. Use GitHub private vulnerability reporting for this repository. Include affected commit/version, impact, reproduction with synthetic data, and suggested mitigation if known. Never include real credentials or customer payloads.

The maintainers aim to acknowledge a report within five business days, establish severity and remediation ownership, and coordinate disclosure after a fix is available. Timelines vary with complexity and active exploitation.

## Scope reminders

Development bootstrap authentication is not production multi-user authentication. Milestone 2 adds HTTP and process-plugin actions, but generic shell execution remains disabled and unimplemented. Outbound destinations require explicit host allowlists, redirects are rejected, DNS results are pinned for each request, and loopback/link-local/metadata addresses are prohibited. OIDC, tenant-aware RBAC, and service accounts remain Milestone 3 work.
