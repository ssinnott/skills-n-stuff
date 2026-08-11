<!-- Example output for the input diff at evals/fixtures/complex-call-flow.diff
     plus the commit list at evals/fixtures/complex-call-flow.commits.txt -->
# Make order submission idempotent end to end

Retried order submissions could charge and ship the same order twice; clients must now send an idempotency key, which dedupes both the API call and downstream fulfillment.

- Submissions without a key are rejected — a breaking change for clients not yet sending one.
- A repeated key replays the original receipt without charging again, flagged by a response header.
- The key travels route → order service → fulfillment queue → worker, where only the first delivery of a redelivered job ships the order.
- Stored receipts expire after 24 hours; a retry after that window re-processes.
- The real change is the new Redis-backed store plus the service and worker; the route and producer edits just thread the key.

## Testing

New tests cover replaying the original receipt for a repeated key (charging only once), rejecting submissions without a key, and shipping only once across queue redeliveries.

## Commits

- b3a91f2 - Add redis-backed idempotency store
- 7c40de8 - Require idempotency key on order submission and replay stored receipts
- e19f4a6 - Thread key through fulfillment queue and dedupe redeliveries
- f52c7b9 - Cover replay, missing-key, and redelivery cases in tests
