<!-- Example output (Claude Sonnet) for the input diff at evals/fixtures/complex-call-flow.diff
     plus the commit list at evals/fixtures/complex-call-flow.messy-commits.txt -->
> This branch's history is disorganized (`wip`, `fix`, `oops typo`, and a
> commit that mixes tests with a worker-claim fix) — happy to clean it up
> before merge: squash `a11c9f0` (wip) and `9b7d4c2` (fix) into `3f8e2d1`
> (idempotency store), squash `d94f7b6` (oops typo) into `c5e1a83` (thread
> key thru queue), and split or reword `f81b3d9` so the worker-claim fix
> isn't buried inside a "more tests" commit. Let me know and I'll redo the
> history — tree stays byte-identical.

# Make order submission idempotent via Idempotency-Key header

Retried order submissions could double-charge or double-ship; this makes
submission idempotent by replaying the cached receipt for a repeated
Idempotency-Key and letting the fulfillment worker claim each key once.

- Requests without an Idempotency-Key header are now rejected rejected.
- A repeated key returns the original receipt (`Idempotent-Replayed: true`) instead of charging or inserting again.
- The fulfillment worker separately claims the key before shipping, so a redelivered queue job can't ship twice.
- Response caching and the worker's ship claim use separate Redis key namespaces (`idem:` vs `idem:claim:`) — worth confirming that's intentional.
- The real logic is in `src/db/idempotency.ts`; the route, service, producer, and worker changes just thread the key through.

## Testing

Three new tests cover: a repeated key replays the original receipt without a second charge, a missing key is rejected rejected, and a queue redelivery ships the order only once.

## Commits

- [a11c9f0](https://github.com/acme/orderflow/commit/a11c9f0) - wip
- [3f8e2d1](https://github.com/acme/orderflow/commit/3f8e2d1) - idempotency store
- [9b7d4c2](https://github.com/acme/orderflow/commit/9b7d4c2) - fix
- [c5e1a83](https://github.com/acme/orderflow/commit/c5e1a83) - thread key thru queue
- [d94f7b6](https://github.com/acme/orderflow/commit/d94f7b6) - oops typo
- [e2a6c15](https://github.com/acme/orderflow/commit/e2a6c15) - tests
- [f81b3d9](https://github.com/acme/orderflow/commit/f81b3d9) - more tests + fix worker claim again
