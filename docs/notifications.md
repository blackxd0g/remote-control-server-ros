# Administrator notifications

The Web Console notification center is a durable triage projection of selected audit events. Audit records remain immutable and authoritative; marking a notification as read never changes or deletes its source audit event.

Notifications are currently created for:

- failed sign-ins;
- device identity mismatches;
- registrations awaiting approval;
- MFA disable and administrator reset operations;
- deployment API-token creation;
- server-control commands.

Each record has a severity (`info`, `warning`, or `critical`), creation time, optional resource identifier, and persistent read timestamp. The Web Console polls the lightweight notification endpoint every 30 seconds and exposes an unread counter. Access is protected by the `audit.read` RBAC permission.

Webhook delivery remains independent. It is intended for machine-to-machine integrations, while the notification center is intended for administrator triage.
