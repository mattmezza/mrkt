---
name: mrkt
description: Manage a repository-owned mrkt email marketing project by initializing, validating, previewing, planning, deploying, and operating it. Use when a repository contains mrkt.yaml or the user explicitly asks about mrkt.
---

# mrkt

Treat `mrkt.yaml` and its templates as repository-owned configuration. Contacts, consent, suppression, enrollments, deliveries, and execution state are live server data; never restore them by editing or rolling back a manifest.

Before deployment, read the manifest and relevant sources, run `mrkt validate --dir <project>`, inspect all locales with `mrkt preview --dir <project>`, and run `mrkt plan --dir <project>`. Explain destructive changes and require authorization before using `--allow-destructive`. Deploy with `--expected-release` so a stale plan cannot silently win.

Use `MRKT_URL` and `MRKT_TOKEN` for remote access. Never commit tokens or transport credentials. Prefer narrow project credentials. Query live state before mutation, identify exact targets for operational actions, never infer marketing consent from contact data, and never bypass suppression. Use a fresh idempotency key per business operation; reuse one only for an identical retry.
