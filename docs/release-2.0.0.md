# RustDesk Server RouterOS 2.0.0 Stable

This release consolidates the complete self-hosted control plane into a production-gated `linux/amd64` image for MikroTik CHR and conventional container hosts.

Highlights:

- official RustDesk Client authentication and address-book compatibility with server-side sessions and immediate revocation;
- HBBS pre-connection login, user, ACL, Strategy, and managed-device enforcement;
- user/device lifecycle, groups, shared address books, explicit allow/deny ACL simulation, Strategies, session center, connection analytics, and immutable audit explorer;
- TOTP, generic OIDC, LDAP/AD, custom RBAC, deployment tokens, password policy, persistent brute-force lockouts, and registration approval/auto-approval;
- multi-relay telemetry, signed Webhooks, event-driven automation, leader leases, cluster visibility, online backups and staged validated restore;
- managed-client profiles plus a hardened, separately deployable Builder worker protocol using per-worker credentials;
- redacted audited support bundles and an explicit non-destructive upgrade/rollback contract.

The native Builder worker is not included in the RouterOS image. The API-side integration is stable and ready for the separate isolated worker when that project resumes.

Published image: `blackxdog/rustdesk-server-routeros:2.0.0`; `2.0` and `latest` resolve to the same verified manifest.
