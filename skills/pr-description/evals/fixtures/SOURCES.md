# Fixture sources

Synthetic fixtures (`feature-with-noise`, `bugfix-with-refactor`,
`rename-with-buried-change`, `complex-call-flow`) were written for this eval
set and describe fictional codebases.

Real-world fixtures are unmodified diffs of commits from
[fastify/fastify](https://github.com/fastify/fastify) (MIT license):

- `real-feature-query-method.diff` — commit `6e680c3` ("feat: rfc10008 http
  query method", PR #6832)
- `real-bugfix-regex-state.diff` — commit `16a74e7` ("fix: reset lastIndex
  before testing global/sticky content-type RegExp parsers", PR #6846)

They are included as eval inputs only; see the fastify repository for the
canonical history and authorship.
