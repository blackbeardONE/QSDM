#!/usr/bin/env bash
# install-qsdmminer-console.sh — one-command installer for the QSDM
# friendly console miner on Linux and macOS.
#
# Usage:
#   curl -sSL https://raw.githubusercontent.com/blackbeardONE/QSDM/main/scripts/install-qsdmminer-console.sh | bash
#
# With a pinned version:
#   curl -sSL https://raw.githubusercontent.com/blackbeardONE/QSDM/main/scripts/install-qsdmminer-console.sh | QSDM_VERSION=v0.1.0 bash
#
# What it does:
#   1. Detects OS (linux/darwin) and arch (amd64/arm64).
#   2. Picks the release to install — either $QSDM_VERSION or the
#      latest semver tag from the GitHub Releases API.
#   3. Downloads the matching qsdmminer-console-<os>-<arch> binary
#      and the release's consolidated SHA256SUMS file.
#   4. Verifies the binary's hash against SHA256SUMS. Aborts on
#      mismatch — a mismatch means either the release was tampered
#      with in transit or the mirror is stale, and neither is safe
#      to silently ignore.
#   5. Installs to $QSDM_INSTALL_DIR (default: /usr/local/bin if
#      writable as the current user, else $HOME/.local/bin). Uses
#      `sudo` only if the chosen directory requires it; never for
#      anything else.
#   6. Runs the installed binary with --version and fails if it
#      prints "dev" or "unknown" — that would mean the download
#      bypassed the release pipeline and should never be trusted.
#
# Why verify --version: the installed file at this point has a valid
# SHA256, but a future mirror-compromise scenario could swap the
# binary with an unsigned local build. The release pipeline always
# embeds the tag + commit SHA via -ldflags, so "dev"/"unknown" in
# --version output is a hard signal that something is wrong.
set -euo pipefail

REPO="${QSDM_REPO:-blackbeardONE/QSDM}"
BINARY="qsdmminer-console"

# ---- helpers ---------------------------------------------------------------

err()  { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }
info() { printf '\033[36m==>\033[0m %s\n' "$*"; }
ok()   { printf '\033[32m✓\033[0m  %s\n' "$*"; }

need() {
  command -v "$1" >/dev/null 2>&1 || err "required command not found: $1"
}

need curl
need uname
need mktemp
need sha256sum 2>/dev/null || need shasum  # macOS ships shasum, Linux ships sha256sum

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# ---- platform detection ----------------------------------------------------

case "$(uname -s)" in
  Linux)  os="linux" ;;
  Darwin) os="darwin" ;;
  *)      err "unsupported OS: $(uname -s). See MINER_QUICKSTART.md for manual-build instructions." ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) err "unsupported arch: $(uname -m). Known supported: amd64, arm64." ;;
esac

info "platform: ${os}/${arch}"

# ---- release selection -----------------------------------------------------

version="${QSDM_VERSION:-}"
if [ -z "$version" ]; then
  info "resolving latest release tag via GitHub API…"
  # --fail makes curl return non-zero on HTTP errors (e.g. 404 if
  # the repo has no releases yet), which we want to surface instead
  # of parsing an HTML error page as JSON.
  resp="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest")" \
    || err "could not reach GitHub releases API. Set QSDM_VERSION=vX.Y.Z to pin a specific release."
  # Parse "tag_name": "v0.1.0" without depending on jq.
  version="$(printf '%s\n' "$resp" | grep -E '"tag_name"[[:space:]]*:' | head -n1 | sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/')"
  [ -n "$version" ] || err "could not parse tag_name from GitHub API response."
fi

info "installing ${BINARY} ${version}"

base="https://github.com/${REPO}/releases/download/${version}"
asset="${BINARY}-${os}-${arch}"
asset_url="${base}/${asset}"
sums_url="${base}/SHA256SUMS"

# ---- download + verify -----------------------------------------------------

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

info "downloading ${asset_url}"
curl -fsSL -o "${tmp}/${asset}" "${asset_url}" \
  || err "binary not found at ${asset_url}. Check the release page: https://github.com/${REPO}/releases/tag/${version}"

info "downloading ${sums_url}"
curl -fsSL -o "${tmp}/SHA256SUMS" "${sums_url}" \
  || err "SHA256SUMS not found at ${sums_url}. Refusing to install an unverified binary."

# Extract the expected hash for this asset from the consolidated
# SHA256SUMS file. Lines look like: "<hash>  ./<asset>" or
# "<hash>  <asset>" depending on how sha256sum was invoked; match
# whichever.
expected="$(awk -v a="${asset}" '$2 == a || $2 == "./"a { print $1 }' "${tmp}/SHA256SUMS")"
[ -n "$expected" ] || err "asset ${asset} not listed in SHA256SUMS. Release may be incomplete."

actual="$(sha256_of "${tmp}/${asset}")"
if [ "$expected" != "$actual" ]; then
  err "sha256 mismatch for ${asset}: expected ${expected}, got ${actual}. Refusing to install."
fi
ok "sha256 verified: ${expected}"

chmod +x "${tmp}/${asset}"

# ---- pick install dir ------------------------------------------------------

# Honour QSDM_INSTALL_DIR first (lets CI / containers pin exact
# location without prompting). Otherwise try /usr/local/bin as the
# conventional location, falling back to ~/.local/bin which works
# everywhere including single-user macOS installs without sudo.
install_dir="${QSDM_INSTALL_DIR:-}"
sudo_cmd=""
if [ -z "$install_dir" ]; then
  if [ -w "/usr/local/bin" ]; then
    install_dir="/usr/local/bin"
  elif command -v sudo >/dev/null 2>&1 && sudo -n true 2>/dev/null && [ -d "/usr/local/bin" ]; then
    install_dir="/usr/local/bin"
    sudo_cmd="sudo"
  else
    install_dir="${HOME}/.local/bin"
    mkdir -p "$install_dir"
  fi
fi

dest="${install_dir}/${BINARY}"
info "installing to ${dest}"
if [ -n "$sudo_cmd" ]; then
  ${sudo_cmd} install -m 0755 "${tmp}/${asset}" "${dest}"
else
  install -m 0755 "${tmp}/${asset}" "${dest}"
fi
ok "installed ${BINARY} to ${dest}"

# ---- post-install sanity check --------------------------------------------

info "running ${dest} --version"
ver_output="$("${dest}" --version 2>&1)" || err "installed binary failed to execute --version"
printf '    %s\n' "$ver_output"

# Assert the binary is a release build, not a dev/local build that
# somehow ended up in the release bundle.
if printf '%s' "$ver_output" | grep -qE '(^|\s)dev(\s|$)' \
   || printf '%s' "$ver_output" | grep -q 'unknown'; then
  err "installed binary reports dev/unknown metadata — expected a release build. Aborting."
fi
ok "binary identifies as a release build"

# ---- PATH hint -------------------------------------------------------------

case ":${PATH}:" in
  *:"${install_dir}":*) ;;
  *)
    info "note: ${install_dir} is not on your PATH. Add this line to your shell rc:"
    printf '    export PATH="%s:$PATH"\n' "$install_dir"
    ;;
esac

cat <<EOF

Next steps:

  1. Run the setup wizard (creates ~/.qsdm/miner.toml):
       ${BINARY} --setup

  2. Start mining:
       ${BINARY}

  3. See the full quickstart:
       https://github.com/${REPO}/blob/main/QSDM/docs/docs/MINER_QUICKSTART.md

EOF
