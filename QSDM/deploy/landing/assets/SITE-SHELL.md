# QSDM site shell — contract

One header/nav, one footer and one status banner for every qsdm.tech page,
rendered by `/assets/site-shell.js`. Styling is in `/assets/site-shell.css`
(colour tokens remain in `/assets/site.css`). No build step, no CDN, no trackers.

## Page template

```html
<head>
  ...
  <link rel="stylesheet" href="/assets/site.css" />
  <link rel="stylesheet" href="/assets/site-shell.css" />
  <!-- page CSS after these -->
</head>
<body data-page="home">
  <header id="site-header"></header>
  <script src="/assets/site-shell.js"></script>   <!-- right after the header -->

  <main id="main"> ... </main>

  <footer id="site-footer"></footer>
  <!-- page scripts; they may use window.QSDM -->
</body>
```

Rules:

- Put the script tag directly after `<header id="site-header">`. The header,
  banner and skip link render before the rest of the page paints. The footer
  renders on `DOMContentLoaded`.
- `<main id="main">` is the skip-link target. If a page already has a
  `.skip-link`, the shell does not add a second one (docs uses `#doc-main`).
- Don't add Google Fonts links. `site-shell.css` self-hosts Outfit (variable,
  400–700) and IBM Plex Mono (400, 500) from `/assets/fonts/` (SIL OFL 1.1,
  licences are next to the files).

## `data-page` values (active-nav highlighting)

| data-page | Highlights |
|---|---|
| `home` | Home |
| `explorer` | Explorer |
| `wallet` | Wallet |
| `mining` | Mining |
| `download` | Download |
| `api`, `docs` | Developers |
| `network`, `validators`, `trust`, `audit` | Network |
| anything else (`privacy`, `support`, ...) | nothing |

The active link gets `aria-current="page"`.

## Navigation

- **Primary nav (7 items):** Home, Explorer, Wallet, Mining, Download,
  Developers (`/api.html`), Network. To change it, edit `NAV` in `site-shell.js`.
- **Footer columns:** Network, Products, Developers, Trust. Edit `FOOTER`.
  Account, VPN, Audit, Attestations, Validators, Privacy, Security and
  Support live here.
- Off-site links use `ext: true`, which renders `class="ext"` (a ↗ arrow) plus
  a visually hidden "(external site)". Use `class="ext"` on in-page off-site
  links too.
- Below 960 px the nav collapses into a menu button. The button has
  `aria-expanded`, and Escape closes the menu.

## Status banner

The banner text is `BANNER_TEXT` in `site-shell.js`, shown as
`<div id="qsdm-migration-notice" role="status">` above the header on every
shell page.

- To update it everywhere, edit that one string.
- To hide it, set `BANNER_ENABLED = false`.
- If a page already contains a static `#qsdm-migration-notice`, the shell
  leaves it alone. A server-side banner injection still works.

## `window.QSDM` helpers

| Helper | Purpose |
|---|---|
| `QSDM.api(path)` | GET JSON from the chain API. `path` is relative to `/api/v1`, e.g. `"/status"`. |
| `QSDM.attest(path)` | GET JSON from the backup node, `/attest/home-validator/api/v1` + path. |
| `QSDM.fmtInt(n)` | `1,234,567`. Returns `—` for missing values. |
| `QSDM.fmtDec("2670028.36988325", 2)` | Exact decimal-string formatting, no float loss. Returns `—` for missing values. |
| `QSDM.fmtDuration(sec)`, `QSDM.ageOf(isoTime)` | Ages and ETAs. |
| `QSDM.setText(id, text)`, `QSDM.short(hash)` | Small DOM and hash helpers. |
| `<button data-copy="text">` / `<button data-copy-target="elementId">` | Copy buttons. Shows "Copied" feedback. |

### API base

| Setting | Behaviour |
|---|---|
| Unset (production) | Try same-origin `/api/v1` first, then `https://api.qsdm.tech/api/v1`. This matches the earlier pages. |
| `window.QSDM_API_BASE = "https://api.qsdm.tech/api/v1"` | Pins one base. Set it in an inline script **before** `site-shell.js`. |
| `window.QSDM_ATTEST_BASE` | Same, for the backup node. Default: same-origin `/attest/home-validator/api/v1`, then `https://api.qsdm.tech/attest/home-validator/api/v1`. |

Live values must start as `—` in the HTML. Never put `0` in a live field.

## Shared components (site-shell.css)

Layout:

- `q-wrap` (page width)
- `page-hero`
- `sec`, `sec-head`
- `q-eyebrow`, `lead`, `q-actions`

Data display:

- `stat-strip` containing `q-stat` (`stat-label`, `stat-value`, `stat-sub`), plus `q-meter`
- `source-line`
- `spec-table` with `<th scope="row">`. It stacks below 600 px.
- `facts` (title + one sentence)
- `q-kv` (a `dl`)
- `q-pill` (`.ok`, `.warn`, `.bad`)

Containers and code:

- `card`, `q-grid` with `q-grid-2/3/4`
- `codeline` + `copy-btn`
- `pre.sample`
- `q-note`
- `visually-hidden`

Classes that could collide with older page CSS carry a `q-` prefix.

## Brand assets and icons

- The header and footer show `/assets/brand/qsdm-logo-small.svg` (`BRAND.mark` in `site-shell.js`). Sources and
  rebuild steps: `brand/README.md`. The Hive icon (`/assets/qsdm-hive-icon.png`) stays only where the content is
  about Hive (the wallet's Hive tab, the extension hand-off page).
- Every page head carries static favicon, apple-touch-icon and manifest `<link>`s (copy them from `index.html`).
  The shell adds any that a page is missing. Main pages also carry `og:image` / `twitter:image`
  (`/assets/brand/og-image.png`, 1200x630).
- CELL amounts: `<img class="cell-ico" src="/assets/brand/cell-coin-small.svg" alt="" width="14" height="14" />`
  before a label. Use `cell-coin.svg` above 32 px.

## Local preview

```
python tools/preview.py            # http://127.0.0.1:8099
```

This serves `site/` and proxies `/api/v1/*` and `/attest/*` to `https://api.qsdm.tech`.
