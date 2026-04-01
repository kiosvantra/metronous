#!/usr/bin/env python3

import argparse
import re
import subprocess
from dataclasses import dataclass
from typing import List, Optional, Tuple


SEMVER_RE = re.compile(r"^v(\d+)\.(\d+)\.(\d+)$")


def run(cmd: List[str]) -> str:
    return subprocess.check_output(cmd, text=True).strip()


def get_latest_tag(tag_glob: str = "v[0-9]*.[0-9]*.[0-9]*") -> Optional[str]:
    # git sorts version-like refs with v:refname
    try:
        out = run(["git", "tag", "--list", tag_glob, "--sort=-v:refname"])  # one per line
        tags = [t.strip() for t in out.splitlines() if t.strip()]
        return tags[0] if tags else None
    except subprocess.CalledProcessError:
        return None


def parse_semver(tag: str) -> Tuple[int, int, int]:
    m = SEMVER_RE.match(tag)
    if not m:
        raise ValueError(f"Invalid semver tag: {tag}")
    return int(m.group(1)), int(m.group(2)), int(m.group(3))


def bump_version(major: int, minor: int, patch: int, bump: str) -> str:
    if bump == "major":
        major += 1
        minor = 0
        patch = 0
    elif bump == "minor":
        minor += 1
        patch = 0
    elif bump == "patch":
        patch += 1
    else:
        raise ValueError(f"Unknown bump type: {bump}")
    return f"v{major}.{minor}.{patch}"


@dataclass
class Commit:
    subject: str
    body: str

    @property
    def lower_subject(self) -> str:
        return self.subject.lower()

    def is_breaking(self) -> bool:
        # Conventional Commits breaking patterns
        if "breaking change" in self.body.lower():
            return True
        # Subject: feat!: type! patterns
        return bool(re.search(r"\w+!?\(?.*\)?!:$", self.subject)) or "!" in self.subject.split(":", 1)[0]

    def type(self) -> str:
        # Subject formats: type(scope): desc OR type!: desc
        m = re.match(r"^([a-zA-Z]+)(\([^)]+\))?!?:", self.subject)
        return (m.group(1).lower() if m else "other")


def get_commits_since(latest_tag: Optional[str]) -> List[Commit]:
    if latest_tag:
        rev_range = f"{latest_tag}..HEAD"
    else:
        # No prior tags: include all history
        rev_range = "HEAD"

    # Use a delimiter to reconstruct commits.
    # Format: <subject>\n---BODY---\n<body>
    fmt = "%s%n%n%b"
    try:
        out = run(["git", "log", rev_range, f"--pretty=format:{fmt}"])
    except subprocess.CalledProcessError:
        out = ""

    commits: List[Commit] = []
    # Each commit is separated by a blank line; because format uses %s then blank line then %b,
    # we split on double-newlines between commits.
    blocks = [b for b in re.split(r"\n{2,}", out.strip()) if b.strip()]
    for b in blocks:
        lines = b.splitlines()
        if not lines:
            continue
        subject = lines[0].strip()
        body = "\n".join(lines[1:]).strip() if len(lines) > 1 else ""
        if subject:
            commits.append(Commit(subject=subject, body=body))
    return commits


def infer_bump(commits: List[Commit]) -> str:
    if any(c.is_breaking() for c in commits):
        return "major"

    # minor: feat
    if any(c.type() == "feat" for c in commits):
        return "minor"

    # patch: fix (or anything else)
    if any(c.type() in ("fix", "perf") for c in commits):
        return "patch"

    return "patch"


def categorize_release_notes(commits: List[Commit]) -> dict:
    sections = {
        "Added": [],
        "Fixed": [],
        "Changed": [],
        "Breaking": [],
    }

    for c in commits:
        label = c.type()
        if c.is_breaking():
            sections["Breaking"].append(f"- {c.subject}")
        elif label == "feat":
            sections["Added"].append(f"- {c.subject}")
        elif label == "fix":
            sections["Fixed"].append(f"- {c.subject}")
        else:
            sections["Changed"].append(f"- {c.subject}")

    return sections


def render_notes(sections: dict, previous: Optional[str], new: str) -> str:
    lines = []
    lines.append(f"## Release {new}")
    if previous:
        lines.append(f"Previous: {previous}")
    lines.append("")

    order = ["Added", "Fixed", "Changed", "Breaking"]
    for k in order:
        items = sections.get(k, [])
        if not items:
            continue
        lines.append(f"### {k}")
        lines.extend(items)
        lines.append("")

    return "\n".join(lines).strip() + "\n"


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--tag-glob", default="v[0-9]*.[0-9]*.[0-9]*")
    ap.add_argument("--initial-version", default="v0.1.0")
    ap.add_argument("--out-version", default="version.txt")
    ap.add_argument("--out-notes", default="release_notes.md")
    args = ap.parse_args()

    latest = get_latest_tag(args.tag_glob)
    commits = get_commits_since(latest)

    if not commits:
        bump = "patch"
    else:
        bump = infer_bump(commits)

    if latest:
        major, minor, patch = parse_semver(latest)
    else:
        m = SEMVER_RE.match(args.initial_version)
        if not m:
            raise ValueError("--initial-version must be in vX.Y.Z format")
        major, minor, patch = int(m.group(1)), int(m.group(2)), int(m.group(3))

    new_tag = bump_version(major, minor, patch, bump)
    sections = categorize_release_notes(commits)
    notes = render_notes(sections, latest, new_tag)

    with open(args.out_version, "w", encoding="utf-8") as f:
        f.write(new_tag + "\n")
    with open(args.out_notes, "w", encoding="utf-8") as f:
        f.write(notes)

    print(new_tag)


if __name__ == "__main__":
    main()
