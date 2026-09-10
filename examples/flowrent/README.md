# Flowrent producer

Synthetic producer example: after a booking completes, the trusted application publishes `booking.completed` with a stable idempotency key. The address is fictional.

```sh
MRKT_TOKEN=project-token MRKT_CONTACT_ID=CONTACT_ID node producer.mjs
```

The producer sends the strict contact-event shape `{key,type,contact_id,payload}`
and uses the same stable key as its `Idempotency-Key`. Obtain the contact ID from
mrkt after the booking address has been created; never substitute an email or a
contact belonging to another project. Set `EVENT_KEY` when retrying an event.

The consumer release is in `consumer/mrkt.yaml`. Validate it from this directory with `mrkt validate --dir consumer`.

The shared `../webhook-consumer.mjs` verifies timestamped exact-body HMACs,
deduplicates the stable event ID, and persists accepted IDs to an append-only
mode-0600 journal. Try it with `MRKT_WEBHOOK_SECRET='32-or-more-secret-characters' node ../webhook-consumer.mjs --self-test`.
