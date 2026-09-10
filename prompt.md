# Build mrkt: coordinator instructions

You are the lead engineering agent tasked with designing, implementing, testing, documenting, and delivering **mrkt**, a self-hosted email marketing engine. Execute the build; do not stop at a plan, scaffold, mock UI, or collection of unintegrated modules.

## 1. Ownership, authorization, and working style

- Owner: Matteo Merola, GitHub `mattmezza`.
- Target repository: https://github.com/mattmezza/mrkt.
- Create the repository **private**, ready for a later MIT open-source release. Never change visibility to public during this task. If it exists, inspect and preserve its contents before integrating changes. A 404 can mean insufficient access; verify identity/access before concluding it is absent.
- Use the available GitHub plugin first. If repository creation is unsupported, use authenticated `gh` or the GitHub API where available and permitted. Do not fabricate successful creation or bypass access controls. If authentication is missing, complete local work and ask for the exact access needed to push.
- Repository creation, commits, branches, internal integration, and pushing this implementation to the private repository are authorized. Do not contact other people, send real marketing mail, register domains, purchase services, or publish publicly. Test with local capture SMTP and synthetic recipients unless separately authorized.
- Make routine engineering decisions autonomously and document consequential choices. If system dependencies require sudo, give Matteo the exact install command and ask him to run it; he may be at his computer. Continue independent work while blocked.
- Protect existing work and secrets. No force pushes over unrelated changes, secret logging, committed credentials, real subscriber fixtures, or claims of passing unexecuted tests.
- Read applicable AGENTS.md/skills and use relevant tools. Check official documentation before selecting current versions and configuration syntax. Pin versions and record them; do not assume an npm `latest` tag identifies the requested major version.

## 2. Required swarm model

Use a coordinator/worker (master–worker) pattern:

- **Coordinator: `gpt-6-astra`, reasoning `high`.** Own architecture, shared contracts, task dependencies, integration, difficult correctness decisions, review, and final acceptance.
- **Workers: `gpt-5.6-sol`, reasoning `low`.** Use at most five workers concurrently, plus the coordinator. Assign concrete bounded work with acceptance criteria and explicit file ownership.
- Verify that the runtime actually supports these model selections. Configure them explicitly where supported. If unavailable, report the limitation; never claim to be running a different model than the runtime supplies. Complete useful work with available capabilities while requesting a configuration change only when necessary.
- Workers report to the coordinator and do not recursively create more agents. The coordinator must do substantive architecture/review/integration work rather than merely relay tasks.
- Use separate worktrees/branches where possible. With a shared filesystem, allocate exclusive file ownership. Centralize database migrations, dependencies, and shared schemas under the coordinator or one designated owner.
- Each assignment includes context, interfaces, files, tests, dependencies, and the required handoff. A handoff includes commits, actual checks run, limitations, and integration notes.
- Persist decisions, task status, and resumable next steps in `docs/build-status.md`. Keep progress updates concise. Do not lose the original objective across context compaction.

Suggested worker roles, activated as dependencies permit:

1. Storage, schema, artifact releases, Compose, and Litestream recovery.
2. Sequence/broadcast execution, event ingestion, and durable jobs.
3. SMTP adapters, domain checks, subscriptions, suppression, and abuse prevention.
4. REST contract, CLI, MCP, presets, and official agent skill.
5. UI, localization surfaces, branding, and user documentation.

After integration, reassign workers to independently review another worker's subsystem, especially tenant isolation, send correctness, and disaster recovery. Do not delegate all shared interfaces before establishing their contracts.

## 3. Product and boundaries

mrkt will serve multiple independent projects, initially `flowrent.app`, `cheerful.cards`, `mimux.dev`, and `humux.dev`. Each consuming repository owns its marketing content and declarative configuration. A CLI publishes it much like a website deployment. mrkt stores and executes the resulting releases and exposes all remote capabilities through REST, CLI, and MCP.

The core workflow is: scaffold → edit locally → preview/simulate → validate → plan → deploy → inspect/operate.

Git owns configuration and content. mrkt owns live contacts, consent, subscriptions, suppression, events, delivery records, and execution progress. Deployment and rollback must never restore obsolete consent, reverse unsubscribes, reset enrollments, or resend historical campaigns implicitly.

Implement a complete usable first release, including all requirements below. Keep it focused: no billing platform, public SaaS onboarding, visual drag-and-drop builder, arbitrary workflow scripting, built-in LLM runtime, autonomous AI optimization, or multi-region active-active operation. Ordinary operation must not require an LLM or paid agent service.

## 4. Fixed technology choices

- Go modular monolith for application services, HTTP server, scheduler, workers, CLI, and MCP integration.
- Go `html/template` for UI/HTML rendering and `text/template` for text/subjects where appropriate. Keep templates out of handler strings.
- htmx **v4**, Tailwind CSS **v4 compiled at build time**, and Alpine.js for small local browser interactions. Verify official v4 docs and package tags. No SPA framework or runtime CSS CDN.
- SQLite with WAL, foreign keys, controlled writing, short transactions, busy handling, versioned migrations, and real database integration tests.
- **S3-compatible object storage is the default and required production artifact backend from day one.** No production local-disk artifact backend is required. A local S3-compatible service may be provided in the development/test Compose profile.
- Litestream for asynchronous SQLite disaster-recovery replication to object storage.
- Docker and Docker Compose for deployment. Compiled assets and templates may be embedded in the Go binary. No Node runtime needed in the production image.
- Prefer minimal maintained dependencies with licenses compatible with distributing mrkt under MIT. Document dependencies and notices. Verify SMTP, S3, MCP, migrations, and SQLite driver choices against current primary documentation.
- REST handlers and HTML handlers call shared application services; the UI need not HTTP-call its own API. CLI and MCP use the supported API without direct database access.

## 5. Persistence and disaster recovery: mandatory design

### Artifact storage

Store pushed manifests, template sources, HTML/text, translations, styles, images, attachments, release inventories, and required rendered-message artifacts in S3-compatible storage. Support endpoint, region, bucket, prefix, path-style configuration, TLS, and normal credential mechanisms. Stream uploads/downloads and enforce limits; avoid unbounded buffering or local caches.

Use immutable, content-addressed objects scoped by project. A possible layout is:

```text
mrkt/<environment>/artifacts/<project-id>/objects/<sha256>
mrkt/<environment>/artifacts/<project-id>/releases/<release-id>/...
mrkt/<environment>/backups/<installation-id>/sqlite/...
```

The **same bucket may hold artifacts and Litestream backups**, but prefixes, permissions, retention, and cleanup are separate. Keep the bucket private. The application artifact credentials must not be able to delete backups; Litestream credentials must be scoped to its backup area. Do not hardcode an S3 object layout inside Litestream's own replica prefix.

- Validate referenced files, hashes, sizes, MIME types, path traversal, symlinks, archive expansion, and tenant ownership.
- Upload and verify required artifacts before atomically activating a database release record. Failed deploys cannot become active. Use an optimistic release precondition to avoid applying stale plans.
- Repeated identical deployments are harmless. Missing resources do not silently erase live data; destructive configuration changes must be explicit in the plan.
- Images intentionally published for email use have stable HTTPS asset URLs through a restricted serving endpoint or configurable CDN origin. Never expose templates, manifests, attachments, or backup prefixes through that endpoint. Serve only an explicit public-asset inventory with safe content types and security headers.
- Public email image URLs must not expire after minutes or disappear on the next deployment. Keep image publication separate from private download features.
- Attachments are private and embedded into MIME messages by default. Enforce total encoded message size limits, not just raw file size.
- Bound caches, temporary disk usage, log growth, and database event retention. Expose usage and configurable retention; object storage is not infinite free storage.
- Garbage collection is dry-run first and reference-aware. Protect active/pinned releases, queued messages, running sequences, public image retention, and **every database snapshot still supported for restore**. Never delete an artifact needed by a retained recoverable database state. Prefer conservative retention over premature reclamation.

### SQLite and Litestream

Be explicit: the application image is disposable, but SQLite still uses local disk. Use a managed Compose named volume for the live database and WAL files; bulk artifacts never accumulate there. Litestream does not provide a shared database, synchronous replication, zero data loss, or safe multi-writer failover. Do not mount object storage as SQLite's filesystem.

- Run one active application instance and one replication owner for the database; prohibit scaling this Compose service across independent hosts. Document fencing the old instance before recovery. An in-database lease cannot fence a different restored copy.
- Use a pinned Litestream version with verified configuration. A sidecar sharing the SQLite volume is acceptable. Implement deliberate restore/start/replicate ordering and graceful shutdown.
- A normal restart uses the existing database. Restore only through an explicit disaster-recovery path when the local database is missing or an operator requested recovery. Never overwrite an existing database automatically.
- Distinguish a truly new installation from a failed restore or unavailable bucket. Require explicit initialization; a backup access error must never silently start an empty production instance.
- Monitor database/WAL size, backup health, last successful replication, and recovery status. Document measurable RPO/RTO behavior rather than promising a fixed data-loss window despite network outages.
- Restore tests must use an isolated empty volume and real test object storage, restore the selected backup, check database integrity and artifact references, and verify consent, queues, and release records.
- **Restored instances start in recovery mode with outbound SMTP and business webhooks paused.** A backup may predate an unsubscribe, complaint, or accepted send. Provide a reconciliation/runbook and explicit audited resume action; do not blindly replay overdue sends. Resuming requires the operator to assess lost state and uncertain delivery, not merely pass a database integrity check.
- Retryable callbacks must remain ingestible or clearly return retryable failures during recovery. Document what cannot be reconstructed. Any optional external safety journal must state its atomicity and reconciliation limitations; do not invent exactly-once guarantees.
- Backup credentials/encryption keys and operational configuration need an independent recovery procedure. Sharing one bucket is acceptable initially but is not protection against loss of the whole account/bucket; document an optional off-account copy without making it required infrastructure.

## 6. Projects, contacts, consent, and security

- Strong project isolation across every query, object reference, API token, event, webhook, domain, list, and execution record. Derive scope from authenticated authority and validate resource ownership.
- Global installation administration plus project-scoped credentials with read, configuration, operation, and send privileges. Hash API tokens; reveal secrets only at creation. Support rotation/revocation. Use secure sessions and CSRF protection for UI actions.
- Contacts have stable external IDs where supplied, email, locale, timezone, typed attributes, and list memberships. Define email normalization carefully; do not perform provider-specific dot/plus rewriting.
- Maintain pending/confirmed/unsubscribed states separately from bounce/complaint suppression. Consent is project/list/purpose scoped, with source, timestamp, and policy version. Contact upsert or import must not grant marketing consent.
- CSV import/export with previews, validation, explicit consent mapping, bounded processing, safe exports, and no resubscription through routine imports.
- Double opt-in with expiring single-use tokens, generic public responses, resend cooldowns, confirmation audit history, and explicit re-consent behavior.
- Implement visible unsubscribe, standards-based one-click unsubscribe, and localized preferences. Respect list-level and project-wide choices; never infer cross-brand consent.
- Retain only necessary suppression evidence when deleting contact data and document the behavior; no blanket legal-compliance claims. Provide export, deletion, and configurable retention.
- Encrypt stored provider/webhook secrets using an externally supplied key, with rotation/recovery documentation. Never expose keys through template variables or logs.

## 7. Anti-abuse and safe public endpoints

- Pluggable challenge verification with a working Cloudflare Turnstile implementation; verify server-side, including intended hostname/action where configured. Production must not silently bypass validation because a secret is missing. Explicit development-only bypass is acceptable.
- Limits per source IP, target address, project, and globally. Trust forwarded headers only from configured proxies. Include confirmation resend cooldowns, bounded pending subscriptions, token expiration, and suppression checks before confirmation sending.
- Prevent confirmation-email bombing: requests cannot inject arbitrary subject/body/URLs into confirmation messages. CAPTCHA alone is insufficient.
- Record abuse signals, project quotas, bounce/complaint rates, and automatic sending pauses with clear reasons and audited resume. Use durable enough rate accounting that a routine restart does not reset all abuse protection.
- Isolate public subscription operations from authenticated event/contact APIs. Public users cannot assert confirmed consent or choose another project.
- Defend webhook delivery against SSRF, redirects to private addresses, DNS rebinding, and metadata endpoints. Private SMTP servers are an intended feature: permit them only via explicit administrator-controlled transport configuration, never via public request input.
- Treat uploaded templates and webhook payloads as untrusted data. No arbitrary shell/network/filesystem template functions, raw HTML trust for contact values, or agent instructions embedded in subscriber content. Bound rendering complexity and output size.

## 8. Declarative releases, sequences, broadcasts, and localization

Design and publish a versioned strict YAML schema, with examples and machine-readable validation errors. Unknown keys fail validation. Support project metadata, lists, content, locales, sequence definitions, broadcast triggers, and references to separately provisioned transports/domains/secrets. Credentials never belong in manifests.

Minimum workflow primitives:

- Entry from confirmed subscription or authenticated business event.
- Send, delay, typed conditions/branches, exit conditions, and completion.
- Explicit enrollment/re-entry/deduplication policies and stable step IDs.
- Both project events (`article.published`) and contact events (`booking.completed`), with distinct schemas/audience semantics.
- Sequences for per-contact journeys; broadcasts for a selected audience. Freeze/document broadcast audience selection time, then re-check eligibility at dispatch. Duplicate project events must not create duplicate broadcasts.
- No arbitrary user code execution or unbounded loops. Bound execution and reject invalid references/cycles.

Execution rules:

- Pin existing enrollments to immutable sequence versions by default; new enrollments use the active release. Migration of active enrollments is explicit and previewable. A completed step is not repeated by redeployment or rollback.
- Re-check consent, suppression, pause state, and relevant exit conditions at the final dispatch boundary. Define and test concurrent unsubscribe versus dispatch behavior; an email already handed to SMTP cannot be recalled.
- Persist events, jobs, leases, attempts, scheduling timestamps, execution history, and transactional outbox records. Expired worker leases are recoverable. Do not sleep a goroutine for a multi-day sequence delay.
- No database transactions held across SMTP, S3, or HTTP operations. Lease durations/renewals and dispatch attempt ownership must prevent two ordinary workers sending the same claimed job.
- Idempotency keys have scopes, request fingerprints, and conflict behavior. Record stable message IDs and correlation IDs. SMTP cannot guarantee exactly-once delivery: ambiguous acceptance is a distinct state with deliberate retry/reconciliation policy.
- Support pause/resume, cancel, and explain. Explain shows version, conditions, next step/time, suppression, attempts, and why a contact did or did not receive a message.
- Provide deterministic simulation with a fake clock and synthetic events; it performs no external sends.

Localization (i18n):

- Per-project supported/default locales and explicit fallback such as `it-CH → it → en`.
- Localized subject, HTML/text, confirmation, unsubscribe, and preferences. Provide English, Italian, and Turkish sample content to exercise actual Unicode and variable handling.
- Separate locale from timezone. Store scheduling timestamps consistently, define DST behavior, and render localized date/number values.
- Validate missing variables/translations and distinguish deliberate fallback from errors. Resolve locale when preparing each message and freeze its rendered content for retries.
- Email styling uses conservative email HTML and CSS inlining; the admin Tailwind stylesheet is not sent to recipients. Plain text must be usable. Preview all locales and attachments safely, including sandboxed HTML previews.

## 9. SMTP, domains, and outgoing webhooks

SMTP transports:

- Support authenticated TLS SMTP providers and a privately owned SMTP server through administrator-configured transports. TLS certificate validation is enabled by default; insecure local capture is explicit development configuration.
- Start with one complete production provider integration, preferably SES SMTP plus authenticated feedback integration, and generic SMTP. Verify current provider details. Private direct-to-MX infrastructure is outside mrkt: mrkt submits to an MTA and documents DNS/reputation limitations.
- Per-project/stream transport selection, concurrency, rate limits, transient/permanent error classification, backoff, and circuit-breaking. Avoid automatic provider switching after ambiguous acceptance; never rotate transports to circumvent recipient suppression or policy rejection.
- Build correct MIME multipart messages, attachments, header-injection prevention, unsubscribe headers, and stable message identifiers. Provide transport diagnostics and safe capture/test mode.
- Normalize provider delivery/bounce/complaint callbacks with signature verification, replay/deduplication, event ordering handling, and explicit limitations for generic SMTP. An SMTP 250 response means accepted by the next server, not inbox delivery.
- A generic transport without complaint feedback must advertise that limitation; do not fabricate delivery metrics. Implement a documented authenticated bounce ingestion path or adapter for a privately managed MTA.

Domains:

- Project ownership verification, sender identities, actionable SPF/DKIM/DMARC checks, return-path/bounce configuration, and hosted subscription/asset domain configuration.
- Distinguish provider-managed DKIM from signing by a private MTA or mrkt; specify ownership of signing and DNS records. Do not generate records that pretend a provider identity exists before provisioning it.
- Use an existing TLS reverse proxy or an optional documented Compose proxy. Provide exact DNS/TLS setup and readiness status. Do not purchase/register domains or mutate DNS without credentials/authorization.

Outgoing webhooks:

- Versioned envelope with stable event ID, project ID, occurrence timestamp, event type, and correlation/causation identifiers.
- Events include subscription confirmation/unsubscribe, sequence completion, delivery status, bounce, complaint, and operational pauses where useful.
- Transactional outbox, HMAC signatures over exact payload bytes plus timestamp, secret rotation, retry/backoff, delivery history, dead-letter handling, and replay.
- At-least-once delivery with no global ordering guarantee. Provide consumer deduplication examples and loop-avoidance guidance. Preserve event identity during replay and distinguish attempt identity.

## 10. REST, CLI, MCP, skill, and presets

- Versioned REST API with OpenAPI, pagination, typed filters, stable errors, auth scopes, idempotency, and optimistic concurrency where needed. All remote CLI/MCP capabilities must be accessible through REST. Local scaffolding is exempt.
- Cover project/token administration, domains/transports, contacts/lists/consent, event ingestion, artifact uploads, validation/plans/releases, sequences/enrollments, broadcasts, simulation, delivery inspection, webhook endpoints/deliveries, recovery status, and operational controls.
- CLI `mrkt`: useful help, completion, JSON/table output, stable exit codes, stdin support, secure credentials, noninteractive operation, explicit project/environment, and deployment diff. Secrets must not be encouraged in command-line arguments/history.
- Include commands equivalent to `init`, `sequence add`, `preview`, `validate`, `plan`, `deploy`, `releases`, `rollback`, `events emit`, `contacts`, `sequences`, `broadcasts`, `explain`, `domains`, `transports`, `webhooks`, and `doctor`; choose consistent final syntax and document it.
- MCP initially through `mrkt mcp serve` over stdio, using the REST API. Use a supported official SDK/spec version, structured schemas and tool results, pagination, appropriate read/destructive annotations, and clear errors. Stdout is reserved for protocol traffic. No unrestricted shell/SQL tool and no hidden admin credentials.
- Ship an official installable mrkt agent skill, versioned with releases. Use the available skill-creator workflow if present, keep its files in git, and test its documented commands. Teach setup, presets, i18n, preview/simulation, plan/deploy, consent, troubleshooting, recovery limitations, and safe use of real sends. Reference actual docs instead of duplicating a huge command catalog. Include CLI and MCP configuration examples.
- Presets: coming-soon/waitlist, welcome/onboarding, newsletter broadcast, and launch announcement. Generate complete editable project files with localized samples, schemas, sample events, and simulated output. Upgrading mrkt never silently rewrites generated project content.
- Include working synthetic integration examples for Flowrent and Cheerful Cards: authenticated event producer, webhook consumer with verification/deduplication, and localized marketing directory. Do not connect to their real production systems.

## 11. UI and branding

Build a polished responsive operational UI: project switcher, overview, contacts/lists, consent, releases and diffs, sequences/enrollments, broadcasts, message previews, execution timeline/explain, delivery failures, domains, transports, webhooks, abuse pauses, and backup/recovery status. Include loading/empty/error states, keyboard navigation, focus handling, and readable contrast.

Configuration remains repository-owned. The UI may provide previews, inspection, and explicit audited runtime operations; it must not silently edit deployed sequence/content definitions and create drift.

Generate a coherent original **mrkt** identity as part of the build, not placeholder graphics:

- Lowercase wordmark and distinctive simple icon, light/dark and monochrome variants, favicons/app icons.
- GitHub README cover and social preview image (verify current platform dimensions), social card, and real application screenshots after the UI works.
- Editable vector sources for code-native logo artwork, raster exports as needed, design tokens, and a short brand guide under `assets/brand/`.
- Use the image-generation tool/skill when creating raster marketing illustrations. Preserve approved outputs in the repository. Do not substitute fabricated screenshots, copy another product's identity, or claim an unavailable generator ran. If unavailable, produce original vector assets and explicitly record the raster-generation blocker.
- Direction: concise, developer-oriented, calm and professional, clear at small sizes; connect subtly to messages, sequences, or deployment. Avoid generic AI sparkle imagery and illegible generated lettering. Choose a direction autonomously and inspect final exports visually.
- Record font/asset provenance and redistribution rights. All shipped material must be suitable for the intended MIT release, with third-party notices where required.

## 12. Delivery phases and gates

### Phase A — establish contracts

Verify tools/models/access; create or safely inspect the private repository; establish module boundaries, schema ownership, versioned manifest/OpenAPI/event contracts, and ADRs for SQLite/S3/Litestream, release pinning, SMTP ambiguity, and recovery. Set up CI, build scripts, linting, and meaningful test infrastructure. Then dispatch workers with bounded ownership.

### Phase B — prove the vertical slice

From a clean checkout: start dev Compose, provision a project, scaffold localized waitlist content, upload to S3, deploy, submit subscription, capture confirmation, confirm, execute the welcome sequence, inspect via REST/CLI/MCP/UI, and unsubscribe before the next step. The next email must not be sent. Integrate this slice before broadening features.

### Phase C — complete feature scope

Add broadcasts, conditions/exit rules, version changes and rollback, all presets, provider feedback, webhooks, domains, abuse controls, full i18n, agent skill, operational UI, retention, backup monitoring, and recovery. Complete branding and documentation. No unimplemented success-returning endpoints or hidden mock production paths.

### Phase D — independent review and acceptance

Run risk-focused tests and fix failures, including:

1. Cross-project access denial for API, objects, tokens, domains, and public assets.
2. Duplicate event and deploy submissions; stale plans; failed partial uploads.
3. Version-pinned enrollment during redeployment and rollback; no implicit re-enrollment.
4. Concurrent unsubscribe/complaint versus queued sends, including worker lease expiry.
5. SMTP transient/permanent errors and ambiguous acceptance without blind duplicate sends.
6. Duplicate/out-of-order provider callbacks and signed webhook retries/replays/SSRF rejection.
7. Confirmation bombing limits and server-side challenge verification, including restart behavior.
8. Locale fallback, missing variables, Unicode, timezone/DST, HTML escaping, and attachment limits.
9. Process termination/restart with pending jobs and frozen message content.
10. S3 failures and reference-safe garbage collection, including artifacts required by retained backups.
11. Litestream restore into an isolated empty volume, integrity verification, sending paused, explicit reconciliation/resume; existing database never overwritten and failed restore never initializes empty state.
12. API/CLI/MCP operation parity, executable skill examples, and a real MCP protocol smoke test.
13. Real browser checks of main UI paths and visual inspection of branding/exports.
14. Clean-clone Docker Compose build/start and documented upgrade/backup/restore commands.

Use local SMTP capture and S3-compatible test storage plus a fake clock; do not rely only on mocks. Run Go tests/race checks where applicable, static analysis, dependency/security checks, build checks, and meaningful browser tests. Report environment-blocked checks honestly. Benchmark a documented synthetic workload and report hardware/configuration/results rather than inventing capacity guarantees.

### Phase E — finish and hand off

- Integrate completed work and push to the private repository. Verify repository visibility. Run/inspect CI where permissions allow. Configure protection if supported without blocking the owner's access; document unavailable settings.
- Deliver MIT LICENSE, README with generated cover/screenshots, quickstart, architecture/ADRs, API reference, manifests and presets, CLI/MCP/skill documentation, integration examples, SECURITY.md, CONTRIBUTING.md, environment reference, operations/restore runbook, and release notes.
- Supply a reproducible multi-stage Dockerfile, production Compose using external S3, dev Compose profile with S3 test service and SMTP capture, `.env.example` without secrets, and pinned Litestream configuration.
- Build release-ready binaries/images with version metadata and provide private/local artifacts as permitted. Do not publish public GitHub releases or public container packages. No paid infrastructure provisioning is necessary to verify the development stack.
- Report what works, exact tests run, remaining external setup (S3/provider/DNS), and any genuine blockers. Do not call the product production-ready if core correctness/recovery gates remain unverified.

## 13. Completion contract

This task is done when a new user can follow the README from a clean clone, run mrkt, deploy repository-owned localized marketing assets into S3, operate sequences and broadcasts through REST/CLI/MCP, receive verified webhooks, manage consent safely, and perform a tested Litestream recovery. The private GitHub repository contains the working integrated implementation, official agent skill, generated branding, tests, and truthful documentation. Continue until that outcome is achieved or a concrete external blocker prevents further progress.

## Primary documentation starting points

Verify current versions and details at implementation time; these links are starting points, not permission to copy outdated configuration:

- https://four.htmx.org/
- https://tailwindcss.com/docs/installation/tailwind-cli
- https://pkg.go.dev/html/template
- https://sqlite.org/wal.html
- https://litestream.io/guides/docker/
- https://litestream.io/guides/s3/
- https://litestream.io/alternatives/
- https://developers.cloudflare.com/turnstile/get-started/server-side-validation/
- https://support.google.com/mail/answer/81126
- https://docs.aws.amazon.com/ses/latest/dg/monitor-sending-activity-using-notifications.html
- https://modelcontextprotocol.io/docs/

Begin now: verify the environment and repository access, establish the shared contracts, assign the first independent worker tasks, and implement the vertical slice.
