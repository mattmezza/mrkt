# CLI

Set `MRKT_URL` and `MRKT_TOKEN` for remote commands.

```sh
mrkt init --preset welcome --dir campaign
mrkt sequence add onboarding --dir campaign
mrkt validate --dir campaign
mrkt preview --dir campaign --locale it-CH
mrkt plan --dir campaign --expected-release RELEASE_ID
mrkt deploy --dir campaign --expected-release RELEASE_ID
```

Validation and preview are local and send no mail. Plan is remote without activation. Deploy uploads content-addressed files before activation. Expected release prevents overwriting an unplanned concurrent change; destructive changes also require `--allow-destructive`.

`mrkt api RESOURCE [ACTION]` covers other operations. Use `--project`, `--id`, and JSON on stdin. Reuse an idempotency key only for an identical retry.

`mrkt mcp serve` starts stdio MCP. Stdout is reserved for the protocol.

Localized English/Italian/Turkish starters are available for `coming-soon`, `welcome`, `newsletter`, and `launch`. They contain different waitlist, onboarding, recurring broadcast, and launch-event workflows.

Named remote commands include `projects`, `tokens`, `releases`, `events emit`, `contacts`, `sequences`, `broadcasts`, `explain`, `domains`, `transports`, `webhooks`, and `doctor`. Each resource supports consistent `list`, `get`, `create`, `delete`, and explicit action verbs where valid. Use `--output json` or `--output table`.

Pass `--env NAME` to select credentials from `$MRKT_CONFIG` or the platform config directory at `mrkt/config.json`. The JSON shape is `{"environments":{"NAME":{"url":"...","token":"..."}}}` and the file must have mode `0600`.
