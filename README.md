**English** | [Русский](README_RU.md)

# Remote Control Server

> Lightweight self-hosted remote-control platform compatible with the RustDesk client protocol.
> Built for `linux/amd64`, `linux/arm64`, MikroTik CHR, and RouterOS
> containers with authentication, access control, relay, API, and a modern web console.

[![GitHub release](https://img.shields.io/github/v/release/blackxd0g/remote-control-server-ros?label=release)](https://github.com/blackxd0g/remote-control-server-ros/releases)
[![Docker Pulls](https://img.shields.io/docker/pulls/blackxdog/remote-control-server-ros?logo=docker&label=docker%20pulls)](https://hub.docker.com/r/blackxdog/remote-control-server-ros)
[![Docker Image Size](https://img.shields.io/docker/image-size/blackxdog/remote-control-server-ros/latest?logo=docker&label=image%20size)](https://hub.docker.com/r/blackxdog/remote-control-server-ros)
![Platform](https://img.shields.io/badge/arch-amd64%20%7C%20arm64-success)
![RouterOS](https://img.shields.io/badge/RouterOS-7.22%2B-blue)
![Protocol](https://img.shields.io/badge/protocol-RustDesk%20compatible-7B61FF)
[![Boosty](https://img.shields.io/badge/Boosty-support-f15f2c?logo=boosty&logoColor=white)](https://boosty.to/blackxdog/donate)

## ✨ Features

- 🔐 Mandatory client login before PunchHole or relay authorization.
- 🎫 Argon2id passwords, server-side sessions, JWT, logout, revoke, and forced re-login.
- 👥 Users, custom RBAC roles, user groups, and registration approval workflow.
- 🖥 Device inventory, device groups, tags, ownership, online state, and deployment control.
- 🛡 Pre-connection ACL enforcement for control, view, files, clipboard, tunnel, and terminal access.
- 🧭 Personal and shared address books compatible with current official client routes.
- ⚙️ Inherited Strategies for global, user, group, device, and device-group policies.
- 📜 Audit of authentication, connections, denied requests, security changes, and file transfers.
- 🔑 TOTP, generic OIDC, and encrypted LDAP / Active Directory integration.
- 🌐 Multiple relay registration, health monitoring, telemetry, and load-aware selection.
- 🔔 Signed webhooks, persistent notifications, backup scheduling, and automation.
- 🧰 Managed-client profiles and an isolated builder-worker API boundary.
- 📊 Responsive Vue 3 console with runtime, CPU, RAM, uptime, peer, session, and relay metrics.
- 📦 Split scratch images and a compact all-in-one image for RouterOS.

> [!NOTE]
> Remote Control Server is an independent clean-room project and is not affiliated with RustDesk.
> RustDesk is a trademark of its respective owner; the name is used only to describe protocol compatibility.

## ⚡ Quick start

1. Set the public hostname and start the all-in-one deployment:

```sh
export RDS_PUBLIC_HOST=remote.example.net
docker compose -f deploy/compose.all-in-one.yaml up -d
```

2. Read the generated first-run credentials:

```sh
docker compose -f deploy/compose.all-in-one.yaml exec rustdesk-server-routeros \
  cat /data/secrets/bootstrap-admin.txt
```

3. Open `http://localhost:21114/` for local testing. In production, expose port
   `21114` only through a trusted HTTPS reverse proxy.

Configure the client with:

| Client field | Value |
|---|---|
| ID server | `remote.example.net:21116` |
| Relay server | `remote.example.net:21117` |
| API server | `https://remote.example.net` |
| Key | contents of `/data/secrets/id_ed25519.pub` |

> [!IMPORTANT]
> Remove `bootstrap-admin.txt` after the first successful administrator login and
> keep the complete `/data` directory persistent. It contains the database, identity keys, and secrets.

## 🚀 Automated RouterOS deployment

[![Installer](https://img.shields.io/badge/RouterOS-download%20installer-0A84FF?logo=mikrotik&logoColor=white)](deploy/routeros-install.rsc)
[![Updater](https://img.shields.io/badge/RouterOS-download%20updater-475569?logo=mikrotik&logoColor=white)](deploy/routeros-update.rsc)
[![Guide](https://img.shields.io/badge/docs-deployment%20guide-7B61FF)](docs/routeros-autodeploy.md)

The installer prepares the VETH network, persistent `/data` mount, environment,
firewall rules, and the `blackxdog/remote-control-server-ros:latest` container.
The multi-architecture manifest automatically selects the correct image for CHR,
RB5009 and hAP ax3. ARM32 devices such as RB3011 and RB4011 are not supported.

### 1. Enable containers

```routeros
/system/device-mode/print
/system/device-mode/update mode=advanced container=yes
```

Confirm the change using the physical button or a power cycle within the RouterOS
time window. The `container` package and `device-mode container=yes` are required.

### 2. Download the installer to RouterOS

Paste this snippet into the MikroTik terminal:

```routeros
:local url "https://raw.githubusercontent.com/blackxd0g/remote-control-server-ros/main/deploy/routeros-install.rsc"
:local dst "routeros-install.rsc"
/tool fetch url=$url mode=https dst-path=$dst
:put ("Downloaded " . $dst . ". Review its configuration block before import.")
```

Open `routeros-install.rsc` in **Files** and review its network settings. At
startup, the installer asks for storage, the public DNS hostname, the administrator
name, and an optional bootstrap password.

Press Enter for the administrator name to use `admin`. Leaving the password empty
is recommended: the server creates a random one-time bootstrap password inside the
persistent data directory. `/terminal ask` is not a masked password widget, so use
a trusted SSH or WinBox session.

### 3. Install and start

```routeros
/import file-name=routeros-install.rsc verbose=yes
```

After image extraction reaches `stopped`, run the start command printed by the script.

### 4. Update later

Download [`deploy/routeros-update.rsc`](deploy/routeros-update.rsc) and import it.
It uses RouterOS container update, preserves the `/data` mount, waits for extraction,
and starts the updated container.

| Setting | Default | Purpose |
|---|---|---|
| `image` | `blackxdog/remote-control-server-ros:latest` | Published container image |
| `containerName` | `remote_control_server` | RouterOS container name |
| `storageChoice` | prompted | `usb1` or built-in `system` storage |
| `dataRoot` | calculated automatically | Persistent storage and extraction root |
| `publicHost` | prompted, required | Public DNS name used by clients |
| `adminUsername` | `admin` | Prompted; Enter keeps `admin` |
| `adminPassword` | empty | Enter lets the server generate a one-time password |
| `containerAddress` | `172.31.255.2/30` | Isolated VETH address |
| `wanList` | `WAN` | Interface list used by published protocol ports |

| MikroTik | RouterOS architecture | Container platform | Guidance |
|---|---|---|---|
| CHR / x86_64 | `x86_64` | `linux/amd64` | Recommended for large deployments |
| RB5009 / hAP ax3 | `arm64` | `linux/arm64` | Recommended physical RouterOS targets |
| RB3011 / RB4011 | `arm` | Unsupported | ARM32 is intentionally excluded |

> [!IMPORTANT]
> When selecting `system`, make sure internal storage has enough free space. Check
> the `172.31.255.0/30` link network, the `WAN` interface list,
> and firewall policy before importing the script. Review fetched scripts before
> execution; for production, prefer a versioned release URL instead of `main`.

## 🔌 Network ports

| Port | Protocol | Purpose |
|---:|---|---|
| `21114` | TCP | API and web console; reverse proxy recommended |
| `21115` | TCP | NAT type test |
| `21116` | TCP + UDP | Rendezvous / ID server |
| `21117` | TCP | Relay server |
| `21118` | TCP | WebSocket rendezvous |
| `21119` | TCP | WebSocket relay |

## 🧱 Architecture

```text
Official client
    │ login / session token
    ▼
API Server ── users · sessions · devices · policies · audit
    │ event sync + reconciliation
    ▼
HBBS ── authentication ── ACL ── Strategy ── target lookup
    │
    ├── direct P2P
    └── HBBR relay
```

| Component | Runtime | Responsibility |
|---|---|---|
| `art-hbbs` | Rust | Rendezvous, protocol compatibility, auth/ACL/strategy gate |
| `art-hbbr` | Rust | Relay transport and telemetry |
| `art-core` | Rust | Protocol, claims, cache, sync, and shared security logic |
| `art-api` | Go | API, persistence, authentication, policies, audit, and embedded web assets |
| `art-web` | Vue 3 + TypeScript | Static administration console |

## 💾 Persistence

The all-in-one image requires one persistent mount:

```text
RouterOS / Docker volume  →  /data
```

SQLite is the default lightweight mode. PostgreSQL is available for larger deployments.
The server generates shared JWT and identity secrets once and reuses them across restarts.

## 🛡 Security

- Keep `RDS_REQUIRE_LOGIN=true` for production.
- Terminate public API traffic with HTTPS and configure `RDS_TRUSTED_PROXIES` explicitly.
- Never publish `/data`, database files, private keys, bootstrap credentials, or backup secrets.
- Use a dedicated database account for PostgreSQL deployments.
- Restrict builder tokens to isolated worker machines; builders never receive server or DB secrets.
- Review audit events after authentication, ACL, strategy, and server configuration changes.

## 🧪 Local checks

```sh
(cd art-api && go vet ./... && go test ./... && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...)

cargo fmt --all -- --check
cargo clippy --workspace --all-targets -- -D warnings
cargo test --workspace
cargo build --workspace --release

(cd art-web && pnpm exec vue-tsc --noEmit && pnpm build)

docker buildx build --platform linux/amd64,linux/arm64 \
  -f docker/all-in-one.Dockerfile .
```

## 📚 Documentation

| Document | Purpose |
|---|---|
| [`docs/routeros-autodeploy.md`](docs/routeros-autodeploy.md) | RouterOS installation and updates |
| [`docs/deployment.md`](docs/deployment.md) | Docker and production deployment |
| [`docs/architecture.md`](docs/architecture.md) | Service boundaries and data flow |
| [`docs/security.md`](docs/security.md) | Security model and operational guidance |
| [`docs/protocol-compatibility.md`](docs/protocol-compatibility.md) | Official client compatibility |
| [`docs/upgrade-2.0.md`](docs/upgrade-2.0.md) | Upgrade guide for version 2.0 |
| [`docs/roadmap-status.md`](docs/roadmap-status.md) | Implemented roadmap status |

## 💖 Support the project

If Remote Control Server saves you time or is useful in your infrastructure, you can
[support its development on Boosty](https://boosty.to/blackxdog/donate).

[![Support on Boosty](https://img.shields.io/badge/Boosty-support-f15f2c?logo=boosty&logoColor=white)](https://boosty.to/blackxdog/donate)

Your support helps test new RouterOS and client releases, maintain protocol
compatibility, and develop the security and management features of future versions.

## 📄 Project files

| Path | Purpose |
|---|---|
| [`deploy/routeros-install.rsc`](deploy/routeros-install.rsc) | Automated RouterOS installation |
| [`deploy/routeros-update.rsc`](deploy/routeros-update.rsc) | RouterOS container update |
| [`deploy/compose.all-in-one.yaml`](deploy/compose.all-in-one.yaml) | Compact all-in-one deployment |
| [`deploy/compose.yaml`](deploy/compose.yaml) | Split deployment |
| [`docker/`](docker/) | Production multi-stage Dockerfiles |
| [`docs/`](docs/) | Architecture, security, compatibility, and operations |
| [`README_RU.md`](README_RU.md) | Russian documentation |
