---
name: mrkt
description: Manage a repository-owned mrkt email marketing project by initializing, validating, previewing, planning, deploying, and operating it. Use when a repository contains mrkt.yaml or the user explicitly asks about mrkt.
---

# mrkt

Treat `mrkt.yaml` and its templates as repository-owned configuration. Contacts, consent, suppression, enrollments, deliveries, and execution state are live server data; never restore them by editing or rolling back a manifest.

## Set up a project

Confirm that the `mrkt` binary is installed, then scaffold into a new directory:

```sh
mrkt init --preset coming-soon --dir marketing
```

Choose one of `coming-soon`, `welcome`, `newsletter`, or `launch`. Generated files are editable project source; upgrades never rewrite them. Read `mrkt.yaml`, templates, styles, and sample events before changing them. Use `mrkt sequence add ID --dir marketing` only when a new journey is requested.

Keep credentials outside the repository:

```sh
export MRKT_URL=https://mrkt.example.com
read -rs MRKT_TOKEN && export MRKT_TOKEN
```

Prefer a narrow project token. A profile can instead be selected with `--env NAME`; the mode-0600 config file format is documented in [the CLI guide](https://github.com/mattmezza/mrkt/blob/main/docs/cli.md). Never print or commit tokens, SMTP passwords, webhook secrets, or transport configuration.

## Edit and verify

Preserve stable IDs for lists, messages, sequence steps, and broadcasts. Add translations for every project locale. Locale fallback is useful for a missing regional variant, but do not use it to hide incomplete required translations.

Before every deployment, run the local, no-send checks:

```sh
mrkt validate --dir marketing
mrkt preview --dir marketing --locale en
mrkt preview --dir marketing --locale it
mrkt preview --dir marketing --locale tr
```

Inspect rendered subjects, text, HTML, links, unsubscribe behavior, and supplied sample data. Use release simulation when runtime conditions or event data matter:

```sh
printf '%s\n' '{"sequence":"SEQUENCE","variables":{"Email":"preview@example.test","Locale":"en","Attributes":{},"Event":{}}}' |
  mrkt api releases --project PROJECT simulate
```

Simulation and preview do not authorize a live send.

## Plan and deploy

Query the current release, then use its ID as the optimistic precondition:

```sh
mrkt releases list --project PROJECT --output json
mrkt plan --dir marketing --expected-release RELEASE_ID
mrkt deploy --dir marketing --expected-release RELEASE_ID
```

Read the complete plan. Call out list-policy changes, removed messages or workflows, audience changes, and other destructive effects. Obtain explicit authorization before adding `--allow-destructive`. If the current release changes after planning, stop and re-plan; do not bypass the stale-release conflict.

Existing enrollments stay pinned to their release unless an explicit migration is planned and confirmed. Rollback moves the active pointer; it does not roll back contacts, consent, suppressions, deliveries, or other live state.

## Operate safely

Use `MRKT_URL` and `MRKT_TOKEN` for remote access. Never commit tokens or transport credentials. Prefer narrow project credentials. Query live state before mutation, identify exact targets for operational actions, never infer marketing consent from contact data, and never bypass suppression. Use a fresh idempotency key per business operation; reuse one only for an identical retry.

Treat these as hard rules:

- Importing or creating a contact does not grant consent. Use the documented double-opt-in flow and retain its evidence.
- Project events and contact events have different audience semantics. Supply `contact_id` only for a contact event; never turn one contact's event into a broadcast.
- Preview with synthetic recipients. A real send requires explicit user authorization, a verified domain/transport, and a reviewed audience.
- Before pause, resume, cancel, replay, rollback, retention, migration, or deletion, read the target and explain the expected effect. Re-check it after mutation.
- Never work around a suppression, recovery pause, abuse pause, final eligibility check, idempotency conflict, or optimistic-concurrency failure.
- An SMTP-accepted message cannot be recalled. Treat uncertain delivery as uncertain rather than retrying blindly.

Use `mrkt explain --project PROJECT --id ENROLLMENT` for a journey timeline and reason. Use `mrkt doctor` and scoped read operations for troubleshooting. Consult the [CLI guide](https://github.com/mattmezza/mrkt/blob/main/docs/cli.md), [API conventions](https://github.com/mattmezza/mrkt/blob/main/docs/api.md), and [OpenAPI contract](https://github.com/mattmezza/mrkt/blob/main/docs/openapi.yaml) instead of guessing resource actions or JSON shapes.

## Recovery

A restored instance deliberately starts with SMTP and business webhooks paused. Do not resume merely because SQLite passes integrity checks. Fence the old host, reconcile provider accepts, unsubscribes, complaints, and webhook acknowledgements through a recorded cutoff, inspect ambiguous queued or dispatching work, and obtain operator approval. Resume only through the audited API with a concrete note. Follow the full [operations and restore runbook](https://github.com/mattmezza/mrkt/blob/main/docs/operations.md).

## MCP

Configure an MCP client to run the same binary over stdio and pass only a scoped credential:

```json
{
  "mcpServers": {
    "mrkt": {
      "command": "mrkt",
      "args": ["mcp", "serve"],
      "env": {"MRKT_URL": "https://mrkt.example.com", "MRKT_TOKEN": "SCOPED_TOKEN"}
    }
  }
}
```

Keep stdout reserved for MCP protocol traffic. Start with `mrkt_metadata`, use `mrkt_query` for reads, and use `mrkt_operate` only for an explicit requested mutation. REST authorization and safety checks remain authoritative. See [the MCP guide](https://github.com/mattmezza/mrkt/blob/main/docs/mcp.md).
