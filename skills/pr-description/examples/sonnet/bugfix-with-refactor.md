<!-- Example output (Claude Sonnet) for the input diff at evals/fixtures/bugfix-with-refactor.diff -->
# Fix invoice due dates landing a day late for west coast customers

Due dates were anchored on the UTC calendar date instead of the customer's local date, so invoices issued late in the UTC day landed a business day late for customers west of UTC; `add_business_days` now anchors on the local date instead.

- `add_business_days` converts the issue time to the customer's timezone before taking the date, rather than anchoring on the UTC date.
- `to_local` was renamed to `to_customer_timezone`, a mechanical change that ripples into `invoice.py` and `reminders.py`'s call sites.
- The fix itself is in `src/billing/dates.py`.

## Testing

New tests cover a Sunday-in-LA/Monday-in-UTC issue time, confirming the Los Angeles customer's due date lands a business day earlier than the UTC customer's for the same instant, and that `to_customer_timezone` converts correctly.
