# RustDesk Server RouterOS 0.5.0 migration

The public product name is now **RustDesk Server RouterOS** and the container
image is `blackxdog/rustdesk-server-routeros:0.5.0`.

New deployments use the `RDS_*` environment namespace. Release 0.5.0 still
accepts every existing `ART_*` variable; when both forms are present, `RDS_*`
wins. Persistent `/data`, the SQLite database, RustDesk identity keys, JWT
issuer/audience values, sessions and managed-client schema identifiers are not
renamed. Keeping these protocol and storage identifiers stable prevents forced
logout, device identity changes and data migrations during the product rename.

Recommended RouterOS names:

- container: `rustdesk_server_routeros`
- interface: `veth-rustdesk-server`
- environment list: `RUSTDESK_SERVER_ENV`
- mount list: `rustdesk_server_data`
- root directory: `/Containers/rustdesk_server_routeros`

The existing persistent source directory may stay mounted at `/data` under its
old RouterOS path. Renaming or copying it is not required and should never be
done while SQLite or the RustDesk services are running.
