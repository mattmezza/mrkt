# Environment reference

The server rejects a missing database unless explicitly initialized. Production also rejects an invalid public URL, missing encryption key, or absent required S3 backend. Boolean switches are explicit; development does not automatically mean challenge bypass.

| Variable | Meaning |
| --- | --- |
| `MRKT_DB_PATH` | Local SQLite path; Compose uses `/data/mrkt.db`. WAL/SHM and recovery marker share this volume. |
| `MRKT_LISTEN` | HTTP listen address, default `:8080`. Place behind a TLS reverse proxy. |
| `MRKT_PUBLIC_URL` | Canonical origin for public confirmation/unsubscribe/assets; HTTPS required outside development. |
| `MRKT_ENCRYPTION_KEY` | Exactly 32 random bytes, encoded as `base64:...`; stored separately from backups. |
| `MRKT_ADMIN_TOKEN` | At least 32 random characters for explicit production initialization; token hash is stored. Never put a real token in source. |
| `MRKT_FROM` | Administrator-controlled default SMTP sender. |
| `MRKT_RECOVERY` | Forces outbound recovery pause. A `.recovery-required` marker forces it even when this is false. |
| `MRKT_DEVELOPMENT` | Enables deliberately insecure local capture options; never enable for publicly exposed installations. |
| `MRKT_CHALLENGE_BYPASS` | Explicit development-only challenge bypass. |
| `MRKT_TURNSTILE_SECRET` | Server-side Cloudflare Turnstile verification secret. Missing challenge configuration fails closed. |
| `MRKT_TURNSTILE_HOSTNAME`, `MRKT_TURNSTILE_ACTION` | Expected verification result hostname/action. |
| `MRKT_TRUSTED_PROXIES` | Comma-separated trusted proxy CIDRs for forwarded source IPs. Never trust arbitrary client forwarding headers. |
| `MRKT_WEBHOOK_DEVELOPMENT_ALLOWED_HOSTS` | Exact comma-separated `host:port` local webhook destinations; only effective for explicitly development-configured endpoints. |
| `MRKT_S3_BUCKET`, `MRKT_S3_REGION`, `MRKT_S3_PREFIX` | Private application artifact destination, distinct from backups. |
| `MRKT_S3_ENDPOINT`, `MRKT_S3_PATH_STYLE` | Optional S3-compatible endpoint and path-style flag. Use HTTPS in production. |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` | Standard AWS SDK credential chain is supported; workload identity is preferable where available. |
| `MRKT_SMTP_HOST`, `MRKT_SMTP_PORT` | Default SMTP submission endpoint; project transport registries can select provisioned streams. |
| `MRKT_SMTP_TLS_MODE` | `starttls` or `tls` in production; `plain` is development-only. |
| `MRKT_SMTP_USERNAME`, `MRKT_SMTP_PASSWORD` | Optional submission credentials, encrypted when stored in project transport registry. |
| `MRKT_INSTALLATION_ID` | Stable backup namespace, independent of project IDs. |
| `LITESTREAM_BUCKET`, `LITESTREAM_PREFIX`, `LITESTREAM_REGION` | Backup destination used by the sidecar. |
| `LITESTREAM_ENDPOINT`, `LITESTREAM_PATH_STYLE` | Optional S3-compatible backup endpoint. |
| `LITESTREAM_ACCESS_KEY_ID`, `LITESTREAM_SECRET_ACCESS_KEY` | Separate Compose-side replication credentials, mapped to sidecar AWS variables. |
| `MRKT_URL`, `MRKT_TOKEN`, `MRKT_CONFIG` | CLI/MCP endpoint, credential, and optional secure profile-file path. |

Server secret values support a corresponding `_FILE` variable for a bounded file read. Compose secret files must be explicitly mounted and `_FILE` variables passed in a local override. `MRKT_CONFIG` profiles must have mode `0600`. Do not print environment dumps in incident reports.

For generated production secrets, use a password manager or `openssl rand -base64 32` and store the result privately. Changing the encryption key without re-encrypting existing registry records makes them unreadable. Keep old keys alongside the recovery configuration for old snapshots; do not overwrite your only recovery key.

With the application stopped and the normal database/configuration mounted, `mrkt server rotate-key` reads `MRKT_NEW_ENCRYPTION_KEY[_FILE]` and re-encrypts registry records atomically. Update the configured key before restarting and preserve the prior key for old backups. `mrkt server rotate-admin` similarly reads `MRKT_NEW_ADMIN_TOKEN[_FILE]`, revokes old installation administrator credentials, and records an audit entry; project tokens are unaffected. Both commands require exclusive local volume ownership and do not start outbound workers.
