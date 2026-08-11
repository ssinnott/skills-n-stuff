#!/usr/bin/env python3
"""Eval runner for the pr-description skill.

Generates outputs with the `claude` CLI (claude -p), then grades every
mechanical assertion with checks.py. Judgment assertions are listed for a
model or human grader — see README.md for the full grading loop.

Usage:
  python3 run.py                          # all evals, with skill
  python3 run.py --evals 1,4 --baseline   # subset, plus no-skill baseline runs
  python3 run.py --model claude-haiku-4-5-20251001
  python3 run.py --grade-only             # re-grade existing outputs, no generation

Outputs land in ../../pr-description-workspace/runner/<eval-name>/<config>/
(pr-description.md per run). The workspace is gitignored.
"""

import argparse
import subprocess
import sys
from pathlib import Path

import checks

HERE = Path(__file__).resolve().parent
SKILL_DIR = HERE.parent
WORKSPACE = SKILL_DIR.parent / "pr-description-workspace" / "runner"


def build_prompt(ev, with_skill):
    fixture = (HERE / ev["files"][0]).read_text()
    parts = []
    if with_skill:
        parts.append("Follow this skill exactly when writing:\n\n"
                     + (SKILL_DIR / "SKILL.md").read_text())
        parts.append("The template the skill references:\n\n"
                     + (SKILL_DIR / "assets" / "template.md").read_text())
    parts.append(f"Task: {ev['prompt']}")
    parts.append("The full diff referenced above:\n\n```diff\n" + fixture + "\n```")
    parts.append("Reply with ONLY the finished PR description markdown "
                 "(title and body) — no preamble, no commentary.")
    return "\n\n---\n\n".join(parts)


def generate(ev, with_skill, model):
    cmd = ["claude", "-p", build_prompt(ev, with_skill)]
    if model:
        cmd += ["--model", model]
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=600)
    if result.returncode != 0:
        sys.exit(f"claude -p failed for eval {ev['id']}: {result.stderr[:500]}")
    out_dir = WORKSPACE / f"eval-{ev['id']}-{ev['name']}" / (
        "with_skill" if with_skill else "without_skill")
    out_dir.mkdir(parents=True, exist_ok=True)
    out = out_dir / "pr-description.md"
    out.write_text(result.stdout.strip() + "\n")
    return out


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--evals", help="comma-separated ids or names (default: all)")
    p.add_argument("--baseline", action="store_true",
                   help="also run each eval without the skill")
    p.add_argument("--model", help="model id passed to claude --model")
    p.add_argument("--grade-only", action="store_true",
                   help="skip generation; grade existing runner outputs")
    args = p.parse_args()

    evals = checks.load_evals()
    if args.evals:
        keys = args.evals.split(",")
        evals = [e for e in evals if str(e["id"]) in keys or e["name"] in keys]

    total_mech = total_pass = 0
    for ev in evals:
        configs = ["with_skill"] + (["without_skill"] if args.baseline else [])
        for config in configs:
            out = (WORKSPACE / f"eval-{ev['id']}-{ev['name']}" / config /
                   "pr-description.md")
            if not args.grade_only:
                print(f"generating eval-{ev['id']} {config}...", flush=True)
                out = generate(ev, config == "with_skill", args.model)
            if not out.is_file():
                print(f"  (no output at {out}, skipping)")
                continue
            results, mech, failed = checks.grade(out, ev)
            judge = len(results) - len(mech)
            total_mech += len(mech)
            total_pass += len(mech) - len(failed)
            print(f"eval-{ev['id']} {config}: "
                  f"{len(mech) - len(failed)}/{len(mech)} mechanical, "
                  f"{judge} need judgment")
            for a, _, detail in failed:
                print(f"  FAIL [{a[:70]}] {detail}")

    print(f"\nmechanical total: {total_pass}/{total_mech}")
    sys.exit(0 if total_pass == total_mech else 1)


if __name__ == "__main__":
    main()
