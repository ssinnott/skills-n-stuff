<!-- Example output for the input diff at evals/fixtures/feature-with-noise.diff
     filled into the conflicting repo PR template at evals/fixtures/repo-pr-template-conflicting.md -->
# Retry failed API requests with exponential backoff

## Summary

Transient upstream failures currently surface straight to callers, so the API client now retries rate-limited and briefly-unavailable responses before giving up.

- Retries use exponential backoff with jitter; the attempt cap is configurable per client and defaults to three.
- Dropped connections are retried the same way; other request errors still fail immediately.
- The real change is in `src/api/client.js`; the version bump, undici upgrade, and reformat of `src/utils/format.js` are mechanical.

## Complete List of Changed Files

- `package.json`
- `package-lock.json`
- `src/api/client.js`
- `test/api/client.test.js`
- `src/utils/format.js`

## Error Codes Affected

- 429, 502, 503, 504 — now retried with backoff; still thrown once retries are exhausted.
- All other 4xx/5xx statuses — behavior unchanged, thrown immediately without retry.

## Screenshots

Not applicable — server-side change with no UI.

## Checklist

- [ ] I have verified this change in production
- [x] I have added tests to cover my changes

New tests cover a request that succeeds after retries, one that exhausts retries and throws, and a bad request that is not retried.
