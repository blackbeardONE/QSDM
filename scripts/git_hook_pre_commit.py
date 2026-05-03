#!/usr/bin/env python
"""Pre-commit hook for QSDM.

Runs the runbook coverage lint and promtool's two-layer rule check
(`promtool check rules` + `promtool test rules`) before every commit,
but ONLY for the checks whose inputs are part of the staged
changeset. A commit that doesn't touch alerts, runbooks, or the
prometheus deploy folder runs no slow checks at all.

Mirrors the GitHub Actions job in
.github/workflows/validate-deploy.yml so failures surface locally
instead of 30 seconds after `git push`.

Install (one-time, per clone):
  - With the pre-commit framework:
        pre-commit install
  - Without (bare git hook shim):
        python scripts/install_git_hooks.py

Bypass for a single commit:
  - `git commit --no-verify` — standard git escape hatch.

Behaviour
---------
1. Detect staged files via
   `git diff --cached --name-only --diff-filter=ACMR`.
2. Decide which checks need to run based on which staged files
   match each check's path filter (alerts file, runbook tree,
   the lint script itself, etc.).
3. Run each enabled check sequentially, prefixing output with a
   short status banner so the operator sees what was run.
4. Exit non-zero on the FIRST failure, after printing the entire
   failure output (don't truncate — operators need the full
   context to fix).

Skip semantics
--------------
- `promtool` not on PATH (and `$PROMTOOL` env var unset): both
  promtool checks are SKIPPED with a clear amber-warning banner.
  The runbook coverage lint still runs (Python-only).
- The hook never auto-installs promtool; that's a deliberate
  scope decision so the hook stays fast and deterministic.

CI parity
---------
The hook runs the SAME commands the CI workflow runs, in the
same order. A clean local run is the strongest possible signal
that the CI gate will pass.

Version pinning
---------------
The CI workflow installs an exact promtool version. When the
hook will invoke promtool, it also probes
`promtool --version` and compares the result to the pin parsed
from `.github/workflows/validate-deploy.yml`. A mismatch prints
a soft amber warning (never a failure) so operators know that
local-vs-CI parity is approximate when versions diverge — set
`$PROMTOOL` to a binary at the pinned version for exact parity.
"""
from __future__ import annotations

import os
import re
import shutil
import subprocess
import sys
import time
from pathlib import Path

# ANSI colour codes — wrapped in a tiny helper so output stays
# readable when piped through a non-tty (e.g. `git commit | tee`).
_USE_COLOUR = sys.stdout.isatty() and os.environ.get("NO_COLOR") is None


def _c(code: str, s: str) -> str:
    if not _USE_COLOUR:
        return s
    return f"\033[{code}m{s}\033[0m"


def _ok(s: str) -> str:
    return _c("32", s)


def _warn(s: str) -> str:
    return _c("33", s)


def _err(s: str) -> str:
    return _c("31", s)


def _bold(s: str) -> str:
    return _c("1", s)


# -----------------------------------------------------------------
# Paths — keep in sync with .github/workflows/validate-deploy.yml
# -----------------------------------------------------------------

REPO_ROOT = Path(__file__).resolve().parent.parent

ALERTS_FILE = "QSDM/deploy/prometheus/alerts_qsdm.example.yml"
TEST_FILE = "QSDM/deploy/prometheus/alerts_qsdm.test.yml"
SPEC_FILE = "QSDM/deploy/prometheus/alerts_qsdm.test.spec.yml"
RUNBOOKS_DIR = "QSDM/docs/docs/runbooks/"
LINT_SCRIPT = "scripts/check_runbook_coverage.py"
GEN_SCRIPT = "scripts/gen_promtool_tests.py"
WORKFLOW_FILE = ".github/workflows/validate-deploy.yml"
HOOK_FILE = "scripts/git_hook_pre_commit.py"

# Paths whose modification triggers the runbook-coverage lint.
RUNBOOK_LINT_TRIGGERS = (
    ALERTS_FILE,
    RUNBOOKS_DIR,
    LINT_SCRIPT,
    HOOK_FILE,
)

# Paths whose modification triggers `promtool check rules`.
PROMTOOL_CHECK_TRIGGERS = (ALERTS_FILE,)

# Paths whose modification triggers `promtool test rules`. The
# spec file is included because editing it (without regenerating
# alerts_qsdm.test.yml) creates a checked-in inconsistency the
# generator's coverage validator catches; we want that signal
# locally before the operator pushes.
PROMTOOL_TEST_TRIGGERS = (
    ALERTS_FILE,
    TEST_FILE,
    SPEC_FILE,
    GEN_SCRIPT,
)


# -----------------------------------------------------------------
# Helpers
# -----------------------------------------------------------------


def staged_files() -> list[str]:
    """Return paths (forward-slash, repo-relative) of staged files.

    `--diff-filter=ACMR` keeps Added, Copied, Modified, Renamed —
    excludes Deleted (we don't want to lint a file that's being
    removed) and Unmerged (already error-state).
    """
    proc = subprocess.run(
        ["git", "diff", "--cached", "--name-only", "--diff-filter=ACMR"],
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        cwd=REPO_ROOT,
    )
    if proc.returncode != 0:
        # Not a git repo, or git not on PATH. Nothing to do.
        return []
    return [
        line.strip().replace("\\", "/")
        for line in proc.stdout.splitlines()
        if line.strip()
    ]


def any_match(files: list[str], patterns: tuple[str, ...]) -> list[str]:
    """Filter `files` to those that match (prefix or equal) any pattern.

    Patterns ending with `/` match directory prefixes; others match
    exact paths.
    """
    out: list[str] = []
    for f in files:
        for p in patterns:
            if p.endswith("/"):
                if f.startswith(p):
                    out.append(f)
                    break
            elif f == p:
                out.append(f)
                break
    return out


def find_promtool() -> str | None:
    """Locate the `promtool` binary.

    Checks (in order):
      1. `$PROMTOOL` environment variable (explicit override).
      2. `promtool` on PATH (Linux/macOS).
      3. `promtool.exe` on PATH (Windows-native).

    Returns the resolved path, or None if not found.
    """
    env = os.environ.get("PROMTOOL")
    if env and Path(env).exists():
        return env
    for cand in ("promtool", "promtool.exe"):
        found = shutil.which(cand)
        if found:
            return found
    return None


# -----------------------------------------------------------------
# promtool version pinning
# -----------------------------------------------------------------
#
# The CI workflow installs an exact promtool version; locally the
# operator might have a different one. Most rule semantics are
# stable across recent promtool releases, but `test rules`
# behaviour HAS shifted in subtle ways between minors (e.g. how
# `exp_annotations` is matched changed between 2.x and 3.x), so
# "local green" can disagree with "CI green" if the versions
# diverge meaningfully.
#
# We do NOT make a version mismatch fatal — that would be
# annoying for operators who are deliberately on a newer promtool
# (e.g. testing forward compatibility) or who installed the
# distro's package version. Instead the hook prints a single-
# line amber banner so the operator knows local-vs-CI parity
# might not be exact.
#
# Source-of-truth: the CI workflow file is the canonical pin. The
# hook parses it once per invocation. If the workflow file is
# missing or unparseable (e.g. someone refactored the install
# step), the version probe silently skips — never fail the hook
# for missing CI metadata.

# Match `VERSION="2.55.1"` (and tolerant variants) inside the
# Install promtool step. Specific enough not to false-positive on
# unrelated VERSION assignments elsewhere in the workflow file.
_WORKFLOW_VERSION_RE = re.compile(
    r"""
    ^[ \t]*VERSION[ \t]*=[ \t]*
    ["']?(?P<version>\d+\.\d+\.\d+)["']?
    [ \t]*$
    """,
    re.MULTILINE | re.VERBOSE,
)


def pinned_promtool_version() -> str | None:
    """Read the canonical promtool version pin from the CI workflow.

    Returns the version string (e.g. "2.55.1") or None if the
    workflow file is missing / the pin marker is absent.

    The hook treats a None return as "no pin to compare against",
    not a failure. This keeps the hook resilient to future
    refactors of the workflow file.
    """
    wf = REPO_ROOT / WORKFLOW_FILE
    if not wf.exists():
        return None
    try:
        text = wf.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return None
    # We only consider VERSION assignments inside the
    # `prometheus-rules-check:` job; that's a soft scope (we just
    # check the assignment is somewhere AFTER the job name in the
    # file) so the regex doesn't pull a kubeconform VERSION pin
    # by accident.
    job_marker = "prometheus-rules-check:"
    job_pos = text.find(job_marker)
    scope = text[job_pos:] if job_pos >= 0 else text
    m = _WORKFLOW_VERSION_RE.search(scope)
    if not m:
        return None
    return m.group("version")


# Match `promtool, version 2.55.1 (...)` on the first line.
_LOCAL_VERSION_RE = re.compile(
    r"^promtool,?\s+version\s+(?P<version>\d+\.\d+\.\d+)",
    re.MULTILINE,
)


def local_promtool_version(promtool: str) -> str | None:
    """Probe `promtool --version` and parse its output.

    Returns the version string or None on probe failure (binary
    refused to run, output format unrecognised, etc.). Failures
    are NOT reported as errors; they just disable the version
    check.
    """
    try:
        proc = subprocess.run(
            [promtool, "--version"],
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=5,
        )
    except (OSError, subprocess.TimeoutExpired):
        return None
    if proc.returncode != 0:
        return None
    blob = (proc.stdout or "") + (proc.stderr or "")
    m = _LOCAL_VERSION_RE.search(blob)
    if not m:
        return None
    return m.group("version")


# -----------------------------------------------------------------
# Check runners
# -----------------------------------------------------------------


class CheckResult:
    OK = "ok"
    SKIPPED = "skipped"
    FAILED = "failed"

    def __init__(self, status: str, elapsed_ms: int, message: str = "") -> None:
        self.status = status
        self.elapsed_ms = elapsed_ms
        self.message = message


def run_subprocess(
    cmd: list[str], description: str
) -> CheckResult:
    """Run a check command, stream its output, and return a CheckResult."""
    print()
    print(_bold(f"==> {description}"))
    print(f"    $ {' '.join(cmd)}")
    t0 = time.monotonic()
    proc = subprocess.run(
        cmd,
        cwd=REPO_ROOT,
        # Don't capture: stream to the operator's terminal so they
        # see promtool / the lint produce output in real time. The
        # check is going to be slow enough that buffering would feel
        # wrong.
    )
    elapsed_ms = int((time.monotonic() - t0) * 1000)
    if proc.returncode == 0:
        print(_ok(f"    OK  ({elapsed_ms} ms)"))
        return CheckResult(CheckResult.OK, elapsed_ms)
    print(_err(f"    FAIL  (exit {proc.returncode}, {elapsed_ms} ms)"))
    return CheckResult(
        CheckResult.FAILED,
        elapsed_ms,
        f"{description}: exit {proc.returncode}",
    )


def check_runbook_coverage() -> CheckResult:
    return run_subprocess(
        [sys.executable, LINT_SCRIPT],
        "runbook coverage lint (alerts ↔ runbooks)",
    )


def check_promtool_check_rules(promtool: str) -> CheckResult:
    return run_subprocess(
        [promtool, "check", "rules", ALERTS_FILE],
        "promtool check rules (PromQL syntax)",
    )


def check_promtool_test_rules(promtool: str) -> CheckResult:
    return run_subprocess(
        [promtool, "test", "rules", TEST_FILE],
        "promtool test rules (synthetic-time-series suite)",
    )


# -----------------------------------------------------------------
# Main
# -----------------------------------------------------------------


def main() -> int:
    files = staged_files()

    if not files:
        # Empty commit, or no staged changes (somehow). Don't
        # interfere — let `git commit` produce its own error.
        return 0

    runbook_hits = any_match(files, RUNBOOK_LINT_TRIGGERS)
    check_hits = any_match(files, PROMTOOL_CHECK_TRIGGERS)
    test_hits = any_match(files, PROMTOOL_TEST_TRIGGERS)

    runs = []
    if runbook_hits:
        runs.append(("runbook_coverage", runbook_hits))
    if check_hits:
        runs.append(("promtool_check", check_hits))
    if test_hits:
        runs.append(("promtool_test", test_hits))

    if not runs:
        # No relevant files staged — fast-exit, don't print noise.
        return 0

    # ------------- Header -------------
    print()
    print(_bold("─" * 67))
    print(_bold("QSDM pre-commit hook — alerts + runbook contract checks"))
    print(_bold("─" * 67))
    print(f"Staged files matching trigger paths: {len(files)} total")
    for run_name, hits in runs:
        sample = hits[:3]
        more = f"  +{len(hits) - 3} more" if len(hits) > 3 else ""
        print(f"  - {run_name:20s}  -> {len(hits)} file(s)")
        for h in sample:
            print(f"       {h}")
        if more:
            print(f"     {more}")

    # ------------- Locate promtool + check version pin -------------
    promtool = None
    will_run_promtool = any(
        name in ("promtool_check", "promtool_test") for name, _ in runs
    )
    if will_run_promtool:
        promtool = find_promtool()
        pinned = pinned_promtool_version()
        if promtool is None:
            print()
            install_url = (
                "https://github.com/prometheus/prometheus/releases/"
                f"download/v{pinned}/prometheus-{pinned}.<os>-amd64.tar.gz"
                if pinned
                else "https://github.com/prometheus/prometheus/releases"
            )
            pinned_label = pinned or "the version pinned in CI"
            print(_warn("⚠  promtool not found on PATH (and $PROMTOOL unset)."))
            print(_warn("   The two promtool checks will be SKIPPED locally."))
            print(_warn(f"   They still run in CI — the workflow installs"))
            print(_warn(f"   prometheus {pinned_label} itself."))
            print(_warn("   To enable locally, install promtool:"))
            print(_warn(f"     curl -fsSL {install_url}"))
        else:
            # Compare local vs pinned. Mismatches print a single
            # amber line; matches stay silent (no extra noise on
            # the happy path).
            local = local_promtool_version(promtool)
            if pinned and local and local != pinned:
                print()
                print(
                    _warn(
                        f"⚠  promtool version mismatch: local={local}, "
                        f"CI-pinned={pinned}."
                    )
                )
                print(
                    _warn(
                        "   `promtool test rules` semantics have shifted "
                        "between minors;"
                    )
                )
                print(
                    _warn(
                        "   local PASS does not strictly imply CI PASS. "
                        "Pin local to match"
                    )
                )
                print(
                    _warn(
                        f"   CI by installing prometheus {pinned}, or "
                        "set $PROMTOOL to the pinned"
                    )
                )
                print(_warn("   binary."))
            elif pinned and local is None:
                # Probe failed — note but don't dwell on it.
                print()
                print(
                    _warn(
                        f"⚠  could not probe promtool --version; "
                        f"CI is pinned to {pinned}."
                    )
                )

    # ------------- Run checks -------------
    results: list[tuple[str, CheckResult]] = []
    for name, _hits in runs:
        if name == "runbook_coverage":
            results.append((name, check_runbook_coverage()))
        elif name == "promtool_check":
            if promtool is None:
                results.append(
                    (name, CheckResult(CheckResult.SKIPPED, 0, "promtool missing"))
                )
            else:
                results.append((name, check_promtool_check_rules(promtool)))
        elif name == "promtool_test":
            if promtool is None:
                results.append(
                    (name, CheckResult(CheckResult.SKIPPED, 0, "promtool missing"))
                )
            else:
                results.append((name, check_promtool_test_rules(promtool)))

        # Short-circuit on first failure: subsequent checks rarely
        # add useful signal once the first one fails, and operators
        # care about getting a fast clear answer.
        if results[-1][1].status == CheckResult.FAILED:
            break

    # ------------- Summary -------------
    print()
    print(_bold("─" * 67))
    print(_bold("Summary"))
    print(_bold("─" * 67))
    failed = 0
    for name, res in results:
        marker = {
            CheckResult.OK: _ok("PASS   "),
            CheckResult.SKIPPED: _warn("SKIPPED"),
            CheckResult.FAILED: _err("FAILED "),
        }[res.status]
        elapsed = f"{res.elapsed_ms} ms" if res.status != CheckResult.SKIPPED else "-"
        print(f"  {marker}  {name:25s}  {elapsed:>8s}  {res.message}")
        if res.status == CheckResult.FAILED:
            failed += 1

    if failed:
        print()
        print(_err("✗ Pre-commit checks failed. Fix the issue above and"))
        print(_err("  retry, or bypass with `git commit --no-verify`."))
        return 1

    print()
    print(_ok("✓ All pre-commit checks passed."))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
