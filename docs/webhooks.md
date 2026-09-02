# Webhooks

The platform can deliver cache, security, connection, device, strategy, ACL, and relay events to external HTTPS receivers.

## Delivery contract

Each request is an HTTP `POST` with an `application/json` body containing the event envelope. The receiver should validate:

- `X-ART-Event`: event type;
- `X-ART-Delivery`: stable delivery UUID used for idempotency;
- `X-ART-Timestamp`: Unix timestamp used in the signature;
- `X-RDS-Signature`: `sha256=<hex HMAC>`.

Release 0.5.x also sends the former `X-ART-Signature` header for transition compatibility. Receivers should verify `X-RDS-Signature` now; the old header is removed in 0.7.0.

The signed bytes are:

```text
X-ART-Timestamp + "." + exact request body
```

The HMAC key is the webhook secret shown once when the subscription is created. Use constant-time comparison and reject stale timestamps. Delivery can happen more than once, so receivers must deduplicate by `X-ART-Delivery`.

Non-2xx responses and network failures are retried with exponential backoff, up to six attempts. The console exposes status, response code, attempt count, and the last error. Secrets are not stored in the database: they are derived from the persistent internal platform secret and webhook ID.

## Destination security

Only HTTPS URLs without embedded credentials are accepted. Loopback, link-local, multicast, and private destinations are blocked by default, both during configuration and connection establishment to prevent DNS rebinding.

For a trusted internal receiver, explicitly set:

```text
ART_WEBHOOK_ALLOW_PRIVATE=true
```

This option should remain disabled on installations where console administrators are not also trusted network administrators.
