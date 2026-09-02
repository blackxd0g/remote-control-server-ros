# Milestone 0.1 architecture

```text
Official RustDesk Client
  ├─ POST /api/login ──> art-api ──> repository (SQLite/PostgreSQL)
  │                         ├─ Argon2id password verification
  │                         ├─ server-side session
  │                         └─ HS256 JWT {sub,sid,iat,exp,iss,aud,...}
  └─ PunchHole / Relay ─> art-hbbs
                            ├─ signed secure transport
                            ├─ local JWT verification
                            ├─ cached user + session state
                            ├─ ACL enforcement
                            ├─ Strategy enforcement
                            ├─ peer rendezvous
                            └─ one-use relay permit ─> art-hbbr
```

The API owns durable identity and session state. HBBS owns the latency-sensitive connection decision. Database queries and HTTP calls never occur on the PunchHole/Relay decision path.

## Layer boundaries

- `art-api/internal/domain`: persistence-neutral models and repository contracts.
- `art-api/internal/store/sqlstore`: SQLite/PostgreSQL implementation and migrations.
- `art-api/internal/auth`: passwords, tokens, sessions, revocation, and user state transitions.
- `art-api/internal/httpapi`: official client transport compatibility and internal sync transport.
- `art-core/auth`: independent JWT verification and last-valid authorization cache.
- `art-core/protocol`: RustDesk protobuf wire structures, bounded frame codec, address encoding.
- `art-core/transport`: persistent Ed25519 server identity and NaCl-compatible secure frames.
- `art-hbbs/gate`: pre-rendezvous authentication, ACL and Strategy enforcement boundary.
- `art-hbbr`: data relay that only accepts UUIDs permitted by HBBS.

Authentication providers operate above session creation. Local Argon2id, generic OIDC, and LDAP/Active Directory over LDAPS or StartTLS are implemented without coupling provider credentials to JWT/session/HBBS authorization. LDAP group mappings are explicit and local account provisioning remains policy-controlled. Administrative authorization uses persisted custom RBAC roles with server-enforced permissions; the built-in `admin` role remains an immutable full-access break-glass role.

## Cache consistency

API emits `USER_UPDATED`, `USER_DISABLED`, `SESSION_CREATED`, `SESSION_REVOKED`, and `SESSION_REVOKED_ALL` over an authenticated SSE channel. A bounded replay journal closes the snapshot-to-subscription race and replays missed revisions after reconnect. HBBS applies monotonically increasing revisions and persists a password-free snapshot. A reconciliation snapshot runs every 60 seconds by default.

When API is unavailable, HBBS keeps the last valid cache. A new or empty installation is fail-closed: a cryptographically valid token without a cached active session is rejected. A stale reconciliation snapshot cannot replace newer event state.

## Future extension points

- Cluster: replace process-local event revision with a durable ordered event log; cache contracts remain unchanged.
