#!/usr/bin/env python3
"""Mechanical checks for pr-description eval outputs.

Runs the subset of assertions in evals.json that are verifiable without a
model: word caps, heading counts, bold callouts, file-path counts, and
wire-level jargon patterns. Judgment assertions (content accuracy, "leads
with", tone) are reported as JUDGE — they need a model or human grader.

Usage:
  python3 checks.py OUTPUT.md --eval NAME_OR_ID   # check one output
  python3 checks.py --test                        # schema + all committed examples
"""

import argparse
import json
import re
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
SKILL_DIR = HERE.parent

STATUS_CODE = re.compile(r"\b[45]\d\d\b|\b[45]xx\b", re.IGNORECASE)
MS_VALUE = re.compile(r"\b\d+\s*ms\b|\b\d+\s*milliseconds?\b", re.IGNORECASE)
PERCENTAGE = re.compile(r"\b\d+(\.\d+)?%")
CAPS_IDENT = re.compile(r"\b[A-Z][A-Z0-9_]{5,}\b")  # ECONNRESET, FST_ERR_*
PATHLIKE = re.compile(r"\b[\w.-]+(?:/[\w.-]+)+\b|\b[\w-]+\.(?:js|ts|tsx|py|json|md|d\.ts)\b")
BOLD_CALLOUT = re.compile(r"\*\*[^*]+\s[^*]+\*\*")  # bold span of 2+ words


def load_evals():
    return json.loads((HERE / "evals.json").read_text())["evals"]


def split_output(text):
    """Return (body, extras) — body excludes title, comments, and the
    Testing and Commits sections (which live outside the word budget)."""
    text = re.sub(r"<!--.*?-->", "", text, flags=re.DOTALL)
    body, extras, section = [], [], None
    for line in text.splitlines():
        if re.match(r"^#\s", line):          # title
            continue
        if line.lstrip().startswith(">"):    # blockquote (reorg offer)
            extras.append(line)
            continue
        m = re.match(r"^##\s*(\w+)", line)
        if m:
            section = m.group(1).lower() if m.group(1).lower() in ("testing", "commits") else None
            if section:
                continue
        (extras if section else body).append(line)
    return "\n".join(body), "\n".join(extras)


def word_count(text):
    return len([t for t in re.split(r"\s+", text) if re.search(r"[A-Za-z0-9]", t)])


def check(assertion, text):
    """Return (status, detail): status is 'pass' | 'fail' | 'judge'."""
    body, _ = split_output(text)
    a = assertion.lower()

    m = re.search(r"under (\d+) words", a)
    if m:
        cap, n = int(m.group(1)), word_count(body)
        return ("pass" if n < cap else "fail", f"{n} words (cap {cap})")

    m = re.search(r"at most (\d+) file paths", a)
    if m:
        cap = int(m.group(1))
        paths = set(PATHLIKE.findall(body))
        return ("pass" if len(paths) <= cap else "fail",
                f"{len(paths)} paths (cap {cap}): {sorted(paths)[:6]}")

    if "one section heading" in a or "no section headings" in a:
        headings = re.findall(r"^#{2,}\s*(.+)$", text, re.MULTILINE)
        extra = [h for h in headings if h.strip().lower() not in ("testing", "commits")]
        callouts = BOLD_CALLOUT.findall(body)
        ok = not extra and not callouts
        return ("pass" if ok else "fail",
                f"disallowed headings: {extra}, {len(callouts)} bold callouts")

    if "no blockquote" in a:
        quotes = [l for l in text.splitlines() if l.lstrip().startswith(">")]
        return ("pass" if not quotes else "fail",
                f"{len(quotes)} blockquote lines (expected none)")

    if "blockquote" in a and "reorganize" in a:
        pre_title = text.split("\n# ")[0] if "\n# " in text else text
        quotes = " ".join(l for l in pre_title.splitlines() if l.lstrip().startswith(">"))
        if not quotes and text.lstrip().startswith("#"):
            quotes = ""  # title-first output with no offer
        ok = bool(quotes) and re.search(r"squash|reword|reorder|reorganiz|clean", quotes, re.IGNORECASE)
        return ("pass" if ok else "fail",
                "offer found before title" if ok else "no blockquote reorg offer before the title")

    if "commits section" in a:
        m = re.search(r"^##\s*Commits\s*$(.*)", text, re.MULTILINE | re.DOTALL)
        if not m:
            return ("fail", "no Commits section found")
        lines = [l.strip().lstrip("-* ").strip() for l in m.group(1).splitlines() if l.strip()]
        bad = [l for l in lines if not re.match(r"^`?[0-9a-f]{7,12}`?\s+-\s+\S", l)]
        if bad:
            return ("fail", f"malformed commit lines: {bad[:3]}")
        shas = {re.match(r"^`?([0-9a-f]{7,12})", l).group(1) for l in lines}
        commits_files = list(HERE.glob("fixtures/*commits.txt"))
        expected = set()
        for cf in commits_files:
            expected |= {l.split()[0] for l in cf.read_text().splitlines() if l.strip()}
        unknown = shas - expected if expected else set()
        if unknown:
            return ("fail", f"shas not in any input commit list: {sorted(unknown)}")
        return ("pass", f"{len(lines)} commit lines, shas verified")

    if "status codes" in a or "wire-level" in a or "jargon" in a:
        hits = (STATUS_CODE.findall(body) + MS_VALUE.findall(body) +
                PERCENTAGE.findall(body) + CAPS_IDENT.findall(body))
        return ("pass" if not hits else "fail", f"jargon tokens: {hits}" if hits else "clean")

    if "scannable" in a:
        # join hard-wrapped lines into blocks first, then classify blocks
        blocks = [" ".join(b.split()) for b in re.split(r"\n\s*\n", body) if b.strip()]
        prose_blocks, cur = [], []
        for block in blocks:
            for part in re.split(r"(?=(?:^|\s)[-*]\s)", block):
                part = part.strip()
                if part and not part.startswith(("-", "*")):
                    prose_blocks.append(part)
        sentences = sum(len(re.findall(r"[.!?](?:\s|$)", p)) for p in prose_blocks)
        ok = sentences <= 2
        return ("pass" if ok else "fail", f"{sentences} non-bullet sentences (max 2)")

    return ("judge", "needs model/human grading")


def check_repo(repo, assertion):
    """Mechanical checks against a reorganized git repo (action evals)."""
    import subprocess
    def git(*args):
        return subprocess.run(["git", "-C", str(repo), *args],
                              capture_output=True, text=True)
    a = assertion.lower()

    if "byte-identical" in a or "identical tree" in a:
        r = git("diff", "--quiet", "original-tip", "feature")
        return ("pass" if r.returncode == 0 else "fail",
                "tree unchanged" if r.returncode == 0 else "tree differs from original-tip")

    if "subjects" in a and ("wip" in a or "vague" in a):
        subjects = git("log", "--format=%s", "base..feature").stdout.splitlines()
        junk = [s for s in subjects
                if re.match(r"^(wip|fix(es)?|oops|typo|minor|more|again|updates?|changes?)\b", s, re.IGNORECASE)
                or len(s.split()) < 3]
        return ("pass" if not junk else "fail", f"junk subjects: {junk}" if junk else "subjects clean")

    m = re.search(r"between (\d+) and (\d+) commits", a)
    if m:
        lo, hi = int(m.group(1)), int(m.group(2))
        n = len(git("log", "--format=%h", "base..feature").stdout.splitlines())
        return ("pass" if lo <= n <= hi else "fail", f"{n} commits (want {lo}-{hi})")

    if "based on" in a:
        r = git("merge-base", "--is-ancestor", "base", "feature")
        return ("pass" if r.returncode == 0 else "fail",
                "still descends from base" if r.returncode == 0 else "no longer based on base")

    return ("judge", "needs model/human grading")


def grade(output_path, ev):
    path = Path(output_path)
    if path.is_dir():
        results = [(a, *check_repo(path, a)) for a in ev["assertions"]]
    else:
        text = path.read_text()
        results = [(a, *check(a, text)) for a in ev["assertions"]]
    mech = [r for r in results if r[1] != "judge"]
    failed = [r for r in mech if r[1] == "fail"]
    return results, mech, failed


def find_eval(evals, key):
    for ev in evals:
        if str(ev["id"]) == str(key) or ev["name"] == key:
            return ev
    sys.exit(f"unknown eval: {key}")


def run_test_mode():
    evals = load_evals()
    errors = []
    # schema + fixtures
    ids = [e["id"] for e in evals]
    if len(set(ids)) != len(ids):
        errors.append("duplicate eval ids")
    for ev in evals:
        for field in ("name", "prompt", "assertions", "files"):
            if not ev.get(field):
                errors.append(f"eval {ev.get('id')}: missing {field}")
        for f in ev["files"]:
            if not (HERE / f).is_file():
                errors.append(f"eval {ev['id']}: missing fixture {f}")
    # every committed example must pass its eval's mechanical assertions
    by_name = {e["name"]: e for e in evals}
    examples = sorted((SKILL_DIR / "examples").rglob("*.md"))
    checked = 0
    for ex in examples:
        ev = by_name.get(ex.stem)
        if ev is None:
            errors.append(f"{ex}: no eval named '{ex.stem}'")
            continue
        _, mech, failed = grade(ex, ev)
        checked += 1
        for a, _, detail in failed:
            errors.append(f"{ex.relative_to(SKILL_DIR)}: FAIL [{a[:60]}...] {detail}")
    print(f"{len(evals)} evals, {checked} examples checked mechanically")
    if errors:
        print("\n".join(errors))
        sys.exit(1)
    print("all mechanical checks pass")


def main():
    p = argparse.ArgumentParser()
    p.add_argument("output", nargs="?", help="pr-description.md to check")
    p.add_argument("--eval", dest="eval_key", help="eval id or name")
    p.add_argument("--test", action="store_true", help="schema + examples test mode")
    args = p.parse_args()

    if args.test:
        run_test_mode()
        return
    if not (args.output and args.eval_key):
        p.error("need OUTPUT and --eval (or --test)")

    ev = find_eval(load_evals(), args.eval_key)
    results, mech, failed = grade(args.output, ev)
    for a, status, detail in results:
        print(f"[{status.upper():5}] {a}\n        {detail}")
    print(f"\nmechanical: {len(mech) - len(failed)}/{len(mech)} passed, "
          f"{len(results) - len(mech)} need judgment")
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
