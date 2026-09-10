# Security

This initial release is self-hosted and must stay behind HTTPS for production use. Do not expose the development MinIO console or SMTP capture publicly. Run one active mrkt process per SQLite volume; local process locking cannot fence a different restored copy on another host.

API tokens are hashed. Stored transport and webhook configuration is encrypted with AES-256-GCM and project/record-bound associated data. Back up the externally provided key separately from SQLite. Prefer `_FILE` secret inputs and credential profiles with mode 0600; never put credentials in a manifest. Rotate/revoke API tokens with the token API. Existing sessions reauthenticate against token revocation on every request.

Public subscription calls require server-side Turnstile validation unless explicit development bypass is enabled. Rate accounting persists in SQLite. Contact imports never grant consent. Template variables are untrusted and HTML-escaped; uploaded HTML is previewed in a sandbox. Public images use an explicit retained inventory, while templates and attachments remain private.

SMTP acceptance is not proof of inbox delivery. Uncertain attempts require reconciliation. Restored instances pause outbound email and webhooks until an audited operator resume. A restored backup may predate an unsubscribe, complaint or accepted message. See [operations](docs/operations.md).

Report vulnerabilities privately to the repository owner through the private repository’s security advisory feature if enabled. Do not include real recipients, credentials or exploit payloads in a public issue. This project makes no blanket legal-compliance certification.
