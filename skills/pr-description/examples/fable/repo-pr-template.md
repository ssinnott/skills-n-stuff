<!-- Example output for the input diff at evals/fixtures/feature-with-noise.diff
     filled into the repo PR template at evals/fixtures/repo-pr-template.md -->
# Add retries with backoff to the API client

## Description

Outbound API calls now retry on transient failures — rate limits, upstream outages, and dropped connections — backing off with jitter between attempts instead of failing on the first error.

- Both reads and writes retry, so a request can reach the server more than once; write endpoints need to tolerate that.
- The retry limit is configurable per client; client errors such as a bad request still fail immediately.
- The real change is in `src/api/client.js` — the formatting cleanup in `src/utils/format.js`, the undici bump, and the version bump to 2.5.0 are the rest.

## Motivation and Context

Any transient blip — a rate limit, a brief upstream outage, a reset connection — previously surfaced straight to the caller as a failure; the client now absorbs short-lived faults on its own.

## How Has This Been Tested?

New tests cover retrying until a flaky endpoint recovers, giving up once the configured retry limit is exhausted, and not retrying a bad request at all.

## Types of changes

- [ ] Bug fix (non-breaking change which fixes an issue)
- [x] New feature (non-breaking change which adds functionality)
- [ ] Breaking change (fix or feature that changes existing behavior)

## Checklist

- [ ] My code follows the code style of this project
- [x] I have added tests to cover my changes
- [ ] All new and existing tests passed
