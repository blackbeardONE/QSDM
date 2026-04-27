#!/usr/bin/env bash
# sync-wiki.sh — mirror source-controlled docs to the GitHub Wiki.
#
# Background:
#   GitHub wikis live in a separate repo: <main>.wiki.git. That repo is
#   only materialised AFTER someone clicks "Wiki → Create the first page"
#   in the web UI; the API cannot bootstrap it. Once the first page
#   exists, this script keeps the wiki in sync with the canonical docs
#   under QSDM/docs/docs/ and apps/qsdm-nvidia-ngc/.
#
# Usage:
#   1. One-time: go to https://github.com/blackbeardONE/QSDM/wiki and
#      click "Create the first page". Title it "Home", paste anything,
#      save. This materialises the wiki repo.
#   2. Every time the source docs change: bash QSDM/scripts/sync-wiki.sh
#
# What this script copies (with lightly edited headers so intra-wiki
# links work):
#   - QSDM/docs/docs/OPERATOR_GUIDE.md          -> Home.md (landing)
#   - QSDM/docs/docs/NODE_ROLES.md              -> Node-Roles.md
#   - QSDM/docs/docs/VALIDATOR_QUICKSTART.md    -> Validator-Quickstart.md
#   - QSDM/docs/docs/MINER_QUICKSTART.md        -> Miner-Quickstart.md
#   - QSDM/docs/docs/MINING_PROTOCOL_V2.md      -> Mining-Protocol-V2.md  (canonical v2 spec)
#   - QSDM/docs/docs/MINING_PROTOCOL.md         -> Mining-Protocol.md     (frozen v1 spec)
#   - QSDM/docs/docs/CELL_TOKENOMICS.md         -> Cell-Tokenomics.md
#   - QSDM/docs/docs/NVIDIA_LOCK_CONSENSUS_SCOPE.md -> NVIDIA-Lock-Scope.md
#   - apps/qsdm-nvidia-ngc/QUICKSTART.md    -> NGC-Sidecar-Quickstart.md
#
# Source of truth remains the markdown under QSDM/docs/docs/ and
# apps/qsdm-nvidia-ngc/. Do NOT edit the wiki pages directly — any
# web-UI edit gets overwritten on the next sync.

set -euo pipefail

OWNER="blackbeardONE"
REPO="QSDM"
WIKI_URL="https://github.com/${OWNER}/${REPO}.wiki.git"

HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
WORKSPACE="$(cd -- "${HERE}/../.." &>/dev/null && pwd)"

WIKI_DIR="$(mktemp -d -t qsdm-wiki.XXXXXX)"
trap 'rm -rf "$WIKI_DIR"' EXIT

echo "[sync-wiki] cloning $WIKI_URL into $WIKI_DIR"
if ! git clone --depth 1 "$WIKI_URL" "$WIKI_DIR" 2>/dev/null; then
  echo ""
  echo "[sync-wiki] ERROR: could not clone the wiki repo."
  echo ""
  echo "  GitHub only materialises the wiki repo after someone clicks"
  echo "  'Create the first page' in the web UI. Do that once at:"
  echo "    https://github.com/${OWNER}/${REPO}/wiki"
  echo "  then re-run this script."
  exit 1
fi

copy_page() {
  local src="$1" dst="$2" title="$3"
  local src_abs="${WORKSPACE}/${src}"
  local dst_abs="${WIKI_DIR}/${dst}"

  if [ ! -f "$src_abs" ]; then
    echo "[sync-wiki] skip: $src not found"
    return
  fi

  {
    echo "<!-- This page is auto-generated from ${src}. Do not edit here; edit the source and run QSDM/scripts/sync-wiki.sh. -->"
    echo ""
    # Strip the source's top-level # heading (we'll rely on the wiki title).
    awk 'NR==1 && /^# / {next} {print}' "$src_abs" \
      | sed -E 's|\]\((\./)?([A-Z_]+)\.md\)|](\2)|g' \
      | sed -E 's|\]\(\.\./\.\./\.\./apps/qsdm-nvidia-ngc/QUICKSTART\.md\)|](NGC-Sidecar-Quickstart)|g' \
      | sed -E 's|\]\(\.\./apps/qsdm-nvidia-ngc/QUICKSTART\.md\)|](NGC-Sidecar-Quickstart)|g'
  } > "$dst_abs"

  echo "[sync-wiki] wrote ${dst} (<- ${src})"
}

# Remap MD_BASENAME -> wiki title: ONLY the basenames of files we
# actually copy. Any other inter-doc link (e.g. to ROADMAP.md) is left
# as-is and resolves to a broken page, which is correct — we don't
# publish those to the wiki.
copy_page "QSDM/docs/docs/OPERATOR_GUIDE.md"           "Home.md"                       "Home"
copy_page "QSDM/docs/docs/NODE_ROLES.md"               "Node-Roles.md"                 "Node Roles"
copy_page "QSDM/docs/docs/VALIDATOR_QUICKSTART.md"     "Validator-Quickstart.md"       "Validator Quickstart"
copy_page "QSDM/docs/docs/MINER_QUICKSTART.md"         "Miner-Quickstart.md"           "Miner Quickstart"
copy_page "QSDM/docs/docs/MINING_PROTOCOL_V2.md"       "Mining-Protocol-V2.md"         "Mining Protocol v2 (Canonical)"
copy_page "QSDM/docs/docs/MINING_PROTOCOL.md"          "Mining-Protocol.md"            "Mining Protocol (v1, frozen)"
copy_page "QSDM/docs/docs/CELL_TOKENOMICS.md"          "Cell-Tokenomics.md"            "Cell Tokenomics"
copy_page "QSDM/docs/docs/NVIDIA_LOCK_CONSENSUS_SCOPE.md" "NVIDIA-Lock-Scope.md"       "NVIDIA Lock Scope"
copy_page "apps/qsdm-nvidia-ngc/QUICKSTART.md"     "NGC-Sidecar-Quickstart.md"     "NGC Sidecar Quickstart"

# Build a top-level sidebar (shown on every wiki page).
cat > "${WIKI_DIR}/_Sidebar.md" <<'EOF'
### QSDM Operator Wiki

- **[Home](Home)** — End-to-end operator guide
- [Node Roles](Node-Roles)
- [Validator Quickstart](Validator-Quickstart)
- [Miner Quickstart](Miner-Quickstart)
- [Mining Protocol v2 (Canonical)](Mining-Protocol-V2)
- [Mining Protocol (v1, frozen)](Mining-Protocol)
- [Cell Tokenomics](Cell-Tokenomics)
- [NVIDIA Lock Scope](NVIDIA-Lock-Scope)
- [NGC Sidecar Quickstart](NGC-Sidecar-Quickstart)

---

**Source of truth.** Pages here are auto-generated from the canonical
markdown under `QSDM/docs/docs/` on the main repo. Any web-UI edit
gets overwritten on the next sync. Edit the source markdown instead.
EOF

# Footer shared across pages.
cat > "${WIKI_DIR}/_Footer.md" <<'EOF'
---

This wiki mirrors the [QSDM](https://github.com/blackbeardONE/QSDM)
main repo docs. For the latest canonical content, see
[`QSDM/docs/docs/`](https://github.com/blackbeardONE/QSDM/tree/main/QSDM/docs/docs).
EOF

cd "$WIKI_DIR"
if [ -z "$(git status --porcelain)" ]; then
  echo "[sync-wiki] wiki already up to date; nothing to commit"
  exit 0
fi

git add .
git -c user.name="qsdm-docs-sync" \
    -c user.email="admin@qsdm.tech" \
    commit -m "sync: mirror main-repo docs to wiki"

echo "[sync-wiki] pushing to $WIKI_URL"
git push origin master 2>/dev/null || git push origin main

echo "[sync-wiki] done."
