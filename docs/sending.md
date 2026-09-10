# Sending, feedback, and domains

mrkt submits messages to an administrator-configured MTA. It does not operate a direct-to-MX queue, create a provider identity, sign DKIM itself, or guarantee inbox delivery. The private MTA or provider owns DKIM signing, reputation, PTR, and onward delivery.

## SMTP setup

Provision a verified domain in the project's `domains` registry, publish the returned `_mrkt.<domain>` ownership token, and run its `check` action. Set the real DKIM selectors and return-path supplied by your MTA/provider. DNS inspection reports found SPF, DKIM, and DMARC values, not a fabricated deliverability score. Detailed DNS/TLS and SES steps are in [the operations runbook](operations.md).

An installation administrator creates `transports` with:

```json
{
  "name": "primary-marketing",
  "config": {
    "host": "smtp.example.com",
    "port": 587,
    "tls_mode": "starttls",
    "username": "provisioned-smtp-user",
    "password": "SUPPLY_PRIVATELY",
    "from": "Example <newsletter@example.com>",
    "provider": "generic",
    "stream": "marketing",
    "rate_per_minute": 60,
    "allow_private": false
  }
}
```

Supply real secrets via stdin or a protected secret file, never command-line arguments. Private SMTP is permitted only with explicit administrator `allow_private`; insecure plain SMTP is development-only. TLS certificates are verified by default. The administrator owns the trust boundary of a chosen SMTP hostname, including its DNS changes.

`transports/{id}/check` performs TLS/authentication and NOOP, without MAIL/RCPT/DATA. It verifies submission connectivity, not inbox placement. The dispatcher is intentionally serial; each selected transport has durable minute accounting. Five recent transient failures open a project circuit for operator review. There is no provider switching on retries.

Optional manifest `project.transport` and `project.domain` reference separately provisioned registry IDs. Sequences/broadcasts may set `stream`; an explicit transport ID takes precedence, otherwise the matching configured stream is selected. Selected transport IDs are pinned in delivery intent. Disabled/missing pinned transports fail closed, rather than silently using another provider. Default installation transport configuration must also remain stable when reconciling retries.

## Feedback

For SES SMTP, provision the actual regional SES SMTP credentials, verified sending identity, and an SNS notification topic. Set `provider:"ses"` and its exact `sns_topic_arn`. SNS messages are authenticated against the configured topic and SNS-hosted signing certificate; no subscription URL is followed automatically. Complete SNS subscription setup as the AWS administrator. Verification relies on authenticated HTTPS retrieval from the restricted AWS SNS certificate hostname plus the RSA message signature.

For a private MTA, POST a normalized JSON object to `/feedback/{project}/{transport}`:

```json
{"id":"mta-stable-event-id","type":"bounce","message_id":"original-message-id@mrkt","email":"reader@example.test","occurred_at":"2026-09-10T12:00:00Z"}
```

Use `delivered`, `bounce` (permanent), or `complaint`. Header `X-Mrkt-Timestamp` is Unix seconds; `X-Mrkt-Signature` is hex HMAC-SHA256 using the transport's creation-time feedback secret over `timestamp + "." + exactBodyBytes`. The receiver allows a ten-minute replay window and persists event identity/fingerprint. Original Message-ID and recipient must match this project's delivery. Duplicate IDs with different bytes conflict; late delivery cannot clear a complaint or bounce. Retry callbacks during recovery; outbound remains paused. Generic SMTP without this adapter has no automatic complaint or inbox-delivery telemetry.

Any complaint pauses the project; sufficient observed permanent bounces also trigger a pause. An operator reviews source quality, recipient consent, and provider policy before resuming. Never evade suppression through a new transport.

## Outgoing business webhooks

Create an HTTPS endpoint in `webhooks`. The encrypted signing secret is revealed once. Version-1 envelopes contain stable event `id`, `project_id`, `type`, `occurred_at`, correlation/causation IDs and `data`. Subscribers must deduplicate the stable event ID and must not assume global ordering.

`X-Mrkt-Signature: v1=<hex>` signs timestamp-dot-exact-body bytes with HMAC-SHA256. `X-Mrkt-Event-ID` stays stable; `X-Mrkt-Attempt-ID` changes per retry/replay. See [working consumer examples](../examples/). HTTP is at-least-once. Acknowledgement loss can cause repeat calls; retries eventually dead-letter, and replay preserves the original envelope and event identity. Each attempt is inspectable.

Rotation changes the key used for new attempts immediately. Consumers should accept old and new keys during their controlled overlap window, then discard the old key. mrkt does not retain the previous signing secret indefinitely. Secret creation/rotation does not accept generic replayable idempotency keys, so store the once-revealed secret safely.

Private/metadata destinations, redirects, and rebinding to private addresses are blocked. HTTP to a private capture endpoint requires both development configuration and an exact administrator-configured host:port allowlist. Recovery and project pauses stop business webhooks too; feedback ingestion remains available.

## Localized email

Messages resolve locale per variant (`it-CH → it → project default`) and freeze their rendered subject/HTML/text for retries. The `date` function accepts RFC3339 timestamp, locale, and IANA timezone; `number` accepts finite value, locale, and 0–6 decimals. Examples: `{{date .Event.starts_at .Locale "Europe/Zurich"}}`, `{{number 1234.5 .Locale 2}}`. Durations advance elapsed UTC time; local date rendering applies DST at the message timestamp.

Only embedded bounded email CSS is inlined, using [go-premailer](https://github.com/vanng822/go-premailer); there are no remote stylesheet fetches. Script/iframe/form/link elements and event handlers fail validation during rendering. Use conservative email markup, usable plain text, and private attachments within the total encoded message limit. The admin Tailwind stylesheet is never sent to recipients. Localized numbers use [Go's x/text message package](https://pkg.go.dev/golang.org/x/text/message).
