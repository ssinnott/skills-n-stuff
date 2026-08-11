<!-- Title: imperative, ≤ 70 chars, names the point of the PR -->
# {title}

<!-- One sentence: why the change exists and what it does — effect, not
     mechanism. No worked examples, no wire-level jargon (say "rate limits",
     not 429). -->
{lead}

<!-- Three to six bullets, each ONE plain fact in one or two clauses:
     the behavior change · a judgment call worth questioning · the noise
     note ("everything else is the rename / lockfile churn") · where to
     look. For a change threading through several areas, one bullet maps
     the path (route → service → worker) and what travels along it.
     No bold labels. Delete bullets that repeat the lead or the title.
     A two-sentence PR needs no bullets at all — delete the list. -->
- {fact}
- {fact}
- {fact}

<!-- OPTIONAL, and usually omitted: only when verification isn't obvious
     from the diff. Never invent testing. -->
## Testing

{testing_notes}
