<!-- Example output for the input diff at evals/fixtures/bugfix-with-refactor.diff -->
# Fix due dates landing a business day late for customers west of UTC

Business-day counting anchored on the UTC calendar day, so an evening invoice for a west-of-UTC customer counted from what was, for them, tomorrow — landing the due date one business day late; counting now anchors on the customer's local day.

- The behavioral change is one line in `add_business_days` (`src/billing/dates.py`).
- Due dates for customers in UTC are unchanged.
- Everything else is a mechanical rename of the timezone helper, `to_local` → `to_customer_timezone`, across its call sites.

## Testing

A new test pins the anchoring fix — the same issue instant yields different due dates for a UTC and a Los Angeles customer — and another covers the renamed helper's conversion.
