# Roadmap status

This document records the implemented production boundary. A capability is listed as complete only when it has a persistent model, server-side enforcement where applicable, an API surface, and automated coverage for its security-critical path.

## 0.1 Authentication Core — complete

- Argon2id local authentication, first-run bootstrap, rate limiting, server-side sessions, strict JWT claims, logout/revoke/disable/force-relogin, and immutable audit events.
- Authentication is enforced by HBBS before target lookup, punch-hole instructions, or relay permits. The last-valid authorization cache is event-driven, reconciled, persisted, and fail-closed on an empty installation.
- Official RustDesk login, account, heartbeat, inventory, audit, TCP, and WebSocket flows are supported.

## 0.2 Access Control — complete

- User groups, device groups, tags, personal/shared address books, folders, favourites, search, grants, and current-client address-book routes.
- Server-side ACL permissions are resolved and enforced by HBBS before a connection is established.

## 0.3 Strategies — complete

- Global, user, user-group, device, and device-group assignments with deterministic priority and specificity inheritance.
- Security-sensitive settings are enforced by HBBS; compatible client settings are delivered through the official heartbeat response.

## 0.4 Enterprise Auth — complete

- TOTP with one-time recovery codes and configurable enforcement modes.
- Generic OIDC account linking and login.
- LDAP/Active Directory over LDAPS or StartTLS with explicit group mappings and controlled auto-provisioning.
- Persistent custom RBAC roles and scoped, revocable deployment API tokens.

## 0.5 Infrastructure — complete

- Multiple relay registration, health/latency/load history, regional selection, live HBBR telemetry, and authenticated HBBS control.
- API, database, HBBS, HBBR, device, session, CPU, RAM, connection, and traffic visibility.
- Persistent administrator notifications and signed webhook delivery with retry history and SSRF protection.

## 0.6 Client Management — server side complete

- Managed client profiles, scoped assignments, versioned effective configuration, branding metadata, and official-heartbeat delivery.
- Immutable signed configuration artifacts and a persistent native-build queue with atomic capability-aware claims, expiring leases, heartbeat renewal, cancellation, retry and audited completion.
- Dedicated builder authentication, persistent worker inventory, SHA-256 verified binary uploads and artifact downloads are complete. Native compilation itself deliberately remains in dedicated sandboxed platform workers, outside the API/HBBS/HBBR container and repository.

## 0.7 Production Operations — complete

- Token-protected Prometheus gauges for users, sessions, devices, HBBS/HBBR, relays, traffic, CPU, RAM, uptime and auth-cache revision.
- Authenticated server self-diagnostics for database responsiveness, persistent storage, secret permissions, service heartbeats and reverse-proxy trust configuration.
- Explicit trusted-proxy CIDRs with right-to-left `X-Forwarded-For` resolution; forwarding headers from untrusted peers are discarded before authentication, rate limiting or audit.
- Online SQLite snapshots plus bounded, read-only backup upload inspection using SQLite `quick_check`, required-schema validation and audited results. Inspection never replaces the active database.
- Hardened browser response headers, durable metrics credentials and production reverse-proxy documentation.

## 0.8 Device Lifecycle & Fleet Operations — complete

- Active and archived inventories are separated without losing heartbeat history or management metadata.
- Devices can be archived, restored, and permanently removed only after archival; a later heartbeat can safely rediscover a removed device.
- Transactional bulk assignment supports device groups plus independent tag additions and removals.
- UTF-8 CSV export is spreadsheet-safe. Bounded CSV import validates schema, duplicate IDs, field limits, device groups, active inventory membership, and applies the entire file atomically.
- Lifecycle, bulk-update, and import operations are permission-protected and recorded in the immutable audit trail.

## 0.9 Audit Explorer & Connection Analytics — complete

- Indexed server-side filtering by event type, result, actor, target device, IP address, free-text term, and UTC date range.
- Exact result totals and bounded pagination keep the console responsive with large audit histories.
- Filter-aware counters cover total events, allowed and denied connections, and failed authentication attempts.
- Detailed event inspection exposes session, controller, target, reason, and structured metadata without flattening the immutable record.
- Filter-aware UTF-8 CSV export streams the audit history in bounded database pages and protects spreadsheet cells from formula injection.

## 1.0 Session Security Center — complete

- Unified active, revoked, and expired session inventory with the associated username, display name, user state, device identity, client, IP, creation, activity, and expiry data.
- Indexed server-side lifecycle filtering, user/device/IP/client search, exact totals, and bounded pagination.
- Administrators can select and revoke up to 500 active sessions in one operation; every revocation is propagated immediately to HBBS through the existing event-driven authentication cache.
- The current administration session is identified server-side and protected from accidental bulk revocation.
- Bulk revocation requires `sessions.revoke`, validates the complete selection before mutation, and creates one immutable administrative audit event containing the affected session IDs.

## 1.1 Backup & Disaster Recovery — complete

- Scheduled and on-demand online SQLite snapshots are stored under the persistent `/data/backups` directory with configurable retention.
- Every generated or uploaded database is validated with SQLite `quick_check` and required-schema inspection before it is accepted.
- Administrators can list, download, and delete snapshots or stage an uploaded snapshot for recovery through a dedicated RBAC-protected console.
- Restore is applied before services start, verifies a persisted SHA-256 marker, preserves the previous database plus WAL/SHM files, and atomically replaces the active database.
- Backup creation, deletion, restore staging, cancellation, and rejected restore attempts are recorded in the immutable audit trail.

## 1.2 Live Connection Operations — complete

- Official-client `connection_started`, `connection_updated`, and `connection_closed` telemetry is correlated into active, stale, and recently closed connections.
- Connection audit now resolves the authenticated operator and server session from the controller device, so the console can show who connected to each RustDesk ID.
- The administration console provides an auto-refreshing connection center with controller device, target ID, connection type, IP address, start time, and duration.
- Administrators can contain an attributed live connection in one action: the operator session is revoked through the normal event-driven auth path, reconnects are blocked immediately, the live projection is closed, and the action is written to the immutable audit trail.
- The API response and console explicitly report that transport interruption is not guaranteed. Exact termination of an established relay stream requires an HBBR relay UUID correlation channel; direct P2P streams cannot be reliably torn down after rendezvous by server design.

## 1.3 Security & Compliance — complete

- Password requirements are centrally configurable and enforced for registration, user creation, password changes, and administrator edits without weakening existing Argon2id storage.
- Username-based brute-force state and lockouts are persisted in the shared database, survive restarts, normalize login names, expose `Retry-After`, and are cleared after a successful authentication.
- TOTP enrollment, mandatory modes, one-time recovery codes, administrator reset, server-side session revocation, security audit events, and hardened proxy/header handling remain enforced.

## 1.4 Advanced Access Control — complete

- ACL rules support explicit `allow` and `deny` effects. Lower priority numbers take precedence; at the same priority an explicit deny wins.
- API simulation and HBBS pre-connection enforcement use the same deterministic rule semantics. Existing installations migrate old rules to `allow` without manual intervention.
- The console includes an effective-access simulator with per-rule matching trace, winning effect, and priority, so administrators can verify a policy before relying on it.

## 1.5 Event-driven Automation — complete

- Persistent automation rules subscribe to the existing internal event stream and can filter domain or immutable audit event fields without polling.
- Rules provide durable execution history, per-rule throttling, severity, RBAC-protected management APIs, administrator notifications, and a dedicated console.
- Outbound actions reuse the signed webhook delivery pipeline with retry and SSRF protection; no shell-command action is exposed and generated events cannot recursively invoke automation.

## 1.6 High Availability & Cluster Readiness — complete

- API instances have a persistent node identity, database-backed heartbeat inventory, and an administrator-visible cluster state endpoint.
- Atomic renewable leases work on SQLite and PostgreSQL semantics and prevent an active lease from being stolen before expiry.
- Relay monitoring, webhook delivery, and scheduled backups run under separate leader leases; event ingestion remains local to every API node so events are not dropped on followers.
- The dashboard exposes active API nodes and leases, and existing saved layouts automatically acquire newly introduced widgets.

## 1.7 External Builder Trust Boundary — complete

- The shared bootstrap credential can only register a worker. Registration returns a unique high-entropy worker credential once, and only its SHA-256 digest is persisted.
- Heartbeat, claim, lease renewal, payload, completion, and failure routes require the individual worker credential and bind every operation to the authenticated worker identity.
- Re-registering a worker explicitly rotates its credential. A worker cannot impersonate another worker ID or use the bootstrap credential to claim work.
- Native compilation remains intentionally outside this image. The server-side queue and protocol are stable; the separately isolated worker container can be connected later without access to the database or server secrets.

## 1.8 Supportability — complete

- Administrators can download an audited support ZIP from a dedicated console page.
- The bundle contains version, safe runtime policy, cluster state, inventory counters, and a bounded redacted audit timeline.
- Passwords, hashes, JWTs, worker credentials, usernames, IP addresses, file names, and connection content are excluded by construction.

## 1.9 Upgrade Readiness — complete

- Database changes through 2.0 are additive and migrate automatically for existing SQLite and PostgreSQL installations.
- Persistent `/data` keeps the legacy database filename, server identity, JWT/session material, branding, backups, and runtime configuration to avoid a destructive rename during upgrade.
- A documented pre-upgrade backup, rollback boundary, release checklist, and immutable versioned image tag are required for the 2.0 release gate.

## 2.0 Stable — complete

- Authentication, pre-connection enforcement, ACL, Strategies, enterprise authentication, audit, automation, cluster coordination, backup/restore, fleet operations, managed-client control plane, and supportability have a persistent production implementation.
- The external native Builder worker remains an optional separate component and is not bundled into the lightweight RouterOS image.

## Release gate

Every release must pass Vue TypeScript validation and production build, Go formatting/vet/tests/static linux-amd64 build, and Rust formatting/clippy/tests/release build. Container publication and target-host deployment are separate post-gate operations.

## Runtime security configuration

The administrator console persists operational security policy in the database. Environment values remain first-run defaults; once an administrator saves a value, it survives restarts and takes precedence. `require_login` and `require_device_deployment` are propagated to every HBBS node through the internal event stream and periodic snapshot reconciliation. Registration, access-token/session lifetimes, and the TOTP enforcement mode apply immediately to new authentication operations. Existing signed tokens keep their original expiry, while revocation and force-login remain available for immediate invalidation.
