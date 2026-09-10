# Infrastructure versions

Checked against primary project documentation and registries on 2026-09-10.

| Component | Pin | Rationale |
|---|---:|---|
| Go toolchain image | `golang:1.27.1-alpine3.23` | Current patched Go toolchain, compiling the Go 1.26 module baseline. |
| Node asset builder | `node:24.21.0-alpine3.23` | Current verified LTS line; build stage only, absent from runtime. |
| Runtime Alpine | `alpine:3.23.3` | Exact patch tag; CA certificates and timezone data only. |
| AWS SDK for Go v2 config | `v1.33.4` | Current resolved module pin; standard AWS credential chain and S3-compatible endpoints. |
| AWS SDK for Go v2 S3 | `v1.113.0` | Current resolved module pin. |
| emersion/go-smtp | `v0.25.0` | Maintained SMTP protocol client exposing the DATA boundary required for ambiguity classification. |
| Litestream | `litestream/litestream:0.5.14` | Current documented v0.5 release; full restore integrity check and safe no-overwrite behavior. |
| MinIO (development/test only) | `minio/minio:RELEASE.2025-09-07T16-13-09Z` | Immutable dated release tag; never part of production Compose. |
| MinIO client | `minio/mc:RELEASE.2025-08-13T08-35-41Z` | Immutable dated release used only to create the dev bucket. |
| Mailpit (development/test only) | `axllent/mailpit:v1.27.8` | Versioned local SMTP capture service. |

Container tags are version-pinned but not registry-digest-pinned so the same Compose file remains multi-architecture. A production supply-chain process should resolve each approved architecture to its manifest digest, mirror it, and update these pins through a tested change.

Litestream 0.5 does not support its former Age client-side encryption. Use S3 server-side encryption and restricted credentials; keep backup credentials and application encryption keys in independent recovery storage.
