# RustDesk Remote Control 0.4.0

Release 0.4.0 completes the planned control-plane roadmap through the managed-client builder foundation.

Highlights:

- pre-connection authentication, server-side sessions, ACL, and Strategy enforcement in HBBS;
- official RustDesk Client login, inventory, address-book, audit, TCP, and WebSocket compatibility;
- TOTP, generic OIDC, LDAP/Active Directory, custom RBAC, and deployment API tokens;
- user/device groups, tags, personal and shared address books, folders, favourites, and grants;
- multiple relays, telemetry history, regional selection, server control, notifications, and signed webhooks;
- managed-client profiles, assignments, heartbeat configuration, and signed immutable build artifacts;
- fully operational RU/EN Web Console with light, dark, and system themes;
- SQLite and PostgreSQL persistence with a static Go API and optimized Rust rendezvous/relay binaries.

The release image is `blackxdog/art-rustdesk:0.4.0` for `linux/amd64`. The `latest` tag resolves to the same immutable image digest.
