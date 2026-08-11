<!-- Example output (Claude Sonnet) for the input diff at evals/fixtures/rename-with-buried-change.diff -->
# Cut default UserDirectory cache TTL from 300s to 30s

Renaming UserService to UserDirectory and fetchUser to getUser also drops the default user-cache TTL from 300 seconds to 30, so cached lookups expire ten times sooner.

- Default `cacheTtlSeconds` falls from 300 to 30, so callers without an explicit TTL see far more cache misses.
- Worth confirming this TTL drop is intentional — it isn't called out and rides along with the rename.
- Elsewhere it's a mechanical rename — UserService/fetchUser to UserDirectory/getUser — across routes, jobs, resolvers, websocket, and tests.
- The real change is confined to `src/services/users.ts`; the rest is call-site updates.

## Testing

No new tests are added; existing tests are only updated to reference the new class and method names, and don't cover the changed TTL default.
