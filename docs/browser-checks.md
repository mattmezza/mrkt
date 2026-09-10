# Browser and end-to-end checks

Run `node scripts/acceptance.mjs` while the development MinIO and Mailpit services are available on localhost ports 9000 and 8025/1025. The script builds the real binary, initializes a temporary database explicitly, starts a server on a free port, deploys the localized welcome preset, completes double opt-in from a captured Mailpit message, verifies welcome delivery, unsubscribes, and checks that duplicate event replay does not send afterward. All database, project, and binary fixtures are temporary; Mailpit messages are synthetic.

For browser coverage, start an initialized server containing at least one project and run:

```sh
MRKT_BROWSER_URL=http://127.0.0.1:8080 MRKT_BROWSER_TOKEN='...' npm run test:browser
```

The Playwright configuration uses installed `/usr/bin/chromium`. Tests cover login, overview, contacts, releases, recovery, and an error route at widths 320, 375, 414, 768, and 1280. Screenshots are intentionally not written until the UI owner declares the design ready; final approved captures belong in `assets/brand/screenshots`.

The acceptance run also generates seeded overview, contacts, and releases screenshots at all five widths after checking for horizontal overflow. It exercises the keyboard command palette, invalid pause input, and a successful CSRF-protected pause action.

## Verified run

On 2026-09-10, `node scripts/acceptance.mjs` passed against the Compose MinIO and Mailpit services. It verified explicit initialization, S3-backed deploy, CLI simulation, double opt-in from the captured confirmation URL, welcome delivery, RFC one-click unsubscribe before a synthetic two-second follow-up, no post-unsubscribe delivery, duplicate event replay, and all five Chromium browser cases. Fifteen screenshots were written to `assets/brand/screenshots`.
