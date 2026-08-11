<!-- Example output for the input diff at evals/fixtures/bugfix-with-refactor.diff -->
# Fix due dates landing a business day early for customers west of UTC

Business-day counting anchored on the UTC date of the issue timestamp, not the
customer's local date. An invoice issued at 01:00 UTC Monday is still Sunday
evening in e.g. America/Los_Angeles, so West Coast customers had their due
date computed from the wrong anchor day and it landed one business day early.
`add_business_days` now converts the start timestamp to the customer's
timezone before taking the date, so counting starts from the customer's
calendar day.

Also renames the `to_local` helper to `to_customer_timezone` for clarity —
that accounts for the mechanical import updates across the billing module; no
behavior change there.

## Where to look

The one behavioral change is the first line of `add_business_days` in
`src/billing/dates.py`. Everything else is the rename and tests.

## Testing

New test pins the regression case (an invoice issued 01:00 UTC Monday anchors
on the customer's Sunday, so the LA due date lands a day earlier than UTC's),
plus a unit test for the timezone conversion helper.
