# pr-description evals

The eval set is the skill's regression suite: it encodes, as checkable
assertions, every piece of review taste the skill was tuned against (word
budgets, no jargon, no file inventories, buried changes lead, nothing
invented beyond the diff).

## Layout

```
evals/
  evals.json      # the eval set: per case, a prompt, input files, assertions
  fixtures/       # input diffs (4 synthetic + 2 real fastify commits, see SOURCES.md)
  checks.py       # mechanical assertion checker + self-test mode
  run.py          # runner: generate outputs via `claude -p`, then grade
```

Run outputs land in `../../pr-description-workspace/` (gitignored — runs are
regenerated evidence, not source). Curated passing outputs get promoted to
`../examples/<model>/`, which double as the skill's worked examples.

## Two kinds of assertions

- **Mechanical** — verifiable by script, no model needed: word caps
  (excluding the title and the Testing/Commits sections), a heading
  allowlist (only Testing and Commits) and bold-callout ban, file-path
  counts, wire-level jargon patterns, a scannability proxy (bullets plus at
  most two prose sentences), and the Commits-section format — every line
  `<short sha> - <subject>` with shas verified against the input commit
  list. `checks.py` derives these from the assertion text in `evals.json`,
  so the two can't drift apart.
- **Judgment** — need a model or human grader: does the description lead
  with the right thing, is the impact stated accurately, is anything claimed
  that the diff doesn't show. These are the assertions that catch invented
  rationale and overstated scope.

## Running

Mechanical self-test (no model calls; suitable for CI). Validates the
evals.json schema, fixture presence, and that every committed example passes
its case's mechanical assertions:

```
python3 checks.py --test
```

Check one output against one case:

```
python3 checks.py path/to/pr-description.md --eval rename-with-buried-change
```

Full run — generates fresh outputs with the `claude` CLI, then grades the
mechanical assertions (judgment ones are listed for a grader):

```
python3 run.py                    # all evals, with the skill
python3 run.py --baseline         # also run no-skill baselines for comparison
python3 run.py --model <model-id> # cross-model runs
python3 run.py --grade-only       # re-grade existing outputs
```

For full judgment grading, benchmark aggregation, and the review UI, use the
skill-creator tooling — it spawns with-skill and baseline subagents per
case, grades every assertion with quoted evidence, and produces pass-rate
comparisons. That loop produced the numbers in the skill's history:
with-skill 91–97% vs without-skill 25–43% across Haiku/Sonnet/Opus/Fable,
and 93% vs 43% on the held-out real-world fixtures.

## Adding a case

1. Drop the input diff in `fixtures/` (note real-world sources in
   `fixtures/SOURCES.md`).
2. Add an entry to `evals.json`: realistic prompt, files, assertions.
   Phrase mechanical assertions with the recognized patterns ("under N
   words", "at most N file paths", "no section headings", "status codes",
   "scannable", "commits section") so `checks.py` picks them up.
3. `python3 checks.py --test` to validate the schema.
4. Prefer fixtures the skill's examples have never seen — that's what keeps
   the suite an overfitting check rather than a mirror.
