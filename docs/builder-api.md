# Client Builder API

Client builders run outside the RustDesk Server RouterOS container. The
bootstrap bearer token stored in `/data/secrets/builder.token` is used only for
worker registration; it is not a user JWT and cannot access administrator, HBBS
or database APIs. Registration returns a unique worker credential. Only its
SHA-256 digest is persisted by the server.

All requests use HTTPS in production and include:

```http
Authorization: Bearer <builder-token>
```

## Worker lifecycle

`POST /internal/v1/builders/register` uses the bootstrap token and accepts:

```json
{
  "worker_id": "builder-linux-01",
  "name": "Linux Builder 01",
  "hostname": "builder01.example.net",
  "version": "0.1.0",
  "formats": ["portable", "installer"],
  "platforms": ["linux", "windows"],
  "architectures": ["amd64", "arm64"],
  "concurrency": 1
}
```

Its response contains the registered `worker` and a `worker_token`. The token
is returned only by registration and must be stored as a secret on the isolated
builder host. Re-registering the same worker ID rotates its credential.

`POST /internal/v1/builders/heartbeat` accepts the same document but uses the
unique worker token. The document `worker_id` must match the authenticated
worker. Workers become offline in the console after 30 seconds without a
heartbeat.

All claim, lease, payload, completion, and failure requests below use the unique
worker token. A worker cannot claim or complete work under another worker ID.

## Claim and lease

`POST /internal/v1/client-builds/claim`:

```json
{
  "worker_id": "builder-linux-01",
  "formats": ["portable"],
  "platforms": ["linux"],
  "architectures": ["amd64", "arm64"],
  "lease_seconds": 300
}
```

The API atomically assigns the oldest queued or abandoned job compatible with
all advertised formats, target platforms and architectures.
Response `204` means there is currently no work. A successful response contains
the build job with `status=leased`, `worker_id`, `attempts` and `lease_until`.
Lease duration is clamped to 60–900 seconds.

`POST /internal/v1/client-builds/{id}/heartbeat` renews ownership:

```json
{"worker_id":"builder-linux-01","lease_seconds":300}
```

`GET /internal/v1/client-builds/{id}/payload?worker_id=builder-linux-01`
returns the job and an HMAC-signed, versioned client-profile bundle. A worker
must stop processing if heartbeat or payload returns `409`.

## Completion

Upload at most 16 MiB to
`POST /internal/v1/client-builds/{id}/complete` using the artifact as the raw
request body and these headers:

```http
X-RDS-Builder-ID: builder-linux-01
X-RDS-Artifact-Name: rustdesk-managed-x86_64.exe
X-Content-SHA256: <lowercase SHA-256>
Content-Type: application/octet-stream
```

The API independently computes SHA-256, verifies the active lease and rejects
path separators in artifact names. Completion cannot overwrite a cancelled,
expired or reassigned job.

`POST /internal/v1/client-builds/{id}/fail` accepts:

```json
{"worker_id":"builder-linux-01","error":"sanitized failure message"}
```

Administrators can cancel active jobs or retry failed/cancelled jobs up to five
claimed attempts. Native artifacts larger than 16 MiB will move to streamed
object storage before native compilation is declared production-ready.
