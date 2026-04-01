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
    try:
        out = run(["git", "tag", "--list", tag_glob, "--sort=-v:refname"])
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

    def is_breaking(self) -> bool:
        if "breaking change" in self.body.lower():
            return True
        return bool(re.search(r"\w+!?\(?.*\)?!:$", self.subject)) or "!" in self.subject.split(":", 1)[0]

    def type(self) -> str:
        m = re.match(r"^([a-zA-Z]+)(\([^)]+\))?!?:", self.subject)
        return (m.group(1).lower() if m else "other")


def get_commits_since(latest_tag: Optional[str]) -> List[Commit]:
    rev_range = f"{latest_tag}..HEAD" if latest_tag else "HEAD"
    fmt = "%s%n%n%b"
    try:
        out = run(["git", "log", rev_range, f"--pretty=format:{fmt}"])
    except subprocess.CalledProcessError:
        out = ""

    commits: List[Commit] = []
    blocks = [b for b in re.split(r"\n{2,}", out.strip()) if b.strip()]
    for b in blocks:
        lines = b.splitlines()
        subject = lines[0].strip() if lines else ""
        body = "\n".join(lines[1:]).strip() if len(lines) > 1 else ""
        if subject:
            commits.append(Commit(subject=subject, body=body))
    return commits


def infer_bump(commits: List[Commit]) -> str:
    if any(c.is_breaking() for c in commits):
        return "major"
    if any(c.type() == "feat" for c in commits):
        return "minor"
    return "patch"


def categorize_release_notes(commits: List[Commit]) -> dict:
    sections = {"Added": [], "Fixed": [], "Changed": [], "Breaking": []}
    for c in commits:
        if c.is_breaking():
            sections["Breaking"].append(f"- {c.subject}")
        else:
            t = c.type()
            if t == "feat":
                sections["Added"].append(f"- {c.subject}")
            elif t == "fix":
                sections["Fixed"].append(f"- {c.subject}")
            else:
                sections["Changed"].append(f"- {c.subject}")
    return sections


def render_notes(sections: dict, previous: Optional[str], new: str) -> str:
    lines: List[str] = []
    lines.append(f"## Release {new}")
    if previous:
        lines.append(f"Previous: {previous}")
    lines.append("")

    for k in ["Added", "Fixed", "Changed", "Breaking"]:
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

    if latest:
        major, minor, patch = parse_semver(latest)
    else:
        m = SEMVER_RE.match(args.initial_version)
        if not m:
            raise ValueError("--initial-version must be in vX.Y.Z format")
        major, minor, patch = int(m.group(1)), int(m.group(2)), int(m.group(3))

    bump = infer_bump(commits) if commits else "patch"
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
