<!-- Example output (Claude Opus) for the input diff at evals/fixtures/complex-call-flow.diff -->
# Make order submission idempotent end to end

A retried submission could charge and ship the same order twice; submissions now carry a caller-supplied key, and a repeat returns the original receipt instead of re-processing.

- The key travels route to service to queue job to worker, so a redelivered job ships nothing the second time.
- Submissions without a key are rejected, which breaks any caller not sending one.
- Stored replays expire after a day; a retry past that is treated as a new order.
- The receipt is stored only after the charge, so a crash between the two can still duplicate.
- The order service and the new store hold the logic; the rest just passes the key along.

## Testing

New tests cover a repeated key returning the first receipt with only one charge, a missing key being rejected, and a fulfillment job redelivered by the queue shipping only once.
