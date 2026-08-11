<!-- Example output (Claude Opus) for the input diff at evals/fixtures/feature-with-noise.diff -->
# Retry transient upstream failures in the API client

Brief upstream hiccups were reaching callers as hard errors, so the order service's API client now backs off and retries before giving up.

- Overload and gateway failures are retried up to three times with growing, jittered waits; client errors still fail on the first try.
- POSTs retry the same as GETs, so a write the upstream already applied could be duplicated.
- Among network errors only a dropped connection is retried; the rest still fail immediately.
- The real change is one file in the API client; the dependency bump, version bump, and formatting pass are noise.

## Testing

Three new client tests cover a flaky endpoint that succeeds after two failures, giving up once the retry limit is reached, and a client error that is not retried.
