<!-- Example output (Claude Opus) for the input diff at evals/fixtures/complex-call-flow.diff
     plus the commit list at evals/fixtures/complex-call-flow.commits.txt -->
# Make order submission idempotent end to end

Retried order submissions could charge and ship the same order twice, so submission now requires an idempotency key and replays the original receipt.

- Submissions without the header are rejected, so existing clients break until they send one.
- The key travels route → service → queue → worker: the service returns the stored receipt on a repeat, and the worker ignores redelivered jobs.
- Two simultaneous submissions with one key can both miss the stored receipt and charge twice — only the worker takes a claim.
- Stored receipts expire after a day; a later retry is treated as a new order.
- The real change is the new store and the service's submit path; the producer and worker only pass the key along.

## Testing

A new test file covers a repeated key replaying the first receipt with only one charge, a missing key being rejected, and an order shipping once across queue redeliveries. Nothing covers two submissions arriving at the same time.

## Commits

- b3a91f2 - Add redis-backed idempotency store
- 7c40de8 - Require idempotency key on order submission and replay stored receipts
- e19f4a6 - Thread key through fulfillment queue and dedupe redeliveries
- f52c7b9 - Cover replay, missing-key, and redelivery cases in tests
