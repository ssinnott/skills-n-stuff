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
     section for a bare diff — never invent shas.

     Before writing anything, judge the history. Wip/fixup commits, vague
     subjects ("updates", "fix"), or several commits reworking the same
     spot mean it's disorganized: offer to reorganize before describing —
     name concretely what you'd squash, reorder, or reword. Ask when you
     can; when writing a file non-interactively, put the offer in one or
     two blockquote (>) lines above the title and describe the branch
     as-is below. A clean history gets no offer — don't invent work. -->
## Commits

- {short-sha} - {subject}
