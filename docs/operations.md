# Operations and recovery

## Deployment model

Run exactly one `app` and one `litestream` service for a database volume. SQLite is local state; S3 is not a shared SQLite filesystem, and Litestream is asynchronous disaster recovery rather than synchronous replication or host fencing. Fence the old host before recovery.

Production uses external S3-compatible storage. Use separate credentials: the application identity is scoped to the artifact prefix and must not access backups; the Litestream identity is scoped to `LITESTREAM_PREFIX/<installation-id>/sqlite`. Backup deletion is disabled in Litestream so the replication identity need not have `DeleteObject`; apply a reviewed bucket lifecycle policy separately.

Start from [the artifact IAM example](../deploy/iam-artifacts-policy.json) and [the Litestream IAM example](../deploy/iam-litestream-policy.json), replacing every `REPLACE_*` token. They intentionally grant neither identity `DeleteObject`; the artifact identity cannot reach backups and the backup identity cannot reach artifacts. Validate the final policy with the provider's policy simulator and test it against a non-production bucket.

Create `.env` from `.env.example`, store it outside source control, then explicitly initialize once with `docker compose run --rm app server init`. The one-shot `data-init` service grants the image's fixed non-root UID/GID `100:101` access to the named database volume. A normal `docker compose up -d` never initializes or restores a missing database. Development adds `-f compose.dev.yaml` for MinIO and Mailpit; its checked-in credentials and encryption key are intentionally local-only.

Monitor `/metrics` on the Litestream service at loopback port 9090, container restarts, and the sizes of `/data/mrkt.db` and its WAL. These commands expose current TXID, sync count/errors, and replication logs:

```sh
curl -fsS http://127.0.0.1:9090/metrics | grep -E 'litestream_(txid|sync_count|sync_error_count)'
docker compose logs --since 24h litestream | grep -E 'replica sync|ltx file uploaded|snapshot complete|ERROR'
docker compose exec litestream litestream status -config /etc/litestream.yml /data/mrkt.db
```

Alert on increasing time since the last `replica sync`/upload and any growing sync error count rather than promising a fixed RPO. In the 2026-09-10 enhanced local drill, a forced final sync plus full isolated restore and safety checks of the actual engine fixture completed in 14.90 seconds; its observed transaction RPO was zero because shutdown sync completed. This is a drill result, not a guarantee: RPO expands throughout network/provider outages, and RTO includes fencing, credential recovery, download, integrity/reconciliation checks, and operator review.

## Disaster recovery

1. Fence the previous application and replication owner at the infrastructure/network layer. A lease inside a restored database cannot fence an older copy.
2. Recover independent configuration, encryption key, and backup credentials. Loss of the encryption key makes stored provider/webhook secrets unrecoverable. Keep an offline recovery copy and consider an off-account bucket replication target.
3. Select a brand-new named volume; never reuse or attach the damaged/current volume as the target. Keep the old volume for investigation:

   ```sh
   export MRKT_DB_VOLUME="mrkt-recovery-$(date -u +%Y%m%dT%H%M%SZ)"
   docker compose --profile recovery run --rm recovery
   ```

   The recovery service runs the pinned `deploy/restore.sh`. It refuses any database/WAL/SHM target, restores through a partial file, runs Litestream's full SQLite integrity check, atomically renames, and creates `mrkt.db.recovery-required`. Any bucket/access/backup error fails without initializing an empty database.
4. Start only the application in recovery mode. The marker forces recovery even if the environment flag is accidentally false:

   ```sh
   MRKT_RECOVERY=true docker compose up -d app
   mrkt api installation
   ```

   The response must show `"recovery": true` and `"outbound_paused": true`. SMTP and business webhook dispatch remain paused; signed feedback callbacks remain ingestible.
5. Inspect every project using supported read operations (repeat with each project ID):

   ```sh
   mrkt releases list --project PROJECT --output json
   mrkt contacts list --project PROJECT --output json
   mrkt api consent list --project PROJECT
   mrkt api deliveries list --project PROJECT
   mrkt api webhook-deliveries list --project PROJECT
   mrkt api operations usage --project PROJECT
   ```

   Compare active release inventories to S3 objects, investigate queued/dispatching/uncertain records, and verify restored confirmations, unsubscribes, complaints and suppressions against provider records. `mrkt api installation` is the application recovery status check; there is no separate success-returning “verify” command.
7. Reconcile changes that may have occurred after the last replicated transaction: unsubscribes, complaints, accepted SMTP sends, and webhook acknowledgements cannot be reconstructed from SQLite backup alone. Do not blindly replay overdue or ambiguous sends.
7. Record the incident, last trustworthy provider timestamps and reviewed cutoff. Resume only with an explicit audit note:

   ```sh
   printf '%s\n' '{"reconciled":true,"note":"Compared SMTP accepts, complaints, unsubscribes and webhook acknowledgements through 2026-09-10T14:00:00Z"}' |
     mrkt api installation resume
   docker compose up -d litestream
   ```

   Successful resume clears the recovery marker transactionally with the audit record. Do not delete it manually.

Restore never falls through to initialization when the bucket is empty, unavailable, unauthorized, or corrupt. Never use Litestream's `-if-replica-exists` in production recovery because it can turn a missing backup into apparent success.

Run the real isolated drill with `go test -tags=integration ./deploy -run TestLitestreamRestoreFromRealObjectStore -v`. It uses Docker, a real MinIO bucket, distinct source/restore directories, Litestream full integrity checking, and safety-state assertions.

Artifact garbage collection must remain dry-run-only until it can prove references across active/pinned releases, queued messages, public asset retention, and every retained database restore point. Prefer retention to deleting an object needed by an older recoverable database.

## DNS, TLS and SES feedback

Terminate TLS in an existing reverse proxy and forward to `127.0.0.1:8080`; do not expose the application container directly. Create `A`/`AAAA` records for the chosen application hostname, issue a publicly trusted certificate, proxy with the original `Host`, and restrict direct port 8080 access. For example, a Caddy site block is `mrkt.example.com { reverse_proxy 127.0.0.1:8080 }`; set `MRKT_PUBLIC_URL=https://mrkt.example.com`. Confirm `/healthz`, HTTPS redirects, HSTS, and the hosted subscription/asset URLs before provisioning senders.

For SES, first verify the sending identity and DKIM in the SES region, configure SPF/DMARC appropriate to the real MAIL FROM/signing arrangement, and create SMTP credentials. Create an SNS topic in the same region, subscribe `https://mrkt.example.com/feedback/PROJECT/TRANSPORT_ID`, and configure SES bounce, complaint and delivery notifications to that topic. mrkt verifies the SNS topic ARN, timestamp, AWS certificate URL and RSA signature before normalizing feedback; it never follows SNS certificate redirects or automatic subscription-confirmation URLs. Complete SNS subscription confirmation deliberately in AWS and test with SES simulator addresses. AWS primary references: [SES identity authentication](https://docs.aws.amazon.com/ses/latest/dg/creating-identities.html), [SES SNS notifications](https://docs.aws.amazon.com/ses/latest/dg/monitor-sending-activity-using-notifications-sns.html), and [SNS signature verification](https://docs.aws.amazon.com/sns/latest/dg/sns-verify-signature-of-message.html).

## Encryption-key recovery and rotation

`MRKT_ENCRYPTION_KEY` protects stored transport and webhook configuration. Generate it with a CSPRNG (for example, `openssl rand -base64 32` directly into a mode-0600 secret file), never use a repeated/example/zero-filled value in production, and avoid printing it into shell history or logs. Back it up independently from the SQLite/S3 account, with access auditing and at least one offline recovery copy. Record which key version protects each database-backup generation. A database restore without its matching key cannot recover provider credentials.

Before rotation, stop the application and let Litestream complete shutdown sync, take and test an isolated database backup, and retain the old key under its version. Supply both keys through protected environment/secret files—never command-line arguments—then run the offline exclusive-lock command:

```sh
docker compose stop app
docker compose logs --tail 20 litestream
export MRKT_NEW_ENCRYPTION_KEY_FILE=/run/secrets/mrkt-new-encryption-key
docker compose run --rm -v /secure/host/keys:/run/secrets:ro app server rotate-key
```

`MRKT_ENCRYPTION_KEY` (or its `_FILE`) remains the old key for this command; `MRKT_NEW_ENCRYPTION_KEY` or `_FILE` is the new base64-encoded 32-byte key. The command takes the exclusive database lock and decrypts/re-encrypts registry records plus its audit entry in one SQLite transaction without starting HTTP or workers. On success, update the configured primary key to the new key, unset the new-key variable, and restart. Do not replace the environment key before the command succeeds: existing ciphertext would become unreadable. Keep the old key until every retained database snapshot encrypted under it expires and an isolated restore with that snapshot/key combination has passed.
