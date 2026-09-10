# Flowrent producer

Synthetic producer example: after a booking completes, the trusted application publishes `booking.completed` with a stable idempotency key. The address is fictional.

```sh
printf '%s' '{"external_id":"booking-demo-001","email":"guest@example.test","event":"booking.completed","attributes":{"property":"alpine-demo"}}' |
  mrkt api events --project flowrent --idempotency-key booking-demo-001
```

The consumer release is in `consumer/mrkt.yaml`. Validate it from this directory with `mrkt validate --dir consumer`.
