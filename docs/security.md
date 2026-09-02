# Security model

- Login for remote connections defaults to enabled (`ART_REQUIRE_LOGIN=true`).
- JWT is HS256-only and requires `sub`, `sid`, `iat`, `exp`, `iss=art-rustdesk`, and audience `art-hbbs`.
- JWT is only a signed capability reference. An enabled cached user and active matching server-side session are also mandatory.
- Passwords use Argon2id (`m=64 MiB`, `t=3`, bounded verification parameters). No plaintext or unsalted password hashes are stored.
- JWT/internal secrets contain 64 random bytes encoded with base64url, persist under `/data/secrets`, and are forced to mode `0600` on Unix.
- The RustDesk Ed25519 private identity is persistent and mode `0600`; only its `.pub` sibling is distributed.
- Login has per-source-IP failure lockout. Reverse proxies must preserve the real TCP source or implement their own trusted proxy policy; untrusted forwarding headers are ignored.
- Internal snapshot, event, and audit routes require a constant-time-compared internal token.
- Connection audit delivery is buffered and does not turn API latency into rendezvous latency.
- Relay data UUIDs are random and HBBR accepts only two uses after a short-lived authenticated permit from HBBS.
- All untrusted frame lengths are bounded before allocation.

## Operational rules

Terminate HTTPS for port 21114 and WSS for WebSocket endpoints 21118/21119 at a trusted reverse proxy. Port 21119/TCP may be published for WebSocket relay; never publish the private permit service on 21119/UDP. Back up the complete `/data` directory: database, JWT secret, internal secret, and server identity must remain consistent.

Remove `bootstrap-admin.txt` after the first successful login. Rotate a compromised JWT secret only as a planned global logout because API and HBBS must switch atomically. Use the password-reset or force-relogin APIs for individual users.
