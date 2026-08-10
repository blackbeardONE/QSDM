#!/usr/bin/env python3
"""Merge the QSDM Account route into a live Caddyfile without replacing it."""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path


class MergeError(ValueError):
    """Raised when the live Caddyfile is ambiguous or unsafe to edit."""


SITE_RE = re.compile(r"^qsdm\.tech\s*\{$")
ACCOUNT_ROUTE_RE = re.compile(r"^handle\s+/api/account/\*\s*\{$")
ACCOUNT_REDIRECT_RE = re.compile(r"^redir\s+/account(?:/|\s)")
EXPECTED_ACCOUNT_REDIRECT = "redir /account /account/ 302"
EXPECTED_ACCOUNT_PROXY = "reverse_proxy 127.0.0.1:8092"


def _active_text(line: str) -> str:
    """Remove an unquoted Caddy comment while preserving quoted values."""

    result: list[str] = []
    quote: str | None = None
    escaped = False
    for character in line:
        if escaped:
            result.append(character)
            escaped = False
            continue
        if character == "\\" and quote is not None:
            result.append(character)
            escaped = True
            continue
        if quote is not None:
            result.append(character)
            if character == quote:
                quote = None
            continue
        if character in ('"', "'", "`"):
            result.append(character)
            quote = character
            continue
        if character == "#":
            break
        result.append(character)
    return "".join(result)


def _brace_delta(line: str) -> int:
    active = _active_text(line)
    quote: str | None = None
    escaped = False
    delta = 0
    for character in active:
        if escaped:
            escaped = False
            continue
        if character == "\\" and quote is not None:
            escaped = True
            continue
        if quote is not None:
            if character == quote:
                quote = None
            continue
        if character in ('"', "'", "`"):
            quote = character
        elif character == "{":
            delta += 1
        elif character == "}":
            delta -= 1
    return delta


def _find_block_end(lines: list[str], start: int) -> int:
    depth = 0
    for index in range(start, len(lines)):
        depth += _brace_delta(lines[index])
        if depth == 0:
            if index == start:
                raise MergeError(f"line {start + 1} does not open a block")
            return index
        if depth < 0:
            break
    raise MergeError(f"block beginning on line {start + 1} is not balanced")


def _find_site(lines: list[str]) -> tuple[int, int]:
    matches = [
        index
        for index, line in enumerate(lines)
        if SITE_RE.fullmatch(_active_text(line).strip())
    ]
    if len(matches) != 1:
        raise MergeError(
            "expected exactly one standalone qsdm.tech site block; "
            f"found {len(matches)}"
        )
    start = matches[0]
    return start, _find_block_end(lines, start)


def _validate_existing_route(
    lines: list[str], route_start: int, site_start: int, site_end: int
) -> None:
    if not (site_start < route_start < site_end):
        raise MergeError("the QSDM Account API route is outside qsdm.tech")
    route_end = _find_block_end(lines, route_start)
    if route_end >= site_end:
        raise MergeError("the QSDM Account API route escapes qsdm.tech")
    proxies = [
        _active_text(line).strip()
        for line in lines[route_start + 1 : route_end]
        if _active_text(line).strip().startswith("reverse_proxy ")
    ]
    if len(proxies) != 1 or proxies[0] not in {
        EXPECTED_ACCOUNT_PROXY,
        EXPECTED_ACCOUNT_PROXY + " {",
    }:
        raise MergeError(
            "the existing QSDM Account route must contain exactly "
            f"'{EXPECTED_ACCOUNT_PROXY}'"
        )


def merge_caddy_text(text: str) -> str:
    """Return a Caddyfile with the account route and redirect merged safely."""

    if "\r" in text.replace("\r\n", ""):
        raise MergeError("Caddyfile contains unsupported bare carriage returns")
    newline = "\r\n" if "\r\n" in text else "\n"
    had_final_newline = text.endswith(("\n", "\r"))
    lines = text.splitlines()
    site_start, site_end = _find_site(lines)

    route_matches = [
        index
        for index, line in enumerate(lines)
        if ACCOUNT_ROUTE_RE.fullmatch(_active_text(line).strip())
    ]
    account_path_mentions = [
        index
        for index, line in enumerate(lines)
        if "/api/account/" in _active_text(line)
    ]
    unexpected_mentions = [
        index for index in account_path_mentions if index not in route_matches
    ]
    if unexpected_mentions:
        line_numbers = ", ".join(str(index + 1) for index in unexpected_mentions)
        raise MergeError(
            "unexpected active /api/account/ directives on line(s) " + line_numbers
        )
    if len(route_matches) > 1:
        raise MergeError("multiple QSDM Account API routes are present")

    if route_matches:
        _validate_existing_route(lines, route_matches[0], site_start, site_end)
    else:
        roots = [
            index
            for index in range(site_start + 1, site_end)
            if _active_text(lines[index]).strip() == "root * /var/www/qsdm"
        ]
        if len(roots) != 1:
            raise MergeError(
                "expected exactly one 'root * /var/www/qsdm' anchor in qsdm.tech"
            )
        indent = re.match(r"^[ \t]*", lines[roots[0]]).group(0)
        route_block = [
            "",
            f"{indent}# QSDM Account is a separate identity-only service. It stores no wallet",
            f"{indent}# keys and is intentionally not part of the validator API process.",
            f"{indent}handle /api/account/* {{",
            f"{indent}\treverse_proxy 127.0.0.1:8092 {{",
            f"{indent}\t\theader_up X-Forwarded-Proto https",
            f"{indent}\t\theader_up X-Real-IP {{remote_host}}",
            f"{indent}\t\theader_up X-Forwarded-For {{remote_host}}",
            f"{indent}\t}}",
            f"{indent}}}",
        ]
        lines[roots[0] + 1 : roots[0] + 1] = route_block
        site_start, site_end = _find_site(lines)

    redirects = [
        index
        for index in range(site_start + 1, site_end)
        if ACCOUNT_REDIRECT_RE.match(_active_text(lines[index]).strip())
    ]
    if redirects:
        if len(redirects) != 1 or _active_text(lines[redirects[0]]).strip() != EXPECTED_ACCOUNT_REDIRECT:
            raise MergeError(
                "the existing account redirect must be exactly "
                f"'{EXPECTED_ACCOUNT_REDIRECT}'"
            )
    else:
        wallet_redirects = [
            index
            for index in range(site_start + 1, site_end)
            if _active_text(lines[index]).strip()
            == "redir /wallet/ /wallet.html 302"
        ]
        if len(wallet_redirects) != 1:
            raise MergeError(
                "expected exactly one wallet redirect anchor in qsdm.tech"
            )
        indent = re.match(r"^[ \t]*", lines[wallet_redirects[0]]).group(0)
        lines.insert(
            wallet_redirects[0] + 1,
            f"{indent}{EXPECTED_ACCOUNT_REDIRECT}",
        )

    result = newline.join(lines)
    if had_final_newline:
        result += newline
    return result


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Safely merge the QSDM Account route into a Caddyfile."
    )
    parser.add_argument("source", type=Path)
    parser.add_argument("destination", type=Path)
    arguments = parser.parse_args()
    try:
        with arguments.source.open("r", encoding="utf-8", newline="") as source:
            merged = merge_caddy_text(source.read())
        with arguments.destination.open("w", encoding="utf-8", newline="") as target:
            target.write(merged)
    except (OSError, UnicodeError, MergeError) as error:
        print(f"Could not merge QSDM Account Caddy route: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
