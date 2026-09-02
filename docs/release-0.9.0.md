# RustDesk Server RouterOS 0.9.0

Release 0.9.0 turns the audit tab into a production security and connection investigation console.

## Audit explorer

- database-side filters for event, result, user UUID, target RustDesk ID, IP address, search text, and time interval;
- bounded 50-row web pages with exact totals and previous/next navigation;
- dynamic event-type facets based on the persisted audit history;
- quick 24-hour, 7-day, and 30-day investigation periods;
- full structured event details including immutable metadata.

## Connection analytics

- filter-aware totals for allowed connections, denied connections, failed logins, and all matching events;
- aggregate queries execute in the database rather than loading audit records into browser memory;
- additional indexes cover time, type, result, actor, and target investigations on SQLite and PostgreSQL.

## Export and security

- CSV exports retain all active filters and stream records in bounded 500-row database pages;
- exports are capped at 100,000 records per request and use UTF-8 BOM plus spreadsheet formula-injection protection;
- every query, summary, detail, and export route remains protected by `audit.read` RBAC permission;
- query lengths, dates, limits, and offsets are validated before repository access.

Upgrading from 0.8.0 requires no environment or mount changes. New indexes are created automatically and existing audit events remain unchanged.
