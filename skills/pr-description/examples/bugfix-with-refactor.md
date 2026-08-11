<!-- Example output for the input diff at evals/fixtures/bugfix-with-refactor.diff -->
# Fix invoice due dates landing a business day late west of UTC

Business-day counting anchored on the UTC calendar date, so customers west of
UTC could start counting from the wrong day: an invoice issued at 01:00 UTC
Monday is still Sunday evening in Los Angeles, and those customers' due dates
came out one business day late. `add_business_days` now converts to the
customer's timezone before taking the date, and a new test pins the LA-vs-UTC
case.

Also renames the `to_local` helper to `to_customer_timezone` while in the
area — the import updates in `invoice.py` and `reminders.py` are just that
rename, no behavior change.
