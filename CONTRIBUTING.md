# Contributing

Use Go 1.26 or newer and the pinned npm lockfile for build-time assets. Run `npm ci && npm run build`, `go test ./...`, `go vet ./...`, and `go test -race ./...`. Use synthetic recipients and local capture SMTP. The production container has no Node runtime.

Preserve the separation between Git-owned immutable releases and live consent/execution state. Shared business logic belongs in internal/engine; REST and HTML call that service, CLI and MCP call REST. New database changes require an appended numbered migration and actual SQLite tests. No network I/O inside database transactions.

For changes to dispatch, test retries and ambiguous acceptance. For consent, test cross-project access and final-boundary unsubscribe. For artifacts, test partial upload and retained-backup safety. Record actual tests in pull requests; do not infer recovery or deliverability guarantees from unit tests.

The repository remains private until the owner separately authorizes public release. Contributions are distributed under MIT, subject to notices for third-party dependencies.
