# Upgrade to 2.0

Version 2.0 keeps the existing `/data` layout and performs additive database migrations during API startup. Do not rename `art.db` or any existing secret/key file: those legacy filenames are intentionally retained to preserve server identity and active installations.

## Before upgrading

1. Download an on-demand database backup from **Backups** and store it outside the RouterOS container mount.
2. Preserve the complete `/data` directory, especially `secrets/id_ed25519`, `secrets/jwt.secret`, `secrets/mfa.secret`, branding, and backups.
3. Record the currently deployed immutable image tag. Do not use `latest` as the rollback reference.
4. Ensure the `/data` mount remains writable by UID/GID `65532`.

## Upgrade

Pull `blackxdog/rustdesk-server-routeros:2.0.0`, keep the same environment and mount, then recreate the container. The first startup may take slightly longer while indexes and additive columns are created. Do not run two application versions against the same SQLite file.

After startup verify:

- the administration login and an ordinary RustDesk Client login;
- a new authenticated connection and a denied logged-out connection;
- **Overview → Production diagnostics**;
- **Support → Download bundle**;
- device inventory, active sessions, audit, relay heartbeat, and address-book synchronization.

## Rollback

Stop 2.0 before rollback. Restore the pre-upgrade SQLite backup through the documented restore workflow or replace the stopped database with the saved snapshot, then start the previously recorded immutable image tag. Never attach an older and newer API instance to the same SQLite database concurrently.
