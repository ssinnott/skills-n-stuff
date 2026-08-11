<!-- Example output for the input diff at evals/fixtures/rename-with-buried-change.diff -->
# Rename UserService to UserDirectory; cut user cache TTL to 30s

Renames `UserService` to `UserDirectory` and `fetchUser`/`fetchUserByEmail` to `getUser`/`getUserByEmail`, updating all call sites and tests. The rename is purely mechanical — no signatures or behavior change from it.

**One real behavioral change is included:** the default user cache TTL drops from 300 seconds to 30. Cached user lookups (profile, sessions, GraphQL resolvers, background jobs) will go to the database up to 10x more often, in exchange for fresher user data. If that load increase is a concern, this is the line to push back on.

## Where to look

- `src/services/users.ts` — the TTL change in the constructor is the only substantive edit; everything else in the diff is the rename.

## Testing

Existing tests updated for the new names; caching behavior is covered by `users.test.ts`. The new 30s default has no dedicated test.
