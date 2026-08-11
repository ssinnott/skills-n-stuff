<!-- Example output (Claude Sonnet) for the input diff at evals/fixtures/complex-call-flow.diff
     plus the commit list at evals/fixtures/complex-call-flow.commits.txt -->
# Make order submission idempotent

Duplicate order submissions — from client retries or queue redeliveries — could double-charge and double-ship; this requires an Idempotency-Key header and a redis-backed store that replays the original receipt instead of reprocessing.

- A submission without an Idempotency-Key header now is rejected.
- A repeated key skips re-charging and re-inserting the order; the response carries an Idempotent-Replayed header.
- The key now flows through the fulfillment queue job, so a redelivered job ships only once.
- The store keeps receipts and delivery claims for 24 hours, written once, so concurrent duplicates can't overwrite a stored result.

## Testing

Covers replay returning the original receipt with a single charge, rejection of a missing key, and an order shipping only once across redelivered fulfillment jobs.

## Commits

- [b3a91f2](https://github.com/acme/orderflow/commit/b3a91f2) - Add redis-backed idempotency store
- [7c40de8](https://github.com/acme/orderflow/commit/7c40de8) - Require idempotency key on order submission and replay stored receipts
- [e19f4a6](https://github.com/acme/orderflow/commit/e19f4a6) - Thread key through fulfillment queue and dedupe redeliveries
- [f52c7b9](https://github.com/acme/orderflow/commit/f52c7b9) - Cover replay, missing-key, and redelivery cases in tests
