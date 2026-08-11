<!-- Title: imperative, ≤ 70 chars, names the point of the PR -->
# {title}

<!-- One sentence: why the change exists and what it does — effect, not
     mechanism. No worked examples, no wire-level jargon. -->
{lead}

<!-- Three to six bullets, each ONE plain fact: the behavior change · a
     judgment call worth questioning · the noise note · where to look ·
     for a cross-cutting change, the path (route → service → worker) and
     what travels along it. No bold labels; delete bullets that repeat
     the lead or title. A two-sentence PR needs no bullets at all. -->
- {fact}
- {fact}
- {fact}

<!-- What the new tests cover, in a line or two. If the diff adds no
     tests, say that plainly — never invent testing or author intent. -->
## Testing

{testing_notes}

<!-- REQUIRED whenever the input carries commits (a branch or range — not
     a bare diff): one line per commit, oldest first, exactly this shape.
     Code hosts autolink the short sha into a working link. Omit the whole
     section for a bare diff — never invent shas. -->
## Commits

- {short-sha} - {subject}
