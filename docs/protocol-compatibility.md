# Official RustDesk Client compatibility

The implementation was checked against the current upstream protocol and client sources on 2026-08-30. Upstream compatibility has priority over behavior in the lejianwen forks.

## Login API

`POST /api/login` accepts the fields used by the official client and common open-source API implementations:

```json
{
  "username": "alice",
  "password": "...",
  "id": "123456789",
  "uuid": "client-device-uuid",
  "type": "client",
  "autoLogin": true,
  "deviceInfo": {"os": "Windows", "type": "desktop", "name": "PC"}
}
```

When TOTP is enabled, the official client receives `{"type":"tfa_check","tfa_type":"totp","secret":"<one-use challenge>"}` and completes login with `type=tfa_code`, `tfaCode`, and the returned `secret`. The challenge expires after three minutes and is atomically consumed after successful verification.

Generic OIDC uses the official client's `/api/login-options`, `/api/oidc/auth`, and `/api/oidc/auth-query` polling contract. Provider callbacks terminate at `/api/oidc/callback`; the poll code is separate from OAuth state and bound to the requesting RustDesk ID and client UUID.

Console identity linking reuses the verified Authorization Code + PKCE callback but has a separate authenticated `/api/oidc/link` and `/api/oidc/link-query` flow. A link result is atomically consumable only by the local user who initiated it.

Self-registered accounts may obtain an API session while their approval status is `pending`, allowing the account portal to show an accurate status. The same JWT remains unusable for remote connections because HBBS checks the event-synchronized `approval_status` before session, ACL, strategy, PunchHole, or Relay processing and returns `Ваша учётная запись ожидает подтверждения администратора`.

The successful response keeps the expected shape:

```json
{
  "access_token": "<jwt>",
  "type": "access_token",
  "expires_at": "...",
  "user": {"id": "...", "name": "alice", "is_admin": false, "status": 1}
}
```

Compatibility aliases are available at `GET /api/user/info` and `POST /api/currentUser`. Logout is `POST /api/logout` with `Authorization: Bearer <token>`.

## Address book API

ART supports both the legacy client synchronization contract (`GET/POST /api/ab` and `POST /api/ab/get`) and the current personal/shared routes used by RustDesk clients:

- `POST /api/ab/personal` and `POST /api/ab/settings`;
- `POST /api/ab/shared/profiles` (plus the `shared-profiles` alias);
- `GET/POST /api/ab/peers` (both current and legacy desktop clients);
- `POST /api/ab/tags/{bookID}` required by the RustDesk 1.4.9 non-legacy pull flow;
- `POST /api/ab/tag/add/{bookID}`, `PUT /api/ab/tag/rename/{bookID}`, `PUT /api/ab/tag/update/{bookID}`, and `DELETE /api/ab/tag/{bookID}` with persistent colors and peer assignments;
- `POST /api/ab/peer/add/{bookID}`, `PUT /api/ab/peer/update/{bookID}`, and `DELETE /api/ab/peer/{bookID}`.

Personal books are visible only to their owner and administrators. Shared books use explicit `read` or `write` grants assigned to a user or user group. The owner and administrators retain `manage`; every compatible client route applies the same authorization service as the web console API.

## Inventory and client audit

The official background synchronization routes are implemented at `POST /api/heartbeat`, `POST /api/sysinfo`, and `POST /api/sysinfo_ver`. Inventory decoding deliberately accepts unknown future fields, but keeps strict request-size, RustDesk ID, UUID, and stored device-identity checks. Authenticated client directory requests are available at `GET /api/users`, `GET /api/peers`, and `GET /api/device-group/accessible`; pagination query parameters are accepted for wire compatibility.

RustDesk connection and file-transfer reports are accepted at `POST /api/audit/conn` and `POST /api/audit/file`. These routes are called by the remote service process and therefore do not depend on an interactive user JWT. ART instead requires the reporting RustDesk ID and UUID to match an already discovered device before appending an immutable audit event. Payload sizes, action values, peer count, path length, and embedded file metadata are validated before persistence.

## Rendezvous token

The current protobuf places the access token in:

- `PunchHoleRequest.token`, field 5;
- `RequestRelay.token`, field 8.

ART validates both before target registry lookup or relay authorization. Denials use `PunchHoleResponse.other_failure` (field 7) or `RelayResponse.refuse_reason` with one of the explicit messages:

- `Для подключения необходимо войти в аккаунт RustDesk`
- `Connection denied: session expired`
- `Connection denied: session revoked`
- `Connection denied: user disabled`
- `Connection denied: force re-login required`

## Secure TCP

When an API token is used, ART requires the configured RustDesk server public key. HBBS sends an Ed25519-signed ephemeral Curve25519 public key in `KeyExchange`; the client returns its ephemeral public key and a NaCl-box-encrypted XSalsa20-Poly1305 key. Subsequent protobuf frames use secretbox with independent little-endian 64-bit send/receive nonce counters starting at 1. A byte-for-byte compatible round-trip is covered by a Rust integration test.

## Ports in 0.1

- `21114/tcp`: API (place behind HTTPS reverse proxy).
- `21115/tcp`: NAT probe.
- `21116/tcp+udp`: secure rendezvous and peer registration.
- `21117/tcp`: relay data.
- `21118/tcp`: WebSocket rendezvous (`/ws/id`).
- `21119/tcp`: WebSocket relay (`/ws/relay`).
- `21119/udp`: private HBBS-to-HBBR permit channel; do not publish the UDP service.

WebSocket rendezvous and relay use binary WebSocket frames compatible with the upstream server. Authentication and policy checks are applied to WebSocket PunchHole/Relay requests before target lookup exactly as on the secure native TCP path. WSS termination belongs at a trusted reverse proxy.

References: [official client](https://github.com/rustdesk/rustdesk/blob/master/src/client.rs), [current rendezvous proto](https://github.com/rustdesk/hbb_common/blob/master/protos/rendezvous.proto), [official community server](https://github.com/rustdesk/rustdesk-server), [lejianwen API reference](https://github.com/lejianwen/rustdesk-api), and the [`forapi` server branch](https://github.com/lejianwen/rustdesk-server/tree/forapi).
