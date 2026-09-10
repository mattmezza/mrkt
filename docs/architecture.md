# Architecture decisions

## ADR 001 — one SQLite owner and private S3 artifacts

One active application process uses SQLite WAL on a local named volume, short serialized writes, foreign keys, busy timeout and versioned migrations. S3 stores immutable project-scoped content hashes. Deployment verifies every object before a release-pointer transaction with an expected-release precondition. Rollback moves the pointer only; live consent, suppressions and existing enrollments stay intact.

## ADR 002 — pinned execution and SMTP uncertainty

Enrollments pin release IDs. Stable step IDs, durable deadlines and event fingerprints prevent redeploy-driven reentry. A dispatch intent is persisted before network I/O; consent is rechecked at that boundary. A later unsubscribe cannot recall an SMTP handoff. Acceptance means next-server acceptance, never inbox delivery. Ambiguous SMTP attempts and interrupted dispatches require operator reconciliation, never blind retry.

## ADR 003 — asynchronous recovery

Litestream is disaster recovery, not synchronous replication or fencing. Fence the old host before restoring into an empty volume. Restore cannot overwrite an existing DB or fall through to initialization on object-storage failure. Restored instances pause outbound SMTP/business webhooks; callbacks remain ingestible. Explicit audited resume requires reconciliation of potentially lost unsubscribes and uncertain sends. Conservative artifact retention protects all supported backup states; no artifact deletion until reference safety across backup retention is demonstrable.

## ADR 004 — one application operation surface

REST and server-rendered UI call shared application services. CLI/MCP call REST. Strict manifest schema, hash-addressed source files, and operation errors are shared contracts. No LLM required at runtime. Go templates expose no filesystem/network/shell capability.
