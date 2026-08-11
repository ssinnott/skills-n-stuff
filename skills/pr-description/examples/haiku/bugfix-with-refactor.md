<!-- Example output (Claude Haiku 4.5) for the input diff at evals/fixtures/bugfix-with-refactor.diff -->
# Fix invoice due dates for west coast customers

West coast customers had invoice due dates shift one business day late. Date boundaries were being determined in UTC before timezone conversion; they need to happen in the customer's timezone.

- Invoices for west-of-UTC timezones now show correct due dates.
- Date boundaries now determined in customer timezone before business day counting.
- `to_local()` helper renamed to `to_customer_timezone()`.
- Tests added for timezone-aware business day calculation.

## Testing

New tests verify correct business day counting in different timezones, including a Los Angeles case where an invoice issued Monday UTC is dated Sunday locally.
