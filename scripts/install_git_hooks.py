#!/usr/bin/env python
"""Install the QSDM pre-commit hook into .git/hooks/.

This is the BARE-GIT install path. If you already use the
`pre-commit` framework (https://pre-commit.com/), prefer:
    pre-commit install
which reads .pre-commit-config.yaml at the repo root.

This script writes a tiny POSIX-shell shim to .git/hooks/pre-commit
that invokes scripts/git_hook_pre_commit.py via Python. The shim
is shell-based (not Python) because git's hook protocol expects an
executable file with the exact name `pre-commit` (no extension);
on Windows-with-Git-Bash the shell shim runs under MSYS bash, on
Linux/macOS under /bin/sh — both with `python` resolved through
PATH.

The shim is small and self-contained so the hook keeps working
even if scripts/git_hook_pre_commit.py is mid-refactor in a
working-tree branch. The shim itself never changes; only the
target script does.

Run:
    python scripts/install_git_hooks.py

Re-run the same command to refresh the shim (idempotent).

Uninstall:
    rm .git/hooks/pre-commit       # POSIX
    del .git\\hooks\\pre-commit    # Windows cmd
"""
from __future__ import annotations

import os
import stat
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
HOOKS_DIR = REPO_ROOT / ".git" / "hooks"
HOOK_PATH = HOOKS_DIR / "pre-commit"

# POSIX-shell shim. Resolves the repo root via `git rev-parse` so it
# keeps working when invoked from a subdirectory or via a worktree.
SHIM = """\
#!/bin/sh
# QSDM pre-commit hook — installed by scripts/install_git_hooks.py.
# This shim is intentionally minimal so the hook stays robust against
# in-flight edits to scripts/git_hook_pre_commit.py. To customise
# behaviour, edit the Python file; do not edit this shim.
set -e
REPO_ROOT="$(git rev-parse --show-toplevel)"
PYTHON="${PYTHON:-}"
if [ -z "$PYTHON" ]; then
    if command -v python >/dev/null 2>&1; then
        PYTHON=python
    elif command -v python3 >/dev/null 2>&1; then
        PYTHON=python3
    else
        echo "QSDM pre-commit hook: no python/python3 on PATH; skipping." >&2
        echo "  Install python or set PYTHON=<path>; or bypass with --no-verify." >&2
        exit 0
    fi
fi
exec "$PYTHON" "$REPO_ROOT/scripts/git_hook_pre_commit.py" "$@"
"""


def main() -> int:
    if not HOOKS_DIR.exists():
        print(
            f"error: {HOOKS_DIR} does not exist. Are you inside a git "
            "checkout? (`git init` if this is a fresh repo.)",
            file=sys.stderr,
        )
        return 1

    backup = None
    if HOOK_PATH.exists():
        existing = HOOK_PATH.read_text(encoding="utf-8", errors="replace")
        if existing == SHIM:
            print(f"OK: {HOOK_PATH} already installed and up to date.")
            return 0
        # Don't silently overwrite a foreign hook. Save it next to
        # the install for the user to merge by hand if they want to.
        backup = HOOK_PATH.with_suffix(".bak")
        HOOK_PATH.replace(backup)
        print(f"  saved existing hook to {backup}")

    HOOK_PATH.write_text(SHIM, encoding="utf-8")
    # chmod +x — git won't run a non-executable hook on POSIX. On
    # Windows the bit is ignored but doesn't hurt.
    mode = HOOK_PATH.stat().st_mode
    HOOK_PATH.chmod(mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)

    print(f"installed {HOOK_PATH}")
    if backup is not None:
        print(f"  (previous hook saved to {backup})")
    print()
    print("Test it with:")
    print("  git commit --allow-empty -m 'test pre-commit hook'")
    print("(an empty commit triggers the hook with no files to check,")
    print(" so it should exit immediately.)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
