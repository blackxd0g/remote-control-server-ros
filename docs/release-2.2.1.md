# Remote Control Server 2.2.1

- Registration displays the active password requirements before submission and explains password-policy failures in Russian or English.
- The registration options endpoint returns the password policy enforced by the authentication service.
- Registration submission is disabled while options load or registration is disabled.
- New installations default to a minimum password length of 8 characters with no required character classes. Existing persisted settings remain authoritative.
- Registration approval remains required unless the administrator explicitly enables automatic approval.
- Go API container builds use the requested BuildKit target architecture instead of forcing amd64, including ARM64 all-in-one images.

Release verification and deployment results are tracked in `roadmap-status.md`.

## Published and deployed

- Images: `blackxdog/remote-control-server-ros:2.2.1` and `latest` for linux/amd64 and linux/arm64.
- OCI digest: `sha256:e1ad1e4a46601d935082114d21e09892915376c20f6449dba9605a67eef447b8`.
- Vue/TypeScript build, full Linux Go tests/vet, static API builds and both Rust release builds passed. Unchanged Rust fmt/clippy/tests reused verified BuildKit cache.
- Both image architectures passed startup, ELF architecture and registration boundary checks; the registration form was also verified in a browser.
- MikroTik repull completed on 2026-09-07; API, HBBS, HBBR and database were online after upgrade, with user/device counts preserved.
