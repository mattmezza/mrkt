# REST conventions and operation catalog

The authoritative transport contract is [OpenAPI 3.1](openapi.yaml). REST, CLI, MCP, and UI invoke the same engine. Configuration/content is immutable release data; runtime operations do not rewrite it.

All authenticated routes require `Authorization: Bearer ...`. Installation administration requires a global administrator. Project credentials are restricted to exactly one project with `read`, `config`, `operate`, and/or `send` scopes. Token secrets are revealed only at creation. Provider passwords and webhook secrets are encrypted and redacted from later reads.

Collections: `GET /api/v1/projects/{project}/{resource}?limit=50&cursor=...`; responses contain `items` and `next_cursor`. Limits are bounded to 200. IDs/cursors are opaque. Record reads append `/{id}`. Collection creation uses POST; deletion uses DELETE on a record. Other actions use `POST .../{id}/{action}`; `_` is a placeholder for actions without a record ID.

| Resource | Important operations |
| --- | --- |
| `projects`, `tokens` | Create/list/get; scoped tokens and revoke/delete; project pause/resume. |
| `contacts`, `lists`, `consent` | Upsert without consent, inspect, export/import-preview/import, delete; request DOI, confirm, unsubscribe, consent history. |
| `artifacts` | PUT a bounded object under SHA-256; GET requires read scope. No public private-artifact listing. |
| `releases` | Plan/deploy with `manifest`, `expected_release`, `allow_destructive`; list/get/rollback/preview/simulate. |
| `events` | Create authenticated contact or project event with stable producer key and typed payload. |
| `enrollments`, `sequences` | Inspect pinned state, pause/resume/cancel/explain, simulation, explicit migration plan/apply. |
| `broadcasts`, `deliveries` | Frozen audience inspection and runtime controls; inspect frozen delivered renders. |
| `domains`, `transports` | Provision, inspect, disable; DNS ownership/readiness check. SMTP provisioning is administrator-only. |
| `webhooks`, `webhook-deliveries` | Provision, rotate, inspect per-attempt history, replay with stable event ID. |
| `operations` | Usage, retention, conservative dry-run GC. |
| `installation` | Global recovery/health state and explicit audited resume. |

Not every verb applies to every resource; unsupported operations return errors, never mock success. Query filters are intentionally explicit per operation; there is no arbitrary SQL query interface. JSON bodies are bounded and unknown service-input fields fail validation.

Stable errors use `{"error":{"code":"...","message":"..."}}`. Typical statuses: 400 invalid input, 401 missing/invalid token, 403 insufficient authority, 404 missing resource, 409 precondition/idempotency conflict, 413 size limit, 429 abuse limit, 503 unavailable dependency. Internal errors do not expose database diagnostics to API clients.

Supply `Idempotency-Key` for a retryable mutation and repeat the exact logical request. Scope is project/resource/action; fingerprints reject different inputs with the same key. Event producer keys and release content digests provide domain-specific duplicate protection too. An uncertain SMTP attempt is not made safe by another HTTP idempotency key.

## Public subscription and provider callbacks

`POST /public/{project}/subscribe` accepts an address, list, locale/timezone, source and challenge token; it cannot assert confirmed consent or inject confirmation content. Responses are generic. `GET /public/{project}/confirm?token=...` only displays a form; POST consumes a single-use expiring token. Unsubscribe GET similarly does not mutate; POST supports RFC one-click. `project_wide:true` applies only within the token's project. Token-scoped preferences are localized in English, Italian, and Turkish.

`GET /assets/{project}/{sha256}/{name}` only serves approved public image inventory. It never exposes templates, attachments, manifests, or backups. `POST /feedback/{project}/{transport}` accepts exact signed SNS/MTA bytes and remains available during recovery; see [sending](sending.md).

## Preview and simulation

`POST .../releases/{release}/preview` accepts `message`, `locale`, and synthetic `variables`. It returns subject/HTML/text, attachment metadata, requested/resolved locale and explicit fallback, without queuing mail. `POST .../deliveries/{id}/preview` reads the frozen render. Treat returned HTML as untrusted; the built-in UI uses a sandboxed iframe with restrictive CSP.

Simulation accepts `sequence`, `variables`, optional `manifest`, and an RFC3339 `start`; absent start uses a fixed deterministic clock. Results include a bounded execution trace and `external_sends:false`.
