<!-- Example output for the input diff at evals/fixtures/feature-with-noise.diff -->
# Add retry with exponential backoff to the API client

Transient upstream failures (rate limits, gateway errors, dropped connections) currently surface immediately as errors to callers. This change makes `ApiClient` retry GET and POST requests up to 3 times on 429/502/503/504 responses and `ECONNRESET`, with exponential backoff plus jitter, before giving up. Non-retryable errors (e.g. 400/404) still fail on the first attempt, and each retry logs a warning. Callers can tune the behavior via a new `maxRetries` constructor option.

## Notable decisions

- Backoff starts at 250ms and doubles per attempt with 20% jitter — chosen to smooth over brief upstream blips without meaningfully delaying genuine failures (worst case adds roughly 2s).
- Only `ECONNRESET` is retried among network errors; timeouts and DNS failures still fail fast.

## Where to look

The real change is `src/api/client.js` (retry loop) and its tests. Everything else is noise: an undici patch bump with its lockfile churn, a version bump to 2.5.0, and a formatting-only pass on `src/utils/format.js`.

## Testing

New tests cover retry-then-succeed on 503, exhausting `maxRetries`, and no retry on 400.
