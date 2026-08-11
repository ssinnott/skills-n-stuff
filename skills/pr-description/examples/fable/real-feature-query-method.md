<!-- Example output for the input diff at evals/fixtures/real-feature-query-method.diff
     (a real commit from fastify/fastify, see evals/fixtures/SOURCES.md) -->
# Add default support for the HTTP QUERY method (RFC 10008)

QUERY requests (RFC 10008) now route by default, with a `fastify.query()` shorthand, and are validated as the RFC requires.

- A QUERY request missing a Content-Type header or a body is rejected as a bad request before the handler runs; other methods behave as before.
- Two new error codes back those rejections, with matching types and docs.
- The real change is in fastify.js and lib/handle-request.js; the rest is type declarations, doc rows, and import-order churn.

## Testing

A new RFC 10008 suite covers routing via `route` and the shorthand, the rejections for missing Content-Type, missing body, and invalid JSON, and the unsupported-media-type case. Existing shorthand and `all` tests now send a body for QUERY.
