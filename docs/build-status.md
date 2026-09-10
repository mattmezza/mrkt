# Build status

The acceptance contract is the repository-root `prompt.md`. This file records the verified release state.

## Current integrated state — 2026-09-10

- The original prompt commit was preserved. Accepted implementation checkpoint `fb5d1f461b7f566c1596e1fe3bd7834d84f9178b` is pushed to the private `mattmezza/mrkt` repository.
- GitHub reports private visibility and owner administration access. Private-repository branch protection is unavailable on the current plan (API HTTP 403); visibility was not changed and no purchase was attempted.
- The requested coordinator/worker split used three non-recursive workers with explicit ownership. The runtime exposed the requested worker model selection; the coordinator cannot independently attest or change its host-selected model.
- Repository releases, project isolation, consent/suppression, sequence and broadcast execution, final send eligibility, feedback/webhooks, encrypted registries, REST/CLI/MCP, four localized presets, official skill, operational UI, Docker/Litestream recovery, branding and documentation are implemented.
- Contact and project events now have distinct audience semantics. Broadcast and enrollment pause/cancel state is checked at final intent. Completion and provider-feedback ordering have focused regressions. Recovery resume fails closed if its durable marker cannot be removed.
- The current tree passes the Go unit suite, vet, full race suite, npm audit, `govulncheck`, asset/schema generation, skill validation, example syntax/signature checks, and the real synthetic S3/SMTP/browser lifecycle. Five widths pass overflow and light/dark contrast checks.
- A clean clone of pushed checkpoint `87f2691` passed Compose configuration, multi-stage build, explicit initialization, healthy startup and Litestream replication (database/replica TXID 1, ten successful syncs, zero sync errors).
- The enhanced isolated Litestream restore drill passed again on the final working tree in 14.26 seconds, including engine-created state, recovery pause, callback ingestion, missing-backup refusal and no overwrite.

The first two CI runs exposed and drove fixes for a missing CLI-test import and a probabilistic base64 tamper assertion. The accepted implementation rejects non-canonical encrypted-secret encodings and flips authenticated bytes in its tamper test; the focused race test passed 100 consecutive local runs. GitHub Actions run `34498089383` then passed both jobs: frontend compilation, vet, full race suite, vulnerability scan, binary and release-image builds, plus real S3/SMTP/browser lifecycle and isolated Litestream recovery.

External production S3/IAM, SMTP or SES/SNS, DNS, TLS and challenge configuration remain operator work. Local SMTP acceptance does not prove internet inbox placement.
