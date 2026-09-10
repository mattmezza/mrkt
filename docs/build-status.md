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

Contracts established; integrated application compiles. Strict manifest tests pass; client/CLI/MCP targeted tests and skill validator pass, including official SDK stdio subprocess smoke. Infra reports real isolated Litestream restore integration passed (review report in deploy tests/infra handoff). Docker MinIO and Mailpit now running. Root generated assets/brand/message-sequence.png with built-in generator, visually inspected. UI compiled Tailwind 4.3.3, htmx 4.0.0 (npm next tag), Alpine CSP 3.17.2; actual browser checks pending.

## Active work / handoff anchors

- Engine worker owns internal/engine EXCEPT root-owned registry.go/outbox.go/feedback.go. Root added real encrypted registries, domains checks, transport resolution, durable signed outbox dispatch. Engine migration adds webhook_deliveries/provider_feedback and Config.From, Tick calls tickOutbox.
- Engine reviews in progress: required-list consent at final dispatch after S3; transient retry reuses frozen message and stable ID; scoped event ownership; once reentry NULL uniqueness; retain public assets across deploy; transactionally enqueue DOI mail; safe init before creating DB; typed conditions/exit/Event variables; configurable sender.
- Client worker continuation complete named CLI commands, four distinct workflow presets, secure credential profiles; reassigned real acceptance script and browser checks, exclusive scripts/acceptance.mjs, tests/browser, playwright config, docs/browser-checks.md.
- Infrastructure worker owns Docker/Compose/deploy runbook and artifact/mail/security. Implemented SNS signature adapter; root must wire feedback ingestion. Infra doing real Docker multi-stage build/init, additional security review and integrations.
- Root owns manifest/httpapi/UI/cmd, dependencies, docs, brand, registry/outbox/feedback. HTTP public unsubscribe accepts standard query token. UI uses generic record tables; align column names with actual API; recovery form note field must be aligned. Root must implement feedback.go and public feedback route; OpenAPI/schema/docs still pending; no CI/push yet.
- Docker Mailpit publishes 8025 only (SMTP can access container IP or add localhost1025 for local acceptance). MinIO localhost9000 credentials mrktdev/mrktdev-secret synthetic dev only.

Required gates B–E remain open. Do not describe this as production-ready.

## Resumption

Read prompt.md, this file, docs/contracts.md and worker handoffs. Preserve all work. Complete integrated vertical slice before broadening and keep actual test results here.
