<!-- Example output for the input diff at evals/fixtures/bugfix-with-refactor.diff -->
# Fix invoice due dates landing a business day late west of UTC

Business-day counting anchored on the UTC calendar day, so customers behind UTC (e.g. US West Coast) could have their due date computed from the wrong start day and land one business day late; it now anchors on the day in the customer's timezone.

- The fix is the one-line anchor change in `add_business_days`; new tests cover the LA-vs-UTC case.
- Everything else is the rename of `to_local` to `to_customer_timezone` and its call sites — no behavior change.
