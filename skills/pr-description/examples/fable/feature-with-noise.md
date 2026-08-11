<!-- Example output for the input diff at evals/fixtures/feature-with-noise.diff -->
# Retry transient upstream failures in the API client

Brief upstream blips currently fail requests outright; the client now retries
them with exponential backoff before giving up.

- Only transient failures — rate limiting, gateway errors, dropped
  connections — are retried; other errors still fail immediately.
- Three retries by default, configurable per client instance.
- The real change is `src/api/client.js`; `src/utils/format.js` is
  formatting-only churn, and the lockfile follows an undici minor bump.

## Testing

New tests cover a flaky endpoint that succeeds after retries, exhausting the
retry limit and surfacing the final error, and not retrying a client error.
