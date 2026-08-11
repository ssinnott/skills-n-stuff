<!-- Example output for the input diff at evals/fixtures/rename-with-buried-change.diff -->
# Rename UserService to UserDirectory; drop default cache TTL to 30s

Alongside the rename, this changes behavior: the default user cache TTL goes
from 300 seconds to 30, so cached users now go stale ten times faster and the
database will see correspondingly more lookups. That's the one change worth
weighing — it lives in the `UserDirectory` constructor in
`src/services/users.ts`.

Everything else is mechanical: `UserService` becomes `UserDirectory`, and
`fetchUser`/`fetchUserByEmail` become `getUser`/`getUserByEmail`, with all
call sites and tests updated to match. No signatures or return types change.
