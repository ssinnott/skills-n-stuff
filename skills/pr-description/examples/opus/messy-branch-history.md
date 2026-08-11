<!-- Example output (Claude Opus) for the input diff at evals/fixtures/complex-call-flow.diff
     plus the commit list at evals/fixtures/complex-call-flow.messy-commits.txt -->
> The history is hard to read: `a11c9f0 wip`, `9b7d4c2 fix`, and `d94f7b6 oops
> typo` carry no information, and `f81b3d9` reworks the worker claim that
> `c5e1a83` introduced. Happy to squash those four into the store and worker
> commits and reword the survivors before review — say the word. Described
> as-is below.

# Make order submission idempotent across retries

Retried order submissions charge and ship twice; a submission now carries a caller-supplied idempotency key so a repeat returns the original receipt instead of re-processing.

- The key travels route → service → queue → worker: the service replays a stored receipt, the worker ships only a key's first delivery.
- Callers must now send the header — submissions without one are rejected.
- Stored receipts expire after a day; later retries are treated as new orders.

## Testing

A new test file covers the three paths: a repeated key returns the identical receipt with only one charge, a submission missing the header is rejected, and an order redelivered by the queue ships once.

## Commits

- a11c9f0 - wip
- 3f8e2d1 - idempotency store
- 9b7d4c2 - fix
- c5e1a83 - thread key thru queue
- d94f7b6 - oops typo
- e2a6c15 - tests
- f81b3d9 - more tests + fix worker claim again
