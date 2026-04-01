#!/usr/bin/env python3

import argparse
import re
import subprocess
import sys
from dataclasses import dataclass
from typing import List, Optional, Tuple


SUBJECT_RE = re.compile(
    r"^(?P<type>[a-zA-Z]+)(?P<scope>\\([^)]+\\))?(?P<breaking>!)?: (?P<desc>.+)$"
)


def run(cmd: List[str]) -> str:
    return subprocess.check_output(cmd, text=True)


@dataclass
class Commit:
    sha: str
    subject: str
    body: str

    def breaking_in_body(self) -> bool:
        return "breaking change" in self.body.lower()

    def matches_conventional(self) -> bool:
        m = SUBJECT_RE.match(self.subject)
        if not m:
            return False
        # If body says breaking, subject may or may not have '!'; both are acceptable.
        return True


def get_commits(sha_range: str) -> List[Commit]:
    # Format: sha\nsubject\nbody\n---
    fmt = "%H%n%s%n%b%n---END---"
    out = run(["git", "log", sha_range, f"--pretty=format:{fmt}"])
    chunks = [c for c in out.split("---END---") if c.strip()]
    commits: List[Commit] = []
    for c in chunks:
        lines = c.splitlines()
        if not lines:
            continue
        sha = lines[0].strip()
        subject = lines[1].strip() if len(lines) > 1 else ""
        body = "\n".join(lines[2:]).strip() if len(lines) > 2 else ""
        commits.append(Commit(sha=sha, subject=subject, body=body))
    return commits


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--range", dest="sha_range", required=True, help="git range like base...head")
    ap.add_argument("--max-warnings", type=int, default=100)
    args = ap.parse_args()

    commits = get_commits(args.sha_range)
    if not commits:
        print(f"No commits found for range {args.sha_range}.")
        return 0

    violations: List[Tuple[Commit, str]] = []
    for c in commits:
        if not c.matches_conventional():
            violations.append((c, "Subject does not match Conventional Commit pattern"))

    if not violations:
        print("Conventional Commits check: OK")
        return 0

    print("Conventional Commits check: WARNINGS (merge should NOT be blocked)\n")
    for i, (c, reason) in enumerate(violations[: args.max_warnings], start=1):
        breaking_note = " (body contains breaking change)" if c.breaking_in_body() else ""
        print(f"{i}. {c.sha[:7]}: {reason}{breaking_note}\n   subject: {c.subject}\n")

    if len(violations) > args.max_warnings:
        print(f"...and {len(violations) - args.max_warnings} more.\n")

    # IMPORTANT: return 0 so this is warning-only.
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
