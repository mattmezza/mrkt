# Engine handoff

Implemented the shared `Open`, `Close`, `Authenticate`, `Do`, and `Tick` application surface with SQLite WAL, foreign keys, a single connection, busy handling, and append-only migrations. A missing database requires `Initialize`; initialization requires an admin token and creates installation plus hashed token atomically. `Recovery` forces outbound pause. `installation/resume` requires `{"reconciled":true,"note":"..."}` and writes an audit record.

`Authority` adds `Public bool`. Public authority is restricted to consent subscribe/confirm/unsubscribe, approved asset metadata, and signed feedback ingestion. Project API tokens are SHA-256 indexed, scoped, revocable, and tenant checked. Scope names are `read`, `config`, `operate`, and `send`.

Core operation inputs:

- `projects/create`: `{id,name}` (lowercase artifact-safe slug).
- `tokens/create`: `{name,scopes}`; plaintext token is returned only at creation.
- `contacts/create`: `{external_id,email,name,locale,timezone,attributes}`; upsert never grants consent.
- `consent/subscribe`: `{email,list,name?,locale?,source?,source_ip?}` and always returns a generic accepted response. Durable hourly project/IP/address limits and a ten-minute resend cooldown apply.
- `consent/confirm`: `{token}`. Tokens expire after 24 hours and are single-use. Confirmation atomically confirms consent, pins subscription enrollments, emits outbox state, and had already atomically queued its confirmation email.
- `consent/unsubscribe`: public `{token}` or authenticated `{contact_id,list}` / `{contact_id,project_wide:true}`. List unsubscribe cancels journeys for that list; project-wide unsubscribe creates durable suppression evidence.
- `releases/plan|deploy`: `{manifest,expected_release,allow_destructive}`. Deploy validates every S3 reference, uploads the canonical manifest by digest, uses release-pointer CAS, preserves old public assets and retired list definitions, and returns an unchanged success for an identical active digest.
- `releases/rollback`: `{release_id,expected_release}` changes only the active pointer.
- `events/create`: `{key,type,contact_id?,payload,occurred_at?}`. Keys deduplicate per project, contact ownership is validated, enrollments pin releases, and broadcast audiences freeze at ingestion.
- `operations/pause|resume`: pause accepts `{reason}`.

`Tick` serializes local execution, advances durable sequence steps, leases jobs, marks expired dispatch leases uncertain, sends confirmations, renders content from pinned S3 sources, stores frozen rendered JSON in S3, and dispatches sequence/broadcast mail. Eligibility is checked for the exact list, including suppression and project/recovery pauses, immediately before creating dispatch intent. Transient attempts retain message IDs and frozen content; uncertain outcomes never retry automatically. Outbox processing is called before mail jobs.

Tests in `engine_test.go` use real SQLite files and the artifact memory store. They cover explicit initialization, hashed authentication, tenant isolation, deploy/artifact verification, generic double opt-in, token capture and confirmation, pinned welcome execution, unsubscribe preventing later sequence work, and audited recovery resume.

Validation run after integration: `go test ./...` passed, and `go test -race ./internal/engine` passed (4.985s test runtime after the race build cache was warm). The execution regression also forces a transient SMTP result and proves the retry preserves the same Message-ID and frozen delivery record.

Additional completed operations include typed `Event.*` condition/render hydration, exit checks during scheduling and at final dispatch, deterministic simulation routing, localized English/Italian/Turkish confirmation mail, token-scoped preferences, strict bounded CSV preview/import/export, conservative usage/retention/GC dry-runs, detailed enrollment explanations, and preview/apply active-enrollment migration that preserves completed-step delivery records. Recovery marker detection forces pause after restore. List purpose or policy changes are destructive plans and, when explicitly applied, move existing confirmed consent to pending with immutable consent history.

Known limitations: confirmation wording is built in rather than release-owned content; artifact GC intentionally provides dry-run evidence only because backup-retained artifact reachability is unavailable to the application. Retention applies bounded payload redaction and safe expired-row pruning while preserving event identity/key tombstones. Public asset byte streaming belongs to HTTP/artifact integration. Transport execution is one concurrent dispatcher per process; SQLite claim CAS coordinates ordinary multiple-process workers, while cross-host recovery fencing remains an operator/Litestream concern. Stream selection is validated at deploy, frozen by transport ID in delivery intent, rate limited durably, and never switches providers on retry; five transient outcomes in ten minutes open an audited project circuit.
