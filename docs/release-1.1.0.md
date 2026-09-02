# RustDesk Server RouterOS 1.1.0

Release 1.1.0 adds managed backup and disaster-recovery operations for SQLite deployments.

## Highlights

- Automatic online snapshots with `RDS_BACKUP_INTERVAL` (default `24h`) and `RDS_BACKUP_RETENTION` (default `14`).
- New **Backups** console for creating, listing, downloading, and deleting validated snapshots.
- Bounded restore upload with schema validation, SQLite `quick_check`, and persistent SHA-256 verification.
- Restart-time atomic recovery while preserving the replaced database and its WAL/SHM files in a timestamped pre-restore directory.
- Dedicated `backup.read` and `backup.write` RBAC permissions plus immutable audit events.

The restore flow is available only in SQLite mode. A staged restore is intentionally not applied inside the running API process: restart the container after staging, then check `/data/backups/restore.last` and application health.
