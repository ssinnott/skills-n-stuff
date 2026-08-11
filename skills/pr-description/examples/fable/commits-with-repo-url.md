<!-- Example output for the input diff at evals/fixtures/complex-call-flow.diff
     plus the linked commit list at evals/fixtures/complex-call-flow.linked-commits.txt -->
# Make order submission and fulfillment safe to retry

A retried order submission could charge and ship twice; requiring an Idempotency-Key on every submission now makes the whole flow safe to retry.

- Requests without the key are rejected; a repeat with the same key returns the original receipt, flagged by a response header, without charging again.
- The key travels with the fulfillment job, so queue redeliveries ship an order at most once.
- Keys expire after a day — a retry outside that window would re-process.
- The real logic sits in the order service and fulfillment worker; the route and queue changes just carry the key through.

## Testing

New tests cover replaying the original receipt (and charging only once) for a repeated key, rejecting submissions without a key, and shipping only once across queue redeliveries.

## Commits

- [b3a91f2](https://github.com/acme/orderflow/commit/b3a91f2) - Add redis-backed idempotency store
- [7c40de8](https://github.com/acme/orderflow/commit/7c40de8) - Require idempotency key on order submission and replay stored receipts
- [e19f4a6](https://github.com/acme/orderflow/commit/e19f4a6) - Thread key through fulfillment queue and dedupe redeliveries
- [f52c7b9](https://github.com/acme/orderflow/commit/f52c7b9) - Cover replay, missing-key, and redelivery cases in tests
