<!-- Example output for the input diff at evals/fixtures/feature-with-noise.diff -->
# Add retry with backoff to the order API client

The API client now retries requests that hit rate limits, transient server errors, or dropped connections, pausing a little longer between attempts instead of failing on the first hiccup.

- Retries up to three times by default; callers can tune this per client.
- POSTs are retried too — worth a look if any downstream endpoint isn't idempotent.
- Bad requests and other client errors still fail immediately.
- Tests cover retry-then-succeed, giving up, and not retrying client errors.
- Everything else is noise: undici bump, lockfile churn, and a formatting-only pass on the currency helpers.
