#!/usr/bin/env python3
"""
Subresource Integrity (SRI) lint for the QSDM landing site.

Every <script>/<link> in QSDM/deploy/landing that carries an
`integrity="sha###-..."` attribute AND resolves to a file inside the
landing tree must declare the hash that file actually has.

Why this exists
---------------
A stale SRI does not degrade gracefully. The browser refuses to execute
the subresource entirely, so a one-character drift silently turns a
working page into an inert one with no server-side signal. This bit
`wallet.html`: the `wallet.js` integrity literal was left behind when the
file was rebuilt, so the wallet page loaded, rendered, and did nothing —
`window.QSDMWallet` never existed because the browser had blocked the
script that defines it.

The build script (QSDM/scripts/build_wallet_wasm.sh) rotates these hashes
automatically, but nothing enforced that a hand-edited page kept up. This
does.

Exit codes
----------
0  every checked integrity attribute matches its file (or there were none)
1  at least one mismatch, or a referenced local file is missing
2  usage / environment error
"""

from __future__ import annotations

import base64
import hashlib
import re
import sys
from pathlib import Path

# Matches an element carrying both a src/href and an integrity attribute,
# in either attribute order.
TAG_RE = re.compile(r"<(?:script|link)\b[^>]*>", re.IGNORECASE)
SRC_RE = re.compile(r'\b(?:src|href)\s*=\s*"([^"]+)"', re.IGNORECASE)
INTEGRITY_RE = re.compile(r'\bintegrity\s*=\s*"([^"]+)"', re.IGNORECASE)

ALGOS = {"sha256": hashlib.sha256, "sha384": hashlib.sha384, "sha512": hashlib.sha512}


def compute(path: Path, algo: str) -> str:
    digest = ALGOS[algo](path.read_bytes()).digest()
    return f"{algo}-{base64.b64encode(digest).decode('ascii')}"


def check_file(html: Path, root: Path) -> list[str]:
    problems: list[str] = []
    text = html.read_text(encoding="utf-8", errors="replace")

    for tag in TAG_RE.findall(text):
        integrity_m = INTEGRITY_RE.search(tag)
        src_m = SRC_RE.search(tag)
        if not integrity_m or not src_m:
            continue

        src = src_m.group(1)
        # Only local, same-origin assets are checkable. A CDN URL's bytes
        # are not in the repo, so its hash is out of scope here.
        if "://" in src or src.startswith("//"):
            continue

        target = (root / src.lstrip("/")).resolve()
        if not target.is_file():
            problems.append(
                f"{html.relative_to(root.parent.parent.parent)}: "
                f"references {src!r} with an integrity attribute, but that file does not exist"
            )
            continue

        for declared in integrity_m.group(1).split():
            algo = declared.split("-", 1)[0].lower()
            if algo not in ALGOS:
                problems.append(
                    f"{html.name}: {src} declares unsupported hash algorithm {algo!r}"
                )
                continue
            actual = compute(target, algo)
            if declared != actual:
                problems.append(
                    f"{html.name}: SRI mismatch for {src}\n"
                    f"    declared: {declared}\n"
                    f"    actual:   {actual}\n"
                    f"    -> the browser will refuse to load this subresource"
                )

    return problems


def main(argv: list[str]) -> int:
    repo_root = Path(__file__).resolve().parent.parent
    landing = repo_root / "QSDM" / "deploy" / "landing"
    if len(argv) > 1:
        landing = Path(argv[1]).resolve()

    if not landing.is_dir():
        print(f"error: landing directory not found: {landing}", file=sys.stderr)
        return 2

    problems: list[str] = []
    checked = 0
    for html in sorted(landing.rglob("*.html")):
        checked += 1
        problems.extend(check_file(html, landing))

    if problems:
        print(f"SRI lint FAILED ({len(problems)} problem(s) across {checked} page(s)):\n")
        for p in problems:
            print(f"  - {p}")
        print(
            "\nRegenerate hashes with QSDM/scripts/build_wallet_wasm.sh, "
            "or recompute manually:\n"
            "  openssl dgst -sha384 -binary <file> | openssl base64 -A"
        )
        return 1

    print(f"SRI lint OK: {checked} page(s) checked, all local integrity hashes match.")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
