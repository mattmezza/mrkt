# Cheerful producer

Synthetic producer example: a publishing system emits a deduplicated project event. It contains no subscriber data.

```sh
MRKT_TOKEN=project-token node producer.mjs
```

The producer sends the strict event shape `{key,type,payload}` and uses the same
stable key as its `Idempotency-Key`. Set `EVENT_KEY` when retrying an event.

The verified consumer is `consumer/mrkt.yaml`; run `mrkt validate --dir consumer` before plan or deploy.

The shared `../webhook-consumer.mjs` verifies timestamped exact-body HMACs,
deduplicates the stable event ID, and persists accepted IDs to an append-only
mode-0600 journal. Try it with `MRKT_WEBHOOK_SECRET='32-or-more-secret-characters' node ../webhook-consumer.mjs --self-test`.
