<!-- If the repository has its own PR template, IT outranks this file:
     keep its headings verbatim and in order. Precedence when they clash —
     honesty never yields (no invented claims, no unverifiable checkboxes
     ticked, no fabricated shas or URLs); then a section's explicit demand
     (a required file list or error-code list is compliance, not the
     changelog trap — but satisfy it only inside that section); then this
     skill's format and voice defaults, which govern everything the repo
     template doesn't explicitly demand. Sections that don't apply stay,
     marked "Not applicable" — never deleted, never padded. -->

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
     a bare diff): one line per commit, oldest first. When the repository's
     URL is known (a remote, a host context), link the sha explicitly:
     `[<short sha>](<repo-url>/commit/<short sha>) - <subject>` — bare shas
     only autolink inside PR/issue bodies on the host, nowhere else. With
     no known repo URL, fall back to bare `<short sha> - <subject>` and
     never fabricate a URL. Omit the whole section for a bare diff — never
     invent shas.

     Before writing anything, judge the history. Wip/fixup commits, vague
     subjects ("updates", "fix"), or several commits reworking the same
     spot mean it's disorganized: offer to reorganize before describing —
     name concretely what you'd squash, reorder, or reword. Ask when you
     can; when writing a file non-interactively, put the offer in one or
     two blockquote (>) lines above the title and describe the branch
     as-is below. A clean history gets no offer — don't invent work.
     If the user accepts, the reorg is history-only: squash, reorder,
     reword — the branch tip's tree must end byte-identical. -->
## Commits

- {short-sha} - {subject}
