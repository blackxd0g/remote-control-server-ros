# RustDesk Server RouterOS 0.7.0

This production-operations release makes the existing control plane safer to expose through a reverse proxy and easier to monitor and recover.

- protected Prometheus `/metrics` endpoint with a persistent dedicated token;
- Web Console server self-diagnostics and auth-cache revision visibility;
- strict trusted-proxy CIDRs and spoof-resistant forwarded client IP handling;
- consistent online SQLite downloads and safe uploaded-backup integrity/schema inspection;
- additional Permissions Policy and cross-origin browser isolation headers;
- user-facing backup names and console version updated to RustDesk Server RouterOS 0.7.0.

The release is storage-compatible with 0.6.0. It deliberately keeps `art.db`, the existing JWT issuer/audience, server identity and legacy environment aliases unchanged. Reuse the current `/data` mount. For Nginx Proxy Manager on `192.168.255.18`, set `RDS_TRUSTED_PROXIES=192.168.255.18/32`.

The first 0.7 start creates `/data/secrets/metrics.token` with restricted permissions. Preserve it with the rest of `/data`.
