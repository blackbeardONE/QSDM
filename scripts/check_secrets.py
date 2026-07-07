#!/usr/bin/env python3
"""Fail when publishable QSDM files contain likely credentials or key material.

The scanner intentionally uses only Python's standard library so it runs in a
fresh clone, the local pre-commit hook, and GitHub Actions. It favors
high-confidence findings over broad entropy guesses because QSDM source files
legitimately contain many hashes, public wallet addresses, and signatures.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path
import json


REPO_ROOT = Path(__file__).resolve().parent.parent
MAX_TEXT_BYTES = 10 * 1024 * 1024


@dataclass(frozen=True)
class Finding:
    path: str
    rule: str
    line: int | None = None


PATH_RULES: tuple[tuple[str, re.Pattern[str]], ...] = (
    (
        "private-key-or-token-file",
        re.compile(
            r"(?:^|/)(?:id_rsa|id_ed25519)(?:\..*)?$|"
            r"\.(?:pem|p12|pfx|jks|ppk|key|token|passphrase|keystore)$",
            re.IGNORECASE,
        ),
    ),
    (
        "wallet-keystore-file",
        re.compile(
            r"(?:^|/)(?:wallet(?:-[^/]*)?\.json|[^/]*\.keystore\.json)$",
            re.IGNORECASE,
        ),
    ),
    (
        "private-wallet-inventory",
        re.compile(r"ecosystem-wallets\.private[^/]*\.json$", re.IGNORECASE),
    ),
    (
        "signed-transaction-artifact",
        re.compile(r"\.(?:signed|unsigned)\.json$", re.IGNORECASE),
    ),
    (
        "local-environment-file",
        re.compile(r"(?:^|/)\.env(?:\..*)?$", re.IGNORECASE),
    ),
)


CONTENT_RULES: tuple[tuple[str, re.Pattern[str]], ...] = (
    (
        "private-key-block",
        re.compile(
            r"-----BEGIN (?:OPENSSH |RSA |EC |DSA |ENCRYPTED )?PRIVATE KEY-----"
        ),
    ),
    ("putty-private-key", re.compile(r"^PuTTY-User-Key-File-", re.MULTILINE)),
    ("aws-access-key", re.compile(r"\b(?:AKIA|ASIA)[0-9A-Z]{16}\b")),
    (
        "github-access-token",
        re.compile(r"\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,})\b"),
    ),
    ("google-api-key", re.compile(r"\bAIza[0-9A-Za-z_-]{35}\b")),
    ("slack-token", re.compile(r"\bxox[baprs]-[0-9A-Za-z-]{20,}\b")),
    ("stripe-live-key", re.compile(r"\b(?:sk|rk)_live_[0-9A-Za-z]{16,}\b")),
    ("openai-api-key", re.compile(r"\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}\b")),
    (
        "credential-in-url",
        re.compile(
            r"\b(?:postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis)://"
            r"[^\s/:@]+:[^\s/@]+@",
            re.IGNORECASE,
        ),
    ),
    (
        "literal-bearer-token",
        re.compile(r"\bBearer[ \t]+[A-Za-z0-9._~+/=-]{24,}\b", re.IGNORECASE),
    ),
)


SECRET_ASSIGNMENT = re.compile(
    r"^\s*(?:export\s+)?"
    r"(?P<name>[A-Z][A-Z0-9_]*(?:SECRET|TOKEN|PASSWORD|PASSPHRASE|PRIVATE_KEY|API_KEY)[A-Z0-9_]*)"
    r"\s*=\s*(?P<value>[^#\r\n]+?)\s*$"
)

STRUCTURED_SECRET = re.compile(
    r"[\"']?(?P<name>password|passphrase|client_secret|private_key|api_key|bearer_token)"
    r"[\"']?\s*[:=]\s*[\"'](?P<value>[^\"']{8,})[\"']",
    re.IGNORECASE,
)

SAFE_VALUE_MARKERS = (
    "${",
    "{{",
    "process.env",
    "os.getenv",
    "getenv(",
    "example",
    "placeholder",
    "changeme",
    "change-me",
    "replace_me",
    "replace_with",
    "replace-with",
    "redacted",
    "dummy",
    "fake",
    "irrelevant",
    "test-only",
    "not-configured",
    "not_set",
    "your-",
    "your_",
    "<",
)

CONFIG_LIKE_SUFFIXES = {
    ".conf",
    ".env",
    ".ini",
    ".md",
    ".properties",
    ".ps1",
    ".service",
    ".sh",
    ".toml",
    ".yaml",
    ".yml",
}


def run_git(args: list[str]) -> bytes:
    proc = subprocess.run(
        ["git", *args],
        cwd=REPO_ROOT,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if proc.returncode != 0:
        message = proc.stderr.decode("utf-8", errors="replace").strip()
        raise RuntimeError(message or f"git {' '.join(args)} failed")
    return proc.stdout


def decode_paths(raw: bytes) -> list[str]:
    return [
        part.decode("utf-8", errors="surrogateescape").replace("\\", "/")
        for part in raw.split(b"\0")
        if part
    ]


def selected_paths(mode: str) -> list[str]:
    if mode == "staged":
        raw = run_git(
            ["diff", "--cached", "--name-only", "--diff-filter=ACMR", "-z"]
        )
    elif mode == "all-tracked":
        raw = run_git(["ls-files", "-z"])
    else:
        raw = run_git(["ls-files", "-co", "--exclude-standard", "-z"])
    return sorted(set(decode_paths(raw)))


def revision_findings(revision_range: str) -> tuple[list[Finding], int]:
    commits = run_git(["rev-list", "--reverse", revision_range]).decode(
        "ascii", errors="strict"
    ).splitlines()
    findings: list[Finding] = []
    blobs_scanned = 0

    for commit in commits:
        changed = decode_paths(
            run_git(
                [
                    "diff-tree",
                    "--root",
                    "--no-commit-id",
                    "--name-only",
                    "--diff-filter=ACMR",
                    "-r",
                    "-z",
                    commit,
                ]
            )
        )
        for path in changed:
            prefix = f"{commit[:12]}:{path}"
            forbidden = path_finding(path)
            if forbidden:
                findings.append(Finding(prefix, forbidden.rule, forbidden.line))
                continue
            data = run_git(["show", f"{commit}:{path}"])
            blobs_scanned += 1
            for finding in content_findings(path, data):
                findings.append(Finding(prefix, finding.rule, finding.line))

    return findings, blobs_scanned


def staged_bytes(path: str) -> bytes:
    return run_git(["show", f":{path}"])


def worktree_bytes(path: str) -> bytes:
    return (REPO_ROOT / path).read_bytes()


def path_finding(path: str) -> Finding | None:
    normalized = path.replace("\\", "/")
    lower = normalized.lower()
    if lower.endswith(".env.example") or lower.endswith(".env.sample"):
        return None
    for rule, pattern in PATH_RULES:
        if pattern.search(normalized):
            return Finding(normalized, rule)
    return None


def safe_literal(path: str, value: str) -> bool:
    raw = value.strip()
    if raw.startswith(('""', "''")):
        return True
    normalized = raw.strip("\"'").strip().lower()
    if len(normalized) < 8:
        return True
    if normalized in {"authorization", "password"}:
        return True
    if normalized.startswith(
        ("test-", "too-short", "wrongpass", "correct horse battery staple")
    ):
        return True
    if normalized.startswith(("$", "/", "./", "../", "re.")):
        return True
    path_lower = path.lower()
    if "charming123" in normalized and (
        "test" in Path(path_lower).name
        or path_lower == "changelog.md"
        or path_lower.endswith("/pkg/audit/checklist.go")
    ):
        # Historical regression sentinel. Production strict-mode explicitly
        # rejects this value; tests preserve it to prevent reintroduction.
        return True
    return any(marker in normalized for marker in SAFE_VALUE_MARKERS)


def content_findings(path: str, data: bytes) -> list[Finding]:
    if len(data) > MAX_TEXT_BYTES or b"\0" in data[:8192]:
        return []
    text = data.decode("utf-8", errors="replace")
    findings: list[Finding] = []

    for rule, pattern in CONTENT_RULES:
        for match in pattern.finditer(text):
            line = text.count("\n", 0, match.start()) + 1
            findings.append(Finding(path, rule, line))

    suffix = Path(path).suffix.lower()
    for line_number, line in enumerate(text.splitlines(), start=1):
        assignment = SECRET_ASSIGNMENT.match(line) if suffix in CONFIG_LIKE_SUFFIXES else None
        if assignment:
            name = assignment.group("name")
            value = assignment.group("value")
            if not name.endswith(("_FILE", "_PATH", "_URL", "_HEADER")) and not safe_literal(path, value):
                findings.append(Finding(path, "literal-secret-assignment", line_number))
        for match in STRUCTURED_SECRET.finditer(line):
            if not safe_literal(path, match.group("value")):
                findings.append(Finding(path, "literal-structured-secret", line_number))

    if suffix == ".json":
        try:
            parsed = json.loads(text)
        except json.JSONDecodeError:
            parsed = None
        if isinstance(parsed, dict):
            keys = {str(key).lower() for key in parsed}
            if {"ciphertext", "kdf"}.issubset(keys) and (
                "public_key" in keys or "address" in keys
            ):
                findings.append(Finding(path, "encrypted-wallet-keystore"))

    return findings


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    group = parser.add_mutually_exclusive_group()
    group.add_argument("--staged", action="store_true", help="scan staged files")
    group.add_argument(
        "--all-tracked", action="store_true", help="scan every tracked file"
    )
    group.add_argument(
        "--worktree",
        action="store_true",
        help="scan tracked and untracked files not excluded by .gitignore",
    )
    group.add_argument(
        "--commit-range",
        metavar="REV_RANGE",
        help="scan every added or modified blob in a Git revision range",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if args.commit_range:
        findings, checked_count = revision_findings(args.commit_range)
        mode = f"commit-range {args.commit_range}"
        paths: list[str] = []
    else:
        mode = (
            "all-tracked"
            if args.all_tracked
            else "worktree"
            if args.worktree
            else "staged"
        )
        paths = selected_paths(mode)
        findings = []
        checked_count = len(paths)

    for path in paths:
        forbidden = path_finding(path)
        if forbidden:
            findings.append(forbidden)
            continue
        try:
            data = staged_bytes(path) if mode == "staged" else worktree_bytes(path)
        except FileNotFoundError:
            # A tracked file deleted in the working tree remains in
            # `git ls-files` until the deletion is staged. It cannot leak in
            # the candidate tree, so skip it.
            continue
        except (OSError, RuntimeError) as exc:
            print(f"secret scan could not read {path}: {exc}", file=sys.stderr)
            return 2
        findings.extend(content_findings(path, data))

    unique = sorted(set(findings), key=lambda item: (item.path, item.line or 0, item.rule))
    if unique:
        print("Secret scan blocked this publication:", file=sys.stderr)
        for finding in unique:
            location = f":{finding.line}" if finding.line is not None else ""
            print(f"  {finding.path}{location} [{finding.rule}]", file=sys.stderr)
        print(
            "Move custody material outside the repository or replace literals "
            "with environment/secret-store references.",
            file=sys.stderr,
        )
        return 1

    print(f"Secret scan passed: {checked_count} {mode} blob(s) checked.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
