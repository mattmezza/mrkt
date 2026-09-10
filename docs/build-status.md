# Build status

The acceptance contract is `prompt.md`. This file records verified progress, not aspirations.

## Environment

- 2026-09-10: fresh repository with existing prompt commit preserved. Authenticated GitHub identity mattmezza; created private mattmezza/mrkt using gh after GitHub connector identity verification (connector repository call rejected argument binding; creation tool unavailable).
- Go 1.27.0, Node 25.8.0, Docker 29.7.2, Compose 5.5.0, Chromium installed. Docker daemon initially unavailable; requested owner start it.
- Runtime offers gpt-6-astra/high and gpt-5.6-sol/low selections for spawned agents. Root cannot change/attest its own model configuration. Three concurrent worker slots available, within requested maximum five. Workers explicitly configured sol/low; no recursive delegation.

## Architecture / ownership

- Coordinator: shared contracts, manifest, HTTP API, executable wiring, dependencies, integration/acceptance.
- Core worker: internal/engine only, exclusive SQLite schema/migration ownership, consent/releases/jobs/events/outbox.
- Infrastructure worker: internal/artifact, internal/mail, internal/security, deploy/, Dockerfile, compose files, recovery tests/runbook.
- Client worker: internal/client, internal/cli, internal/mcpserver, presets/, examples/, skills/, CLI documentation.
- UI/branding follows shared service stabilization.

## Current gate

Phase D hardening. Actual CLI → S3 → DOI capture → confirmation → welcome → one-click unsubscribe lifecycle passed; five Chromium viewport tests passed with real seeded screenshots. Actual engine-backed Litestream restore passed, including confirmed/unsubscribed state and signed bounce during recovery. Local benchmark recorded in docs/benchmark.md. Go full suite, vet, and race suite passed at the prior checkpoint. npm audit reports zero vulnerabilities. Added CSS inlining exposed x/net transitive vulnerabilities; upgraded to v0.56.0 (latest scan pending). Do not infer final acceptance from earlier passes while hardening edits continue.

## Active work / handoff anchors

- Engine worker owns internal/engine EXCEPT root-owned registry.go/outbox.go/feedback.go. Root added real encrypted registries, domains checks, transport resolution, durable signed outbox dispatch. Engine migration adds webhook_deliveries/provider_feedback and Config.From, Tick calls tickOutbox.
- Engine reviews in progress: required-list consent at final dispatch after S3; transient retry reuses frozen message and stable ID; scoped event ownership; once reentry NULL uniqueness; retain public assets across deploy; transactionally enqueue DOI mail; safe init before creating DB; typed conditions/exit/Event variables; configurable sender.
- Client worker continuation complete named CLI commands, four distinct workflow presets, secure credential profiles; reassigned real acceptance script and browser checks, exclusive scripts/acceptance.mjs, tests/browser, playwright config, docs/browser-checks.md.
- Infrastructure worker owns Docker/Compose/deploy runbook and artifact/mail/security. Implemented SNS signature adapter; root must wire feedback ingestion. Infra doing real Docker multi-stage build/init, additional security review and integrations.
- Root owns manifest/httpapi/UI/cmd, dependencies, docs, brand, registry/outbox/feedback. HTTP public unsubscribe accepts standard query token. UI uses generic record tables; align column names with actual API; recovery form note field must be aligned. Root must implement feedback.go and public feedback route; OpenAPI/schema/docs still pending; no CI/push yet.
- Docker Mailpit publishes 8025 only (SMTP can access container IP or add localhost1025 for local acceptance). MinIO localhost9000 credentials mrktdev/mrktdev-secret synthetic dev only.

## Current ownership and outstanding acceptance (latest checkpoint)

- Root: HTTP/public/session security, manifest/rendering/simulation/schema, registry/outbox/feedback/preview, verification and offline key rotation, docs/branding/dependencies/CI. Root-owned engine files additionally include transport_selection.go, key_rotation.go, verification.go, registry_test.go, outbox_test.go. Secret sessions now AEAD-encrypted. Signed real HTTP retry/replay/history tests pass. Offline key rotation tested.
- Engine worker: remaining core correctness review and regressions: event fingerprint conflicts/tombstones, once-enrollment uniqueness while preserving Event data, final DOI eligibility/recovery pause, contact deletion suppression, audited runtime transitions. Current typed filters and bounded retention are implemented. Migrations remain exclusively engine-owned.
- Client worker: UI message/locale selector, mobile collapsed navigation, contrast testing, accurate overview columns, public/API/CLI/MCP parity review. Owns ui.go/app.html/styles.css and compiled app.css, browser tests and screenshots. Root owns public.go/public template/server.go.
- Infrastructure worker: real recovery/benchmark and production runbook complete; independently adding exported-service black-box tests for broadcast/rollback/stale deploy/concurrent unsubscribe. Await root commit/push for clean-clone Compose build/start.
- Need latest full tests/race/vet/vulnerability/browser/restore runs, all primary risk regressions, schema/OpenAPI checks, complete release notes/acceptance doc and updated notices, final visual inspection and Hallmark log, commit/push complete implementation to PRIVATE repo, inspect CI and branch-protection availability. Earlier private architecture commit edebcd9 is pushed; implementation is not yet pushed.

Required final gates D–E remain open. Do not describe this as production-ready. Last user status estimate: approximately 80% toward the full prompt, not 80% of code lines.

## Resumption

Read prompt.md, this file, docs/contracts.md and worker handoffs. Preserve all work. Complete integrated vertical slice before broadening and keep actual test results here.
