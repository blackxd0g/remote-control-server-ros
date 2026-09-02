# Remote Control Server 2.1.0

## Release links

- Docker image: `blackxdog/remote-control-server-ros:2.1.0`
- Docker rolling tag: `blackxdog/remote-control-server-ros:latest`
- Source: `https://github.com/blackxd0g/remote-control-server-ros`
- Product signature: `Remote Control Server · v2.1.0 · Compatible with the RustDesk client protocol · created by blackxdog`

## Managed relay containment

- HBBS now records the authenticated user, server session, controller device, target ID, selected relay server, and relay UUID before handing off a relay connection.
- HBBR tracks active streams by relay UUID and accepts authenticated, narrowly scoped termination commands over its internal control channel.
- The administrator can physically terminate a managed relay stream and receives an acknowledgement from the selected HBBR.
- The authorization session is revoked even when the stream has already closed or the transport cannot be confirmed, preventing reconnects immediately.
- Direct P2P containment remains explicit: the server blocks future connections but does not claim to control an already established direct encrypted channel.

## Connection lifecycle and console

- Relay lifecycle events keep the API projection synchronized when a stream becomes active or closes normally.
- SQLite and PostgreSQL migrations add transport, relay UUID, and relay server correlation without replacing existing data.
- Connection details show the transport and selected relay endpoint.
- Console messages distinguish confirmed relay termination from session-only containment.

## Validation

- Rust formatting, tests, clippy with warnings denied, and release build.
- Go formatting, tests, vet, native build, and static `linux/amd64` build with `CGO_ENABLED=0`.
- Vue TypeScript check and Vite production build.
