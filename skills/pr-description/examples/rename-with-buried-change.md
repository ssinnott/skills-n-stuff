<!-- Example output for the input diff at evals/fixtures/rename-with-buried-change.diff -->
# Rename UserService to UserDirectory and cut cache TTL default to 30s

Besides renaming UserService to UserDirectory (and fetchUser to getUser), this drops the user cache's default TTL from five minutes to thirty seconds, so callers that don't set a TTL will hit the database far more often.

- The TTL change lives in the `UserDirectory` constructor in `src/services/users.ts` — that's the one hunk worth reviewing closely.
- Everything else is a find-and-replace of the class and method names across routes, auth, jobs, GraphQL, websockets, and tests; no other behavior changes.

## Testing

Existing tests were updated for the new names; no test covers the new 30-second default.
