#!/usr/bin/env python3
"""Reject mutable third-party GitHub Action references in workflow files."""

from __future__ import annotations

import re
import sys
from dataclasses import dataclass
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parent.parent
WORKFLOW_ROOT = REPO_ROOT / ".github" / "workflows"
USES_PATTERN = re.compile(
    r"^\s*(?:-\s*)?uses:\s*(?P<target>[^\s#]+)", re.MULTILINE
)
PINNED_ACTION = re.compile(r"^[^@\s]+@[0-9a-fA-F]{40}$")


@dataclass(frozen=True)
class Finding:
    path: str
    line: int
    target: str


def workflow_files(root: Path) -> list[Path]:
    return sorted((*root.rglob("*.yml"), *root.rglob("*.yaml")))


def find_unpinned_actions(root: Path = WORKFLOW_ROOT) -> list[Finding]:
    findings: list[Finding] = []
    if not root.exists():
        return findings

    for path in workflow_files(root):
        text = path.read_text(encoding="utf-8")
        for match in USES_PATTERN.finditer(text):
            target = match.group("target")
            if target.startswith(("./", "docker://")):
                continue
            if PINNED_ACTION.fullmatch(target):
                continue
            line = text.count("\n", 0, match.start()) + 1
            findings.append(
                Finding(path.relative_to(root.parent.parent).as_posix(), line, target)
            )
    return findings


def main() -> int:
    findings = find_unpinned_actions()
    if findings:
        print("GitHub workflow action pin check failed:", file=sys.stderr)
        for finding in findings:
            print(
                f"  {finding.path}:{finding.line} uses mutable ref {finding.target}",
                file=sys.stderr,
            )
        print(
            "Pin every external action to its verified 40-character commit SHA.",
            file=sys.stderr,
        )
        return 1

    print("GitHub workflow action pin check passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
