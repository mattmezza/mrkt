# Client/CLI/MCP handoff

Implemented:

- REST client with bearer authentication, idempotency keys, standard routes, bounded response reads, error envelopes, artifact PUT, and resource DELETE.
- `cli.Run(context.Context, []string, io.Reader, out, errOut)` supporting local init, sequence add, validate and preview plus REST plan/deploy, named project/token/release/event/contact/sequence/broadcast/domain/transport/webhook/explain/doctor commands, generic API operations, DELETE, JSON/table output, named credential environments, and MCP stdio serving.
- Deploy uploads the manifest inventory before activation. Plan/deploy pass expected release and explicit destructive permission.
- Official MCP Go SDK v1.7.0 server with inferred JSON schemas, structured output, annotations, and two focused query/mutation tools.
- Four distinct English/Italian/Turkish complete presets (coming-soon/waitlist, welcome/onboarding, newsletter broadcast, launch announcement), Flowrent/Cheerful synthetic producer and consumer examples, CLI/MCP/manifest docs, and an installable `mrkt` agent skill.

Actual checks:

```text
go test ./internal/client ./internal/cli ./internal/mcpserver ./presets
ok internal/client, internal/cli, internal/mcpserver; presets has no tests
python3 .../skill-creator/scripts/quick_validate.py skills/mrkt
Skill is valid!
git diff --check
passed (no whitespace errors)
```

MCP tests perform both an official SDK in-memory protocol handshake/call and a real stdio subprocess handshake/tool listing. CLI tests initialize all four distinct presets, validate, preview Italian fallback, add and revalidate a sequence, and validate both synthetic consumers.

Integration notes and limitations:

- Coordinator must route non-server root arguments to `cli.Run` and owns that file.
- `MRKT_URL` and `MRKT_TOKEN` configure remote access. An empty URL intentionally fails for remote commands and MCP startup.
- `sequence add` rewrites YAML canonically, so comments are not preserved.
- No live SMTP or external mrkt server was contacted.
- Repository-wide `go test ./...` was also attempted. Client/CLI/MCP and the other completed packages passed, but the run currently fails because the concurrently developed `internal/engine` references `doRegistry`, which is not yet present. This is outside this worker's ownership.

Read-only engine review findings were sent to the coordinator: transient delivery retries are defeated by the delivery uniqueness path; final eligibility is not list-specific; list-level unsubscribe cancels enrollments across all lists; and event contact ownership requires explicit project validation.
