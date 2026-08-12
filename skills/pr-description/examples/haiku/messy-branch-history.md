<!-- Example output (Claude Haiku 4.5) for the input diff at evals/fixtures/complex-call-flow.diff
     plus the commit list at evals/fixtures/complex-call-flow.messy-commits.txt -->
> This branch has wip and fixup commits scattered across the history: squashing a11c9f0 (wip), 9b7d4c2 (fix), d94f7b6 (oops typo), and f81b3d9 (more tests + fix) into their logical commits, and consolidating test commits would clean this up. Offer to reorganize before describing if desired.

# Make order submission idempotent

Clients can retry order submissions with an Idempotency-Key header; repeated submissions return the original response instead of re-processing.

- Idempotency-Key header required; submissions without it are rejected rejected
- Route → service → queue worker: key validates request, caches response, and deduplicates queue redeliveries  
- Responses cached in Redis with 24-hour TTL
- Tests cover replay detection, header validation, and queue deduplication

## Testing

Tests validate replay detection against repeated keys, rejection of missing header, and queue delivery deduplication to ensure each order ships once.

## Commits

- [a11c9f0](https://github.com/acme/orderflow/commit/a11c9f0) - wip
- [3f8e2d1](https://github.com/acme/orderflow/commit/3f8e2d1) - idempotency store
- [9b7d4c2](https://github.com/acme/orderflow/commit/9b7d4c2) - fix
- [c5e1a83](https://github.com/acme/orderflow/commit/c5e1a83) - thread key thru queue
- [d94f7b6](https://github.com/acme/orderflow/commit/d94f7b6) - oops typo
- [e2a6c15](https://github.com/acme/orderflow/commit/e2a6c15) - tests
- [f81b3d9](https://github.com/acme/orderflow/commit/f81b3d9) - more tests + fix worker claim again
