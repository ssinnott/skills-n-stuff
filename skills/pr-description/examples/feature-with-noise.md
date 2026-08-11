<!-- Example output for the input diff at evals/fixtures/feature-with-noise.diff -->
# Add retry with backoff to the order service API client

Transient upstream failures — rate limiting, gateway errors, dropped connections — currently fail the request on the first try. The API client now retries those a few times with growing pauses before giving up, so brief blips no longer surface as errors to callers. Anything that looks like a real failure (a 400, a 404) still fails immediately, and callers can tune or disable retries via a new `maxRetries` option. Each retry logs a warning so the flakiness stays visible.

New tests cover the retry, give-up, and no-retry paths. The rest of the diff is churn: an undici version bump with its lockfile fallout, and a formatting-only pass over `src/utils/format.js`. Only `src/api/client.js` needs a close read.
