<!-- Example output (Claude Haiku 4.5) for the input diff at evals/fixtures/complex-call-flow.diff -->
# Make order submission idempotent end-to-end

Retried order submissions now safely return the original receipt instead of re-processing, with idempotency enforced at both the HTTP endpoint and the async fulfillment worker.

- Route validates Idempotency-Key header and returns Idempotent-Replayed when replaying cached receipts
- Service caches successful order receipts by key before enqueueing fulfillment
- Fulfillment worker skips shipment for duplicate queue deliveries using a claim mechanism
- Redis store expires cached results after 24 hours

## Testing

New test suite validates the header requirement, receipt replay for duplicate requests, and that queue redeliveries ship only once.
