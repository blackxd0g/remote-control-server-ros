# Managed clients and builder foundation

Managed client profiles are separate from connection authorization. ACL and Strategies remain the source of truth for server-side enforcement; profiles add deployment defaults, network endpoints, UI restrictions, and branding metadata.

Profiles can be assigned globally or to a user, user group, device group, or RustDesk ID. Assignments are merged from the least specific to the most specific scope. Higher priority wins. Compatible settings are also returned through the official RustDesk heartbeat strategy response, so managed installations receive updates without a proprietary client protocol.

## Signed configuration packages

The initial builder provider creates immutable JSON configuration packages. Each package contains the complete versioned profile and an HMAC signature derived from the persistent internal platform secret. Build jobs, artifact bytes, SHA-256 digest, creator, target OS, architecture, timestamps, and failures are persisted.

The API process never invokes a shell or compiler. Future executable builders must implement the builder provider interface and run as isolated workers. This keeps build credentials and untrusted branding assets outside the API/HBBS security boundary.

Supported configuration-package targets:

- Windows amd64/arm64;
- Linux amd64/arm64;
- macOS amd64/arm64;
- Android amd64/arm64.

Native installers are intentionally not claimed as implemented yet. The server now provides a persistent queue, atomic worker claims, expiring leases, heartbeat renewal, isolated worker registration, digest-verified completion, cancellation and retry. The wire contract is documented in [builder-api.md](builder-api.md). Dedicated builder containers implement providers without receiving database, JWT, HBBS or HBBR access.
