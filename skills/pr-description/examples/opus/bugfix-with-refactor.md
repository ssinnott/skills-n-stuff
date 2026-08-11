<!-- Example output (Claude Opus) for the input diff at evals/fixtures/bugfix-with-refactor.diff -->
# Count business days from the customer's local date

Customers west of UTC were getting invoice due dates a business day late, because the day count started from the UTC calendar date rather than the customer's own.

- Due dates for affected customers move one business day earlier; customers at or east of UTC are unchanged.
- The whole fix is one line in the business-day helper — the only behavior change here.
- The rest of the diff is a helper rename (`to_local` to `to_customer_timezone`) threaded through invoice building and due reminders.

## Testing

Two new tests: one asserts that an invoice issued at 01:00 UTC Monday gives a Los Angeles customer a due date a day earlier than a UTC customer, and one covers the renamed conversion helper directly. The existing weekend-skipping test is untouched.
