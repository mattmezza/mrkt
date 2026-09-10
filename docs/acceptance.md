# Acceptance evidence

The acceptance contract is the root `prompt.md`. These checks use synthetic recipients and local capture systems; no real campaigns, production provider account, DNS changes, or public publication were used.

## Executed locally

| Check | Evidence |
| --- | --- |
| Go unit/integration services | `go test ./...`; actual SQLite temporary databases. |
| Static checks | `go vet ./...`, `git diff --check`. |
| Race detection | `go test -race ./...` passed at the integrated checkpoint; focused engine risk tests also repeated ten times. |
| S3 adapter | Real MinIO conditional immutable writes, metadata verification and project paths under integration build tag. |
| SMTP adapter | Real Mailpit capture plus bounded MIME, attachments, injection/classification tests and no-send TLS/auth/NOOP diagnostics. |
| Vertical slice | `node scripts/acceptance.mjs`: explicit initialization, CLI scaffold/deploy, S3 content, captured DOI, confirmation, welcome, one-click unsubscribe before delayed follow-up, and duplicate event replay. |
| Browser | Five real Chromium widths (320, 375, 414, 768, 1280), login/CSRF, seeded views, command palette, sandboxed release/delivery previews and errors. [Browser record](browser-checks.md). |
| Restore | Isolated real Litestream/MinIO restore of engine-created release, confirmed/unsubscribed consent, pending queue, encrypted registries and S3 references. Recovery marker forces pause with `Recovery:false`; signed bounce can apply during recovery. Refuses overwrite and does not initialize after missing-bucket failure. |
| Idempotency/consent | Project/key authority and fingerprint conflicts, single-effect concurrent mutation, event producer-key conflicts, retained tombstone behavior, once-journey identity, DOI single-use/recovery pause, and CSV no-consent import. |
| Dispatch boundary | Blocking artifact storage test pauses before final intent, commits unsubscribe concurrently, then releases storage: no marketing SMTP call occurs. |
| Release changes | Stale activation rejected; broadcasts deduplicate; v2 deploy/rollback preserves existing enrollment pinned to v1. |
| Business webhooks | Real allowlisted local HTTP server verifies exact-byte HMAC, retries, success on final attempt, frozen replay identity, new attempt IDs, history and recovery pause. |
| Feedback/secrets | Signed MTA dedupe/order/suppression, forged SNS rejection and RSA verification fixtures, encrypted registry tenant contexts and offline rotation. |
| CLI/MCP/skill | Local preset/command tests, official SDK in-memory and real stdio protocol smoke, installable skill validation. |
| Workload | [Recorded serial synthetic benchmark](benchmark.md), including machine/configuration and measured times. |
| Dependencies | npm audit returned zero findings. Go scan discovered vulnerable transitive HTML parsing; x/net was upgraded to v0.56.0, and the final `govulncheck` reported no vulnerabilities. |

## Final delivery gate

Clean-clone Compose build/start, the latest browser/Go/vulnerability rerun, and final real recovery drill passed. The final private push and CI inspection are tracked in [build status](build-status.md). The repository's private branch-protection endpoint returned HTTP 403 for the current plan; the repository was not made public to enable it.

SES and real internet deliverability were not exercised against an external production account. Those require operator provisioning and separately authorized sending; local captures do not establish inbox delivery or provider production readiness.
