# Remote Control Server 2.2.0

## Multi-architecture RouterOS release

- Adds one OCI manifest for `linux/amd64` and `linux/arm64`.
- Supports CHR/x86_64 and RB5009/hAP ax3 ARM64 targets.
- ARM32 devices, including RB3011 and RB4011, are intentionally unsupported.
- Builds Go API binaries with explicit `GOARM=7` for 32-bit ARM.
- Builds static Rust HBBS/HBBR binaries with native musl toolchains for every target.
- Keeps the final all-in-one runtime on lightweight Alpine.
- Adds RouterOS architecture, container-package, and device-mode validation before deployment.
- Extends GitHub CI with QEMU-backed multi-platform image verification.

## Verification

- Go formatting, vet, and tests.
- Rust formatting, Clippy with warnings denied, and all workspace tests.
- Vue TypeScript validation and production Vite build.
- Combined OCI build containing `amd64` and `arm64` manifests.

## Images

- `blackxdog/remote-control-server-ros:2.2.0`
- `blackxdog/remote-control-server-ros:latest`
