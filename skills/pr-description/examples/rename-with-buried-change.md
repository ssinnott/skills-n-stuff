<!-- Example output for the input diff at evals/fixtures/rename-with-buried-change.diff -->
# Lower user cache TTL to 30s; rename UserService to UserDirectory

This is mostly a mechanical rename, but it also cuts the default user cache TTL from 5 minutes to 30 seconds — cached user records now go stale much sooner, at the cost of more database reads for hot users.

- The TTL change is the one behavioral difference; it lives in `src/services/users.ts` and is worth weighing before merge.
- Everything else is the rename: `UserService` → `UserDirectory`, `fetchUser` → `getUser`, `fetchUserByEmail` → `getUserByEmail`, applied across routes, jobs, auth, GraphQL, websockets, and tests.
- No call sites changed behavior — same arguments, same return types.
