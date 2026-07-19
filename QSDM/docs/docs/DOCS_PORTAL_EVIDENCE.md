# Docs Portal Evidence — 2026-07-09

> Ship log for the **qsdm.tech landing + `/docs/` knowledge-base portal**.
> Companion to the `RELEASE_EVIDENCE_*` files; this is the static-site
> equivalent — no new binary or container, just a Caddy webroot delta
> and a curated SPA.
>
> Reproduce by curling the URLs in §"Live probes" from any host.

## What shipped

| Surface | Status |
|---|---|
| Landing page (`qsdm.tech/`) — capabilities section, unified product nav (Download · Wallet · Capabilities · Mining · Docs · Explorer · Audit · Trust), Hive/CELL/edge/home-gateway messaging aligned to v0.4.3 + Hive 1.3.99 | LIVE (after deploy) |
| Docs portal (`qsdm.tech/docs/`) — SPA with sidebar, hash routing, client-side markdown render, live fetch from main; adds home gateway, task registry, tray monitor, missing runbooks; referral path fixed | LIVE (after deploy) |
| `sitemap.xml`, `robots.txt`, `humans.txt` | LIVE (after deploy) |
| Caddyfile CSP — `connect-src` includes `https://raw.githubusercontent.com` + `https://api.github.com`; `img-src` includes `raw.githubusercontent.com` | LIVE |

No backend changes required for docs content updates that only touch
Markdown under `QSDM/docs/docs/` (fetched live from `main`). Shell
files under `QSDM/deploy/landing/` require a landing deploy.

## Docs portal at a glance

- ~95 curated entries across 9 sections (Get started · Hive +
  Integrations · Wallet · Mining · Validators & operators · Protocol &
  design · Performance · Reference · Runbooks · Project).
- Source of truth is the curated `SECTIONS` manifest in
  `QSDM/deploy/landing/docs/docs.js`. Each entry has `{ slug, title,
  repoPath, badge? }` (some Hive/edge pages also ship `inlineMarkdown`).
- Markdown is fetched at runtime from `raw.githubusercontent.com/
  blackbeardONE/QSDM/main/<repoPath>`. **No mirror, no rebuild
  needed** — pushing to `main` updates docs on the next page load.
- `markdown-it@14.1.0` is vendored at `/docs/lib/markdown-it.min.js`
  (123 618 B) with SRI
  `sha384-wLhprpjsmjc/XYIcF+LpMxd8yS1gss6jhevOp6F6zhiIoFK6AmHtm4bGKtehTani`.
  No CDN in `script-src`.
- The version pill defaults to **v0.4.3** and auto-refreshes from
  `api.github.com/repos/blackbeardONE/QSDM/releases/latest`.
- Filenames with literal spaces (one entry — `Feature Summary.md`)
  are handled by an `encRepoPath()` helper that splits on `/` and
  `encodeURIComponent`-s each segment, preserving slashes.

## Live probes

```text
$ curl -s -o /dev/null -w '%{http_code}\n' https://qsdm.tech/
200
$ curl -s -o /dev/null -w '%{http_code}\n' https://qsdm.tech/docs/
200
$ curl -s -o /dev/null -w '%{http_code}\n' https://qsdm.tech/docs/docs.css
200
$ curl -s -o /dev/null -w '%{http_code}\n' https://qsdm.tech/docs/docs.js
200
$ curl -s -o /dev/null -w '%{http_code}\n' https://qsdm.tech/docs/lib/markdown-it.min.js
200
$ curl -s -o /dev/null -w '%{http_code}\n' https://qsdm.tech/sitemap.xml
200
$ curl -s -o /dev/null -w '%{http_code}\n' https://qsdm.tech/robots.txt
200
$ curl -sI https://qsdm.tech/docs/ | grep -i content-security-policy
content-security-policy: default-src 'self'; img-src 'self' data: https://raw.githubusercontent.com; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline' 'wasm-unsafe-eval'; connect-src 'self' https://api.qsdm.tech https://dashboard.qsdm.tech https://raw.githubusercontent.com https://api.github.com; frame-ancestors 'none'; upgrade-insecure-requests
$ curl -s https://qsdm.tech/ | grep -oE 'href="[^"]+"' | head
(expect Download, Wallet, Capabilities, Mining, Docs, Explorer, Audit, Trust)
$ curl -sI -H 'Origin: https://qsdm.tech' \
    https://raw.githubusercontent.com/blackbeardONE/QSDM/main/QSDM/docs/docs/QUICK_START.md \
  | grep -iE 'access-control-allow-origin|content-type'
content-type: text/plain; charset=utf-8
access-control-allow-origin: *
```

## Manifest integrity

All 78 `repoPath` entries declared in `docs.js` were HEAD-tested
against `raw.githubusercontent.com/.../main/<repoPath>`:

```
TOC entries: 78
ok=78  fail=0
```

(`Feature Summary.md` fails when curled with a literal space; it
passes when URL-encoded — which is exactly what `encRepoPath()`
emits at runtime.)

## Regression sweep

Existing pages still render with their pre-ship byte counts:

```
200  26133  /wallet.html
200  37062  /wallet.js
200  3884131  /wallet.wasm       (3.88 MB — v0.4.0 build, unchanged)
200  13155  /validators.html
200  10777  /trust.html
200  24171  /download.html
200  23106  /chain.html
200          /api/v1/health      (Caddy → 127.0.0.1:8443, proxy regression-free)
```

`/wallet.html`'s SRI on `wallet.js` and `wallet.wasm` was not touched
— the v0.4.0 hashes from RELEASE_EVIDENCE_v0.4.0.md still apply.

## File inventory

```
QSDM/deploy/landing/
├── index.html                       (rewritten — 919 → 365 lines)
├── sitemap.xml                      (new)
├── robots.txt                       (new)
├── _install_docs_site.sh            (new — deploy automation)
└── docs/                            (new directory)
    ├── index.html                   ( 3 914 B  — SPA shell)
    ├── docs.css                     (11 161 B  — sidebar + content theme)
    ├── docs.js                      (27 075 B  — TOC + router + render)
    └── lib/
        └── markdown-it.min.js       (123 618 B  — vendored, SRI-pinned)

QSDM/deploy/Caddyfile                (CSP `connect-src` + `img-src` +
                                      `try_files` chain; ran caddy fmt)
```

## What's *not* in scope

- No new backend or WASM artefacts — the v0.4.0 binary, container,
  and `wallet.wasm` are unchanged. The release pipeline did not run.
- No new cosign signatures. Static-site evidence lives in this
  document only.
- The docs portal exposes existing markdown — no content was rewritten,
  reorganised, or deleted in the repo. The curated `SECTIONS`
  manifest is the only "new" editorial layer.

## Future polish (deferred)

- Right-pane "On this page" outline (`#`-anchor TOC scraped from
  rendered headings).
- Local lunr.js full-text search across all 65 entries.
- Service-worker cache so the SPA + recently-viewed docs work offline.
- A subagent that re-validates the 78 TOC entries on every push to
  `main` and opens a PR if any 404.
