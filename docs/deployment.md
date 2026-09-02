# Deployment

## All-in-one (recommended for MikroTik CHR)

```sh
export RDS_PUBLIC_HOST=rustdesk.example.net
docker compose -f deploy/compose.all-in-one.yaml build --pull
docker compose -f deploy/compose.all-in-one.yaml up -d
```

The all-in-one image contains three static binaries and BusyBox `sh` only. Node.js, Python, and Java are not present. API starts first and creates the shared secrets; HBBS/HBBR start only after those files exist.

The published all-in-one image is `blackxdog/remote-control-server-ros:2.1.0` (`latest` points to the same release). Mount a persistent RouterOS directory at `/data`, set `RDS_DATA_DIR=/data`, `RDS_DB_DRIVER=sqlite`, `RDS_RELAY_SERVER=<public-host>:21117`, and `RDS_REQUIRE_LOGIN=true`. The optional `RDS_DEVICE_ONLINE_TTL` controls how long a device remains online after its last heartbeat (default `180s`, minimum `30s`). Managed online backups default to every `24h` with `14` retained artifacts; configure these with `RDS_BACKUP_INTERVAL` and `RDS_BACKUP_RETENTION`. The web console is served from `/` on TCP `21114`. Existing `ART_*` names remain compatibility aliases.

When the API is behind a reverse proxy, set `RDS_TRUSTED_PROXIES` to an explicit comma-separated CIDR list, for example `192.168.255.18/32`. Forwarded client IP headers are ignored and removed unless the direct transport peer is trusted. Never use `0.0.0.0/0`. Nginx Proxy Manager should forward `Host`, `X-Real-IP`, `X-Forwarded-For`, and `X-Forwarded-Proto` and enable WebSocket support.

Prometheus metrics are available at `GET /metrics` with `Authorization: Bearer <token>` or `X-RDS-Metrics-Token`. The persistent token is generated once at `/data/secrets/metrics.token`; it can instead be supplied through `RDS_METRICS_TOKEN` or `RDS_METRICS_TOKEN_FILE`. The administrator Infrastructure page provides authenticated self-diagnostics without exposing this token.

Administrators with `settings.write` can upload a custom console logo from Settings → Appearance. PNG and SVG files up to 2 MiB are validated and stored persistently at `/data/branding/logo.custom`, so the existing `/data` mount must be preserved during upgrades. Resetting the logo restores the bundled default without changing other settings.

## User registration and approval

Registration is disabled by default. Set `ART_REGISTRATION_ENABLED=true` to expose `/register` and `POST /api/register`. New accounts are created as `pending`, enter the system group `Новые пользователи`, and may sign in only to the account portal at `/account`. HBBS denies PunchHole/Relay authorization until an administrator approves the account in `/admin/users`; approval atomically moves it to `Авторизованные пользователи`. Administrative sign-in is isolated at `/admin/login`, while ordinary users use `/account/login`.

TOTP two-factor authentication defaults to `ART_MFA_MODE=optional`. Users enroll from Settings before an administrator changes this to `required_for_admins` or `required_for_all_users`; changing directly to a required mode would intentionally deny password-only login to accounts that have not enrolled. The encryption key is generated once at `/data/secrets/mfa.secret` with mode `0600`. Preserve it with the rest of `/data`, or provide the same value through `ART_MFA_SECRET`/`ART_MFA_SECRET_FILE` on every replica.

Enrollment returns ten one-time recovery codes. Only keyed hashes are persisted; the plaintext values are shown once and must be stored outside the server. Regenerating the set immediately revokes every previous recovery code. Official RustDesk Client login uses its native `tfa_check` response followed by a `tfa_code` request with a short-lived, one-use server-side challenge.

## Generic OIDC

OIDC is disabled by default. Configure `ART_OIDC_ISSUER`, `ART_OIDC_CLIENT_ID`, and `ART_OIDC_REDIRECT_URL`; `ART_OIDC_CLIENT_SECRET` is optional only for a public PKCE client. The callback URL registered at the provider must exactly match `<public-api-url>/api/oidc/callback`. Optional settings are `ART_OIDC_PROVIDER_NAME`, `ART_OIDC_SCOPES`, and `ART_OIDC_AUTO_REGISTER`.

With auto-registration disabled, an existing local user can sign in with their password and link the external identity under Settings. The link flow performs a fresh provider authorization and binds its one-time result to that authenticated local user; administrators can inspect and revoke links in Users. Subjects cannot be entered manually or linked by email alone.

The implementation uses discovery, Authorization Code flow, PKCE S256, nonce and state validation, signed ID Token/JWKS verification, and one-use polling codes compatible with the official RustDesk Client. Auto-registration defaults to `false`; when enabled, a new local `user` account is created and permanently linked by the tuple `(provider, subject)`. It never links an existing account merely because an email address matches.

## Split

```sh
export RDS_PUBLIC_HOST=rustdesk.example.net
docker compose -f deploy/compose.yaml build --pull
docker compose -f deploy/compose.yaml up -d
```

The split runtime images are `scratch` and run as UID/GID `65532`. The named volume is initialized with matching ownership. For RouterOS bind mounts, create a persistent directory writable by this numeric UID or explicitly map permissions before starting the container.

Publish TCP `21114`, `21115`, `21116`, `21117`; add TCP `21118` and `21119` when WebSocket clients are required. Publish UDP `21116`, but keep UDP `21119` internal. Put port `21114` behind HTTPS and terminate WSS for `/ws/id` and `/ws/relay` at a trusted reverse proxy; never expose credentials over plain HTTP on an untrusted network.

Each HBBR reports active relay sessions and current byte rate to the authenticated internal API every five seconds. In split deployments set a unique `ART_HBBR_ID`, its client-facing `ART_HBBR_PUBLIC_ADDRESS`, and `ART_API_INTERNAL_URL`. The internal URL may remain plain HTTP only on the private container network; the shared token is read from `ART_INTERNAL_SECRET_FILE`.

HBBS sends its instance identity and current rendezvous peer count over the same authenticated internal channel. Set a unique `ART_HBBS_ID` per node. Dashboard and Infrastructure status becomes offline after 20 seconds without a heartbeat; this is runtime state and intentionally is not persisted across API restarts.

## PostgreSQL

Set:

```text
ART_DB_DRIVER=postgres
ART_DB_DSN=postgres://user:password@postgres:5432/art?sslmode=require
```

SQLite remains the default for a single CHR node. WAL, foreign keys, a busy timeout, and one writer connection are enabled. Do not place SQLite on an unreliable network filesystem.

## First start

If `ART_BOOTSTRAP_ADMIN_PASSWORD` is absent, a random password is written once to `/data/secrets/bootstrap-admin.txt` with mode `0600`. The username defaults to `admin` and can be changed with `ART_BOOTSTRAP_ADMIN_USERNAME` before the first start.

Read `/data/secrets/id_ed25519.pub` after HBBS starts and set it as the RustDesk client key. Preserve `/data` across every restart; regenerating identity or JWT secrets breaks clients or active sessions.
