# RustDesk Server RouterOS 0.6.0

This release completes the planned server-side client-management boundary and adds production runtime administration.

- persistent managed-client profiles, assignments and capability-aware native build queue;
- authenticated external builder workers with leases, heartbeats and verified artifacts;
- persistent runtime security settings with audited Web UI management;
- event-driven and reconciled `require_login` and device-enrollment enforcement in HBBS;
- configurable console PNG/SVG branding with secure validation;
- responsive desktop/mobile navigation and corrected light theme;
- stable device identity rotation for unmanaged clients while deployed devices stay pinned;
- durable notification read state and identity-mismatch deduplication;
- official RustDesk Client address-book compatibility fixes.

The release is storage-compatible with 0.5.0. Keep the existing `/data` mount to preserve the database, server identity, JWT secrets and sessions. Environment values are defaults for runtime settings that have not yet been saved by an administrator.
