<!-- Example output (Claude Haiku 4.5) for the input diff at evals/fixtures/complex-call-flow.diff
     plus the commit list at evals/fixtures/complex-call-flow.commits.txt -->
# Make order submission idempotent

Clients can safely retry order requests by providing an Idempotency-Key header; the system stores and replays responses to prevent duplicate charges and shipments.

- Order submissions now require Idempotency-Key header; submissions without one are rejected
- Duplicate requests return the original receipt and Idempotent-Replayed header instead of reprocessing
- Fulfillment queue deduplicates redelivered jobs so each order ships exactly once despite job retries

## Testing

Tests cover replay detection, missing-key validation, and queue redelivery deduplication.

## Commits

- b3a91f2 - Add redis-backed idempotency store
- 7c40de8 - Require idempotency key on order submission and replay stored receipts
- e19f4a6 - Thread key through fulfillment queue and dedupe redeliveries
- f52c7b9 - Cover replay, missing-key, and redelivery cases in tests
