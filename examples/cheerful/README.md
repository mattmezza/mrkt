# Cheerful producer

Synthetic producer example: a publishing system emits a deduplicated project event. It contains no subscriber data.

```sh
printf '%s' '{"name":"article.published","external_id":"article-demo-001","attributes":{"slug":"a-synthetic-card"}}' |
  mrkt api events --project cheerful --idempotency-key article-demo-001
```

The verified consumer is `consumer/mrkt.yaml`; run `mrkt validate --dir consumer` before plan or deploy.
