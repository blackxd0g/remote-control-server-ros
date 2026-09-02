# RustDesk Server RouterOS 1.0.0

Release 1.0.0 introduces the Session Security Center and marks the first stable release of the clean-room control plane.

## Session security center

- one searchable inventory for active, revoked, and expired sessions;
- user-friendly identity columns include login, display name, user UUID and enabled state;
- device identifier, IP address, user agent, last activity, expiry, and lifecycle status are retained;
- server-side search and status filtering with exact totals and 50-row pagination;
- global counters for total, active, revoked, and naturally expired sessions.

## Immediate access termination

- individual revocation remains available;
- up to 500 active sessions can be selected and revoked together;
- the entire selection is validated before any session is changed;
- the administrator's current session is marked and cannot be included in bulk revocation;
- every successful revocation is published to HBBS immediately, so the session cannot authorize a new punch-hole or relay request;
- one immutable `session_bulk_revoke` audit record captures the actor, count, and affected session IDs.

## Persistence and performance

- session queries execute in the database and do not load the complete history into the browser;
- indexes cover user, creation time, expiry time, and revocation time;
- SQLite and PostgreSQL use the same repository behavior;
- existing sessions, users, keys, database files, environment variables, and mounts remain compatible.

No configuration change is required when upgrading from 0.9.0.
