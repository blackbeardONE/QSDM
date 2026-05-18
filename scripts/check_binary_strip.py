#!/usr/bin/env python3
"""
ELF binary strip lint.

Enforces the unwritten production convention I caught manually
during the 2026-05-18 15:46 UTC qsdm binary swap: every Go
binary shipped to /opt/qsdm/qsdm on BLR1 since 2026-05-13 has
been built with `-ldflags='-s -w'` to strip both the symbol
table (-s) and the DWARF debug info (-w). The first build I
produced today forgot the flag and weighed in at 45.6 MB --
40% bigger than every recent .bak file (all ~32 MB). Shipping
the unstripped binary would not have broken anything
functionally, but it would have leaked function names and
file paths into pprof/debugger snapshots a researcher might
later capture, and would have stood out as an obvious anomaly
in the next `ls /opt/qsdm/qsdm.bak.*` review.

For each ELF binary passed on the command line, this script:

  1. Verifies the file exists and is an ELF binary (magic
     bytes \\x7fELF).
  2. Tries `file <path>` first (the canonical Linux/Mac
     toolchain marker; output contains `, stripped` for a
     stripped binary and `, not stripped` otherwise). This
     is what `man file` documents and what packagers /
     distribution maintainers have been parsing for decades.
  3. If `file` is not available (e.g. on a bare Windows
     box), falls back to parsing the ELF section-header
     string table directly via the `struct` module and
     asserts that `.debug_info` is absent. Both -s and -w
     drop the entire `.debug_*` family, so the absence of
     `.debug_info` is a reliable strip indicator.

Acceptable states:
    binary is stripped (`file` output contains `, stripped`,
                         or no `.debug_info` section)        pass
    binary is NOT stripped                                    FAIL

Skips:
    non-ELF files (Windows .exe, macOS Mach-O) are reported,
    not failed -- the strip convention here is specifically
    about Linux/amd64 production binaries on BLR1. The
    /opt/qsdm/qsdm path is always ELF; multi-platform builds
    have their own per-target conventions.

Exit codes:
    0  all binaries are stripped (or skipped)
    1  any binary is unstripped (CI failure)
    2  argument or setup error (file not found, not ELF,
       `file` command absent AND ELF parse failed)

Usage:
    python3 scripts/check_binary_strip.py /opt/qsdm/qsdm
    python3 scripts/check_binary_strip.py --quiet /opt/qsdm/qsdm
    python3 scripts/check_binary_strip.py --remote root@node.qsdm.tech /opt/qsdm/qsdm

The `--remote` form runs `ssh <host> file <path>` so an operator
on a Windows workstation can lint the production binary without
copying it down first. The local-path form is the CI-friendly
mode; the remote form is the post-deploy verification mode that
pairs with check_sitemap_freshness.py.

Designed as evidence for audit row infra-06. Same stdlib-only
convention as check_runbook_coverage.py and
check_sitemap_freshness.py.
"""

from __future__ import annotations

import argparse
import shutil
import struct
import subprocess
import sys
from pathlib import Path
from typing import List, Optional, Tuple


ELF_MAGIC = b"\x7fELF"


def is_elf(path: Path) -> bool:
    try:
        with path.open("rb") as f:
            return f.read(4) == ELF_MAGIC
    except OSError:
        return False


def file_command_says_stripped(path: Path) -> Optional[bool]:
    """Return True if `file <path>` output contains `, stripped`.

    Returns None if the `file` command is not available on the
    PATH. The caller falls back to manual ELF parsing in that
    case.
    """
    if shutil.which("file") is None:
        return None
    try:
        out = subprocess.run(
            ["file", "--brief", str(path)],
            check=True,
            capture_output=True,
            text=True,
            timeout=5,
        ).stdout
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
        return None
    # `file --brief` output examples:
    #   ELF 64-bit LSB executable, x86-64, version 1 (SYSV), statically linked, Go BuildID=..., stripped
    #   ELF 64-bit LSB executable, x86-64, version 1 (SYSV), statically linked, Go BuildID=..., with debug_info, not stripped
    if ", stripped" in out:
        return True
    if ", not stripped" in out:
        return False
    # `file` parsed the ELF but didn't comment on strip state (very old
    # libmagic versions); fall back to manual parse so we don't false-pass.
    return None


def remote_file_says_stripped(host: str, path: str) -> Optional[bool]:
    """Run `ssh <host> file <path>` and parse the same markers.

    Returns None if ssh is unreachable or `file` is not available
    on the remote.
    """
    if shutil.which("ssh") is None:
        return None
    try:
        out = subprocess.run(
            ["ssh", host, "file", "--brief", path],
            check=True,
            capture_output=True,
            text=True,
            timeout=15,
        ).stdout
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
        return None
    if ", stripped" in out:
        return True
    if ", not stripped" in out:
        return False
    return None


def elf_has_debug_info_section(path: Path) -> Optional[bool]:
    """Manual ELF section-header parse: return True iff a section
    named `.debug_info` exists in the section-header string table.

    Returns None on parse failure (not ELF, truncated file, etc.).
    The caller treats None as "couldn't determine; bail with exit 2"
    rather than silently false-pass.
    """
    try:
        with path.open("rb") as f:
            header = f.read(64)
            if not header.startswith(ELF_MAGIC):
                return None
            ei_class = header[4]  # 1=32-bit, 2=64-bit
            ei_data = header[5]   # 1=little-endian, 2=big-endian
            endian = "<" if ei_data == 1 else ">"
            if ei_class == 2:
                fmt_off = endian + "QQQ"
                e_shoff = struct.unpack(endian + "Q", header[40:48])[0]
                e_shentsize, e_shnum, e_shstrndx = struct.unpack(
                    endian + "HHH", header[58:64]
                )
            elif ei_class == 1:
                e_shoff = struct.unpack(endian + "I", header[32:36])[0]
                e_shentsize, e_shnum, e_shstrndx = struct.unpack(
                    endian + "HHH", header[46:52]
                )
            else:
                return None
            if e_shnum == 0 or e_shstrndx == 0:
                return None
            f.seek(e_shoff + e_shstrndx * e_shentsize)
            shstr_hdr = f.read(e_shentsize)
            if ei_class == 2:
                sh_offset = struct.unpack(endian + "Q", shstr_hdr[24:32])[0]
                sh_size = struct.unpack(endian + "Q", shstr_hdr[32:40])[0]
            else:
                sh_offset = struct.unpack(endian + "I", shstr_hdr[16:20])[0]
                sh_size = struct.unpack(endian + "I", shstr_hdr[20:24])[0]
            f.seek(sh_offset)
            shstrtab = f.read(sh_size)
        return b".debug_info\x00" in shstrtab
    except (OSError, struct.error):
        return None


def check_one(
    path_arg: str, remote: Optional[str], quiet: bool
) -> Tuple[int, Optional[bool], str]:
    """Return (exit_contribution, stripped_state, message).

    exit_contribution is 0 (pass), 1 (fail), 2 (setup error)."""
    if remote:
        stripped = remote_file_says_stripped(remote, path_arg)
        if stripped is None:
            return (
                2,
                None,
                f"remote `ssh {remote} file {path_arg}` failed or `file` "
                f"command not available on {remote}",
            )
        return (
            0 if stripped else 1,
            stripped,
            f"{remote}:{path_arg}: "
            + ("stripped (ok)" if stripped else "NOT STRIPPED"),
        )

    path = Path(path_arg)
    if not path.exists():
        return (2, None, f"file not found: {path}")
    if not is_elf(path):
        return (
            0,
            None,
            f"{path}: not an ELF binary (skipped; strip convention is "
            f"Linux-specific)",
        )

    # Try `file` first (canonical, robust to future Go toolchain changes).
    stripped = file_command_says_stripped(path)
    if stripped is None:
        # Fall back to manual ELF parse.
        has_debug = elf_has_debug_info_section(path)
        if has_debug is None:
            return (
                2,
                None,
                f"{path}: could not determine strip state (`file` absent "
                f"AND manual ELF parse failed -- truncated or unsupported "
                f"ELF variant)",
            )
        stripped = not has_debug
    return (
        0 if stripped else 1,
        stripped,
        f"{path}: " + ("stripped (ok)" if stripped else "NOT STRIPPED"),
    )


def main(argv: List[str]) -> int:
    parser = argparse.ArgumentParser(
        description="QSDM ELF binary strip lint (audit row infra-06)",
    )
    parser.add_argument(
        "binaries",
        nargs="+",
        help="One or more paths to ELF binaries to lint",
    )
    parser.add_argument(
        "--remote",
        default=None,
        help="ssh host (e.g. root@node.qsdm.tech) to lint the binary on a "
        "remote machine via `ssh <host> file <path>`. Useful for "
        "post-deploy verification from a Windows workstation.",
    )
    parser.add_argument(
        "--quiet",
        action="store_true",
        help="Suppress per-binary success lines; only print failures + summary",
    )
    args = parser.parse_args(argv)

    failures: List[str] = []
    setup_errors: List[str] = []
    passed = 0
    skipped = 0

    for b in args.binaries:
        rc, stripped, msg = check_one(b, args.remote, args.quiet)
        if rc == 2:
            setup_errors.append(msg)
            print(f"  ERROR {msg}", file=sys.stderr)
        elif rc == 1:
            failures.append(msg)
            print(f"  FAIL  {msg}", file=sys.stderr)
        else:
            if stripped is None:
                skipped += 1
                if not args.quiet:
                    print(f"  skip  {msg}")
            else:
                passed += 1
                if not args.quiet:
                    print(f"  ok    {msg}")

    if setup_errors:
        return 2
    if failures:
        print(
            f"\nFAIL: {len(failures)} unstripped binar{'y' if len(failures)==1 else 'ies'}",
            file=sys.stderr,
        )
        return 1
    summary = f"\nOK: {passed} stripped binar{'y' if passed==1 else 'ies'}"
    if skipped:
        summary += f"; {skipped} skipped (non-ELF)"
    print(summary)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
