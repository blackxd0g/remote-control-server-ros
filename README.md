# Remote Control Server

[![Support on Boosty](https://img.shields.io/badge/Boosty-support-F15F2C?logo=boosty&logoColor=white)](https://boosty.to/blackxdog)

Independent, clean-room, self-hosted remote-control server compatible with the RustDesk client protocol. It targets `linux/amd64` and includes a lightweight deployment path for MikroTik CHR/RouterOS containers.

Remote Control Server is not affiliated with RustDesk. RustDesk is a trademark of its respective owner; the name is used solely to describe protocol compatibility.

Development is supported by the community. You can support new releases and continued compatibility work on [Boosty](https://boosty.to/blackxdog).

The platform now covers the planned authentication, access-control, infrastructure, and managed-client foundations:

- Rust `art-hbbs`, `art-hbbr`, and reusable `art-core`;
- Go API with SQLite and PostgreSQL repository implementations;
- official-client-compatible `/api/login`, `/api/logout`, `/api/me`, `/api/user/info`, and `/api/currentUser` routes;
- Argon2id local passwords, persistent server-side sessions, strict HS256 JWT claims, logout/revoke/disable/force-relogin;
- `PunchHoleRequest.token` and `RequestRelay.token` validation before target lookup, P2P instruction, or relay permit;
- event-driven HBBS auth-cache updates plus periodic reconciliation and persisted last-valid cache;
- RustDesk-compatible signed key exchange and encrypted TCP frames;
- login and connection audit events;
- embedded Vue 3 web console with operational users, custom RBAC roles, devices, groups, address books, ACL, strategies, sessions, audit, relay and infrastructure sections;
- multiple relay registration, active health monitoring, event-driven HBBS relay cache and live HBBR connection/bandwidth telemetry;
- live HBBS/HBBR heartbeats plus container CPU, RAM, uptime, rendezvous peer and aggregate relay metrics;
- scratch split images and an Alpine all-in-one image.
- local Argon2id authentication, TOTP, generic OIDC and encrypted LDAP/Active Directory authentication with explicit group mapping;
- registration approval workflow, custom RBAC, scoped API deployment tokens and full session administration;
- personal/shared address books compatible with current official RustDesk client routes;
- device/user groups, pre-connection ACL enforcement and inherited Strategies;
- persistent relay telemetry/history, regional load-aware relay selection and authenticated HBBS server control;
- signed webhooks with retry history, SSRF protection and audit-event delivery;
- persistent administrator notifications with security severities, unread state and RBAC-protected triage;
- managed client profiles, scoped assignments, official heartbeat integration, signed configuration bundles, an isolated worker registry and capability-aware native build queue.

Native branded executable compilation runs in a separate builder project/container. This server repository provides its production boundary: dedicated authentication, atomic leasing, payload delivery, worker heartbeats, digest-verified uploads, cancellation, retry and artifact downloads without granting a builder access to the database or RustDesk server secrets.

## Quick start

```sh
export RDS_PUBLIC_HOST=rustdesk.example.net
docker compose -f deploy/compose.all-in-one.yaml up --build -d
```

On first start, read `/data/secrets/bootstrap-admin.txt` from the persistent volume. Remove that file after the first successful administrator login. The RustDesk server public key is stored at `/data/secrets/id_ed25519.pub` and is also printed once in HBBS logs.

Open the administration console at `http://localhost:21114/` for local testing, or at the HTTPS API hostname in production.

Configure the official RustDesk Client with:

- ID server: `rustdesk.example.net:21116`
- Relay server: `rustdesk.example.net:21117`
- API server: `https://rustdesk.example.net` (reverse proxy to port `21114`)
- Key: contents of `id_ed25519.pub`

See [roadmap status](docs/roadmap-status.md), [2.0 upgrade guide](docs/upgrade-2.0.md), [architecture](docs/architecture.md), [protocol compatibility](docs/protocol-compatibility.md), [security](docs/security.md), [webhooks](docs/webhooks.md), [notifications](docs/notifications.md), [managed clients](docs/managed-clients.md), and [deployment](docs/deployment.md).

Upgrading from the former ART image is covered by the [0.5.0 rebrand migration](docs/rebrand-0.5.0.md). Existing data, server keys and active sessions remain compatible.

## Local checks

```sh
gofmt -w $(find art-api -name '*.go')
(cd art-api && go vet ./... && go test ./... && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...)

cargo fmt --all -- --check
cargo clippy --workspace --all-targets -- -D warnings
cargo test --workspace
cargo build --workspace --release --target x86_64-unknown-linux-musl

(cd art-web && pnpm run build)
```
