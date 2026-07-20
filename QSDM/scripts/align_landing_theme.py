#!/usr/bin/env python3
"""Align product pages to the shared black / minimal site chrome (UTF-8 safe)."""
from __future__ import annotations

import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1] / "deploy" / "landing"

FONT_LINKS = """\
<link rel="preconnect" href="https://fonts.googleapis.com" />
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
<link href="https://fonts.googleapis.com/css2?family=IBM+Plex+Mono:wght@400;500&family=Outfit:wght@400;500;600;700&display=swap" rel="stylesheet" />
<link rel="stylesheet" href="/assets/site.css" />
"""

NAV_ITEMS = [
    ("/online.html", "Online", "online"),
    ("/wallet.html", "Wallet", "wallet"),
    ("/explorer.html", "Explorer", "explorer"),
    ("/chain.html", "Chain", "chain"),
    ("/validators.html", "Validators", "validators"),
    ("/docs/", "Docs", "docs"),
    ("/audit.html", "Audit", "audit"),
    ("/trust.html", "Trust", "trust"),
    ("/api.html", "API", "api"),
]


def make_header(active: str) -> str:
    nav_parts = []
    for href, label, key in NAV_ITEMS:
        cls = ' class="active"' if key == active else ""
        nav_parts.append(f'      <a href="{href}"{cls}>{label}</a>')
    nav = "\n".join(nav_parts)
    mobile = "\n".join(f'    <a href="{href}">{label}</a>' for href, label, _ in NAV_ITEMS)
    return f"""\
<header class="site-header">
  <div class="site-header-inner">
    <a class="site-brand" href="/">
      <img src="/assets/qsdm-hive-icon.png" alt="" />
      <span>QSDM</span>
    </a>
    <nav class="site-nav" aria-label="Primary">
{nav}
    </nav>
    <div class="site-actions">
      <a class="site-btn site-btn-ghost" href="/wallet.html">Wallet</a>
      <a class="site-btn site-btn-solid" href="/download.html">Download</a>
      <button class="site-menu-toggle" type="button" id="siteMenuToggle" aria-label="Open menu" aria-expanded="false">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M20 7H4V5H20V7ZM20 13H4V11H20V13ZM20 19H4V17H20V19Z"/></svg>
      </button>
    </div>
  </div>
  <div class="site-mobile-nav" id="siteMobileNav">
{mobile}
    <a href="/download.html">Download</a>
  </div>
</header>"""


REPLACEMENTS = [
    ('content="#07171d"', 'content="#061116"'),
    ("--bg: #07171d", "--bg: #061116"),
    ("--bg-2: #0a252d", "--bg-2: #0a1f26"),
    ("--panel: #102f38", "--panel: #0c242c"),
    ("--panel-2: #173d48", "--panel-2: #12323c"),
    ("--border: rgba(142, 220, 224, .18)", "--border: rgba(142, 220, 224, 0.16)"),
    ("--text-2: #adc6cc", "--text-2: rgba(244, 251, 253, 0.62)"),
    ("--muted: #7fa3aa", "--muted: rgba(173, 198, 204, 0.72)"),
    (
        "--mono: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;",
        '--mono: "IBM Plex Mono", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;',
    ),
    (
        '--mono: ui-monospace, SFMono-Regular, Menlo, Monaco, "Cascadia Mono", Consolas, monospace;',
        '--mono: "IBM Plex Mono", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;',
    ),
    (
        '--sans: -apple-system, BlinkMacSystemFont, "Segoe UI", Inter, Roboto, Arial, sans-serif;',
        '--sans: "Outfit", system-ui, -apple-system, "Segoe UI", sans-serif;',
    ),
    (
        '--sans: -apple-system, BlinkMacSystemFont, "Segoe UI", Inter, Roboto, "Helvetica Neue", Arial, sans-serif;',
        '--sans: "Outfit", system-ui, -apple-system, "Segoe UI", sans-serif;',
    ),
    ("background: rgba(7,23,29,.72)", "background: rgba(6,17,22,.82)"),
    ("linear-gradient(180deg, var(--bg) 0%, #0a252d 100%)", "var(--bg)"),
    ("linear-gradient(180deg, var(--bg) 0%, var(--bg-2) 100%)", "var(--bg)"),
    ("linear-gradient(180deg, var(--panel), var(--panel-2))", "var(--panel)"),
]


def update_tokens(html: str) -> str:
    for old, new in REPLACEMENTS:
        html = html.replace(old, new)
    html = re.sub(
        r"radial-gradient\(1200px 600px at 10% -10%, rgba\(214,183,95,\.10\), transparent 60%\),\s*",
        "",
        html,
    )
    html = re.sub(
        r"radial-gradient\(900px 500px at 100% 0%, rgba\(142,220,224,\.10\), transparent 60%\),\s*",
        "",
        html,
    )
    return html


def inject_fonts(html: str) -> str:
    if "assets/site.css" in html:
        return html
    needle = '<link rel="icon" href="/assets/qsdm-hive-icon.png" />'
    if needle in html:
        return html.replace(needle, needle + "\n" + FONT_LINKS)
    return html.replace("</title>", "</title>\n" + FONT_LINKS)


def replace_header(html: str, active: str) -> str:
    header = make_header(active)
    html2, n = re.subn(r"(?s)<header\b.*?</header>", header, html, count=1)
    if n:
        return html2
    html2, n = re.subn(
        r'(?s)<div class="nav">\s*<div class="container nav-inner">.*?</nav>\s*</div>\s*</div>',
        header,
        html,
        count=1,
    )
    return html2 if n else html


def inject_nav_js(html: str) -> str:
    if "site-nav.js" in html:
        return html
    return html.replace("</body>", '  <script src="/assets/site-nav.js"></script>\n</body>')


def align_page(name: str, active: str) -> None:
    path = ROOT / name
    html = path.read_text(encoding="utf-8")
    html = inject_fonts(html)
    html = update_tokens(html)
    html = replace_header(html, active)
    html = inject_nav_js(html)
    path.write_text(html, encoding="utf-8", newline="\n")
    print(f"aligned {name}")


def align_docs_index() -> None:
    path = ROOT / "docs" / "index.html"
    html = path.read_text(encoding="utf-8")
    html = html.replace('content="#07171d"', 'content="#000000"')
    html = re.sub(r"v0\.4\.[0-9]+(?:-rc\.[0-9]+)?", "v0.4.7-rc.4", html)
    html = inject_fonts(html)
    if 'href="/">Home' not in html:
        html = html.replace(
            '<a class="docs-link" href="https://github.com/blackbeardONE/QSDM"',
            '<a class="docs-link" href="/">Home</a>\n      '
            '<a class="docs-link" href="https://github.com/blackbeardONE/QSDM"',
        )
    html = html.replace(
        "Knowledge base for QSDM, QSDM Hive, CELL, integrations, NVIDIA-attested mining, CPU shared edge participation, and operator runbooks.",
        "Knowledge base for QSDM Core, Hive, CELL, NVIDIA mining, Mother Hive edge pools, home gateway, wallets, and operator runbooks.",
    )
    path.write_text(html, encoding="utf-8", newline="\n")
    print("aligned docs/index.html")


def main() -> None:
    pages = [
        ("online.html", "online"),
        ("explorer.html", "explorer"),
        ("audit.html", "audit"),
        ("trust.html", "trust"),
        ("chain.html", "chain"),
        ("api.html", "api"),
        ("validators.html", "validators"),
        ("wallet.html", "wallet"),
    ]
    for name, active in pages:
        align_page(name, active)
    align_docs_index()
    # sanity: explorer title still has em dash bytes
    b = (ROOT / "explorer.html").read_bytes()
    assert bytes([0xE2, 0x80, 0x94]) in b, "em dash missing from explorer.html"
    assert b"site-header" in b
    print("ok")


if __name__ == "__main__":
    main()
