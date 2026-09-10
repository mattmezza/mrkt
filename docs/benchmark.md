# Synthetic workload observation

Measured 2026-09-10 with `go test -tags=integration ./deploy -run TestSyntheticWorkload -v` on an Intel Core Ultra 5 235U host with 14 logical CPUs and 30 GiB RAM. The test used Go 1.27.0, a temporary local SQLite database, Compose MinIO on loopback with path-style S3, and Compose Mailpit SMTP capture. Calls were serial and the test ran once after services were warm.

| Fixed workload | Observed elapsed time |
|---|---:|
| 1,000 engine contact upserts into SQLite | 492.2 ms |
| 100 immutable 4 KiB artifact PUTs followed by HEAD verification | 1.299 s |
| 100 individual multipart SMTP messages accepted by Mailpit | 2.285 s |

The complete test took 4.13 seconds. These are local development observations, not production capacity, latency, deliverability, or service-level guarantees. Real results vary with storage RTT, SMTP provider throttles, message size, SQLite filesystem durability, contention, and hardware. Repeat the exact bounded test on intended deployment hardware and keep provider rate limits below the measured application ceiling.
