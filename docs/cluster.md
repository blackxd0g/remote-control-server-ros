# API cluster operation

Every API instance has a stable identifier derived from `/data/secrets/node.id`.
Set `ART_NODE_ID` only when an orchestrator already provides a stable unique
identity. `ART_ADVERTISE_ADDRESS` is optional and is displayed to administrators;
it must identify the individual API instance rather than a shared load balancer.

Node heartbeats and renewable leases are stored in the configured database.
PostgreSQL is required for a multi-host production cluster. SQLite remains the
single-host RouterOS mode and must not be mounted concurrently by several
containers.

The following singleton workers use independent leases, so failure of one task
does not transfer ownership of unrelated tasks:

- `relay-monitor`
- `webhook-delivery`
- `backup-scheduler` (SQLite mode)

Each owner renews at one third of the lease lifetime. A worker context is
cancelled when renewal fails or ownership changes; another healthy node can take
over after expiry. Webhook event ingestion and automation evaluation run on every
API node because their source event stream is local, while delivery is the leased
singleton.

`GET /api/admin/cluster` requires `infrastructure.read` and returns active nodes
seen within 90 seconds plus current leases. The same counts are visible through
the customizable dashboard.
