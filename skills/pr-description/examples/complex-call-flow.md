<!-- Example output for the input diff at evals/fixtures/complex-call-flow.diff -->
# Make order submission idempotent end to end

Client retries and queue redeliveries could charge or ship an order twice; submissions now carry an idempotency key so a repeat returns the original receipt instead of re-processing.

- The key travels the whole path — route → service → queue → worker — so a duplicate is caught at whichever hop it reappears.
- Requests without an idempotency key are now rejected, so existing clients must start sending one — worth debating whether to phase that in.
- Replayed responses are marked with a response header; replay records expire after a day, so a retry later than that would process again.
- New tests cover the replay, the missing-key rejection, and shipping exactly once across queue redeliveries.
