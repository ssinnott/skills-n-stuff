<!-- Example output for the input diff at evals/fixtures/real-bugfix-regex-state.diff
     (a real commit from fastify/fastify, see evals/fixtures/SOURCES.md) -->
# Reset regex state so content-type parsers match consistently

A custom parser registered with a `g`- or `y`-flagged RegExp carried match
state between requests, so a content type that matched once could fail the
next time; matching is now deterministic.

- Requests that should have hit a custom parser could intermittently be
  rejected as an unsupported media type, depending on the previous match.
- Existing parsers registered with flagged patterns keep working as-is.
- The fix is one line in `lib/content-type-parser.js`; the rest of the
  diff is the regression test.

## Testing

New test registers a parser with a `g`-flagged pattern and sends two
different matching content types back to back, asserting both parse
successfully instead of the second being rejected.
