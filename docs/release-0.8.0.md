# RustDesk Server RouterOS 0.8.0

Release 0.8.0 adds production device lifecycle and fleet-management operations while retaining database and official-client compatibility.

## Device lifecycle

- active and archived inventories with counts and filters in the web console;
- archive and restore without deleting device history;
- protected permanent deletion available only for archived devices;
- automatic rediscovery when a deleted RustDesk ID sends a later heartbeat;
- compatible additive database migration for the archive timestamp.

## Fleet operations

- transactional bulk device-group assignment;
- bulk tag addition and removal while preserving unrelated tags;
- UTF-8 CSV export with BOM, stable headers, and spreadsheet formula-injection protection;
- bounded CSV import for aliases, groups, and tags with semicolon/comma detection;
- all-or-nothing import validation for duplicate IDs, unknown or archived devices, invalid groups, excessive rows, fields, and tags.

## Security and audit

- every write route requires `devices.write`;
- lifecycle, bulk changes, successful imports, and rejected imports are audited;
- imports are limited to 8 MiB and 5,000 device records;
- archived records cannot be modified by bulk operations or imports.

No environment or mount changes are required when upgrading from 0.7.0. The existing persistent database, secrets, keys, and legacy-compatible file names remain unchanged.
