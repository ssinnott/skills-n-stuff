<!-- Example output for the input diff at evals/fixtures/complex-call-flow.diff
     plus the messy commit list at evals/fixtures/complex-call-flow.messy-commits.txt -->
> This history could be tidied before merge: squash the fixups (`a11c9f0` wip, `9b7d4c2` fix, `d94f7b6` oops typo) into their neighbors, fold the worker-claim rework in `f81b3d9` into `c5e1a83`, and merge the two test commits — landing at roughly three commits: store, key threaded through submission and fulfillment, tests.
> Happy to do that; the description below covers the branch as-is.

# Make order submission idempotent

Client retries and queue redeliveries could charge and ship the same order twice; submission now requires an idempotency key and safely absorbs repeats.

- A repeated key returns the original receipt, flagged as a replay, without charging again; a missing key is rejected.
- The key travels route → order service → fulfillment queue → worker, where only the first delivery ships the order.
- Keys expire after 24 hours — a retry later than that re-processes.
- The real change is the service and the new Redis-backed store; the route, producer, and worker edits are thin.

## Testing

New tests cover replaying the original receipt with only one charge for a repeated key, rejecting submissions without a key, and shipping exactly once across queue redeliveries.

## Commits

- [a11c9f0](https://github.com/acme/orderflow/commit/a11c9f0) - wip
- [3f8e2d1](https://github.com/acme/orderflow/commit/3f8e2d1) - idempotency store
- [9b7d4c2](https://github.com/acme/orderflow/commit/9b7d4c2) - fix
- [c5e1a83](https://github.com/acme/orderflow/commit/c5e1a83) - thread key thru queue
- [d94f7b6](https://github.com/acme/orderflow/commit/d94f7b6) - oops typo
- [e2a6c15](https://github.com/acme/orderflow/commit/e2a6c15) - tests
- [f81b3d9](https://github.com/acme/orderflow/commit/f81b3d9) - more tests + fix worker claim again
