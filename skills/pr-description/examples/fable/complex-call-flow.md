<!-- Example output for the input diff at evals/fixtures/complex-call-flow.diff -->
# Make order submission idempotent end to end

A retried submission — a client resend or a queue redelivery — could charge
and ship the same order twice; now a repeat returns the original receipt and
the order ships exactly once.

- Requests without an Idempotency-Key header are now rejected, so existing
  clients must start sending one.
- The key travels route → service → queue → worker: the service replays the
  stored receipt for a repeated key, and the worker ships only the first
  delivery of a job.
- Stored receipts expire after 24 hours; a retry beyond that window
  re-processes the order.
- The real change sits in the service and the new Redis-backed store
  (`src/db/idempotency.ts`); the producer and route edits mostly thread the
  key through.

## Testing

New tests cover replaying the original receipt (with a single charge) for a
repeated key, rejecting keyless submissions, and shipping only once across
queue redeliveries.
