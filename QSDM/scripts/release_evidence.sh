#!/usr/bin/env bash
# release_evidence.sh
# ===================
# Linux/macOS twin of QSDM/scripts/release_evidence.ps1.
#
# Same artefact layout, same review semantics, same default output
# directory (_tmp_release_evidence_<UTC>/ — caught by the _tmp_*
# .gitignore rule).
#
# Usage:
#   bash QSDM/scripts/release_evidence.sh
#   bash QSDM/scripts/release_evidence.sh --out-dir /path/to/bundle
#   bash QSDM/scripts/release_evidence.sh --quick      # skip slow steps
#
# Exit code:
#   0  bundle written; reviewer flips audit-checklist items next
#   2  hard precondition failed (git / go / node missing)

set -u
set -o pipefail

QUICK=0
OUT_DIR=""

while [ $# -gt 0 ]; do
  case "$1" in
    --out-dir)
      OUT_DIR="$2"; shift 2
      ;;
    --quick)
      QUICK=1; shift
      ;;
    -h|--help)
      sed -n '2,20p' "$0"; exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2; exit 2
      ;;
  esac
done

# Preconditions.
for bin in git go node npm sha256sum; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    echo "ERROR: missing required tool: $bin" >&2
    exit 2
  fi
done

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
SOURCE_DIR="$REPO_ROOT/QSDM/source"
JSSDK_DIR="$SOURCE_DIR/sdk/javascript"

if [ -z "$OUT_DIR" ]; then
  TS="$(date -u +'%Y%m%dT%H%M%SZ')"
  OUT_DIR="$REPO_ROOT/_tmp_release_evidence_${TS}"
fi
mkdir -p "$OUT_DIR"
OUT_DIR="$(cd "$OUT_DIR" && pwd)"
echo "==> evidence bundle dir: $OUT_DIR"

# Each step writes a self-describing header, then the captured
# stdout+stderr, then a footer with the exit code. We never abort
# on inner failure -- the exit code IS the evidence.
capture_step() {
  local file="$1" title="$2"; shift 2
  local path="$OUT_DIR/$file"
  echo "==> $file  ($title)"
  {
    echo "# $title"
    echo "# captured: $(date -u +'%Y-%m-%dT%H:%M:%SZ')"
    echo "# host: $(hostname) / $(uname -srm)"
    printf -- '%.0s-' {1..72}; echo
  } >"$path"
  set +e
  "$@" >>"$path" 2>&1
  local rc=$?
  set -e
  {
    printf -- '%.0s-' {1..72}; echo
    echo "# exit_code: $rc"
  } >>"$path"
}

# 01 - environment fingerprint.
env_step() {
  echo "OS: $(uname -srm)"
  echo "Host: $(hostname)"
  echo "Bash: $BASH_VERSION"
  echo "Captured (UTC): $(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  echo
  echo '--- go ---'
  # Capture LOCAL bootstrap toolchain AND the version Go will auto-fetch
  # per QSDM/source/go.mod. On Go 1.21+ these differ when the bootstrap
  # toolchain is older than the directive; the binaries in 09_binaries.txt
  # are built with the in-module version.
  echo 'local bootstrap toolchain:'
  go version
  echo 'in-module toolchain (the one that builds the binaries):'
  (cd "$SOURCE_DIR" && go version)
  echo
  echo '--- git ---'
  cd "$REPO_ROOT"
  echo "HEAD:   $(git rev-parse HEAD)"
  echo "Branch: $(git rev-parse --abbrev-ref HEAD)"
  echo "Origin: $(git remote get-url origin 2>/dev/null || echo none)"
  echo "Working tree dirty? (file count):"
  git status --porcelain | wc -l
  echo
  echo '--- node ---'
  node --version
  npm --version
}
capture_step '01_environment.txt' 'Build environment fingerprint' env_step

# 02 - audit checklist render.
audit_step() {
  cd "$SOURCE_DIR"
  CGO_ENABLED=0 go run ./cmd/auditreport -format markdown -gate=false -notes=true
}
capture_step '02_audit_report.md' 'cmd/auditreport markdown render' audit_step

# 03 - go mod verify.
mod_step() {
  cd "$SOURCE_DIR"
  CGO_ENABLED=0 go mod verify
}
capture_step '03_go_mod_verify.txt' 'go mod verify (cryptographic)' mod_step

# 04 - govulncheck.
if [ "$QUICK" = "1" ]; then
  echo "# skipped (--quick)" >"$OUT_DIR/04_govulncheck.txt"
else
  vuln_step() {
    cd "$SOURCE_DIR"
    CGO_ENABLED=0 go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
  }
  capture_step '04_govulncheck.txt' 'govulncheck ./... (reachable findings)' vuln_step
fi

# 05 - go vet (default + soak).
vet_step() {
  cd "$SOURCE_DIR"
  echo '--- go vet ./... ---'
  CGO_ENABLED=0 go vet ./...
  local rc1=$?
  echo
  echo '--- go vet -tags soak ./tests/... ---'
  CGO_ENABLED=0 go vet -tags soak ./tests/...
  local rc2=$?
  echo
  echo "# vet default exit: $rc1"
  echo "# vet soak    exit: $rc2"
  # Return success unless BOTH failed.
  [ "$rc1" -eq 0 ] && [ "$rc2" -eq 0 ]
}
capture_step '05_go_vet.txt' 'go vet ./... (default + soak)' vet_step

# 06 - full test suite (skippable).
if [ "$QUICK" = "1" ]; then
  echo "# skipped (--quick)" >"$OUT_DIR/06_go_test_full.txt"
else
  test_step() {
    cd "$SOURCE_DIR"
    CGO_ENABLED=0 QSDM_METRICS_REGISTER_STRICT=1 \
      go test ./... -count=1 -timeout 900s
  }
  capture_step '06_go_test_full.txt' 'go test ./... -count=1 (non-short)' test_step
fi

# 07 - JS SDK tests.
js_step() {
  cd "$JSSDK_DIR"
  node --test qsdm.test.js
}
capture_step '07_jssdk_tests.txt' 'node --test qsdm.test.js' js_step

# 08 - npm pack dry-run.
pack_step() {
  cd "$JSSDK_DIR"
  npm pack --dry-run
}
capture_step '08_npm_pack.txt' 'npm pack --dry-run' pack_step

# 09 - cmd binaries: sha256 + version banner.
bin_step() {
  cd "$SOURCE_DIR"
  local workdir="$OUT_DIR/_binaries_workdir"
  mkdir -p "$workdir"
  local short_sha
  short_sha="$(cd "$REPO_ROOT" && git rev-parse --short HEAD)"
  for cmddir in cmd/*; do
    [ -d "$cmddir" ] || continue
    local cmd
    cmd="$(basename "$cmddir")"
    local outbin="$workdir/$cmd"
    if ! CGO_ENABLED=0 go build -trimpath \
         -ldflags="-s -w -X github.com/blackbeardONE/QSDM/pkg/buildinfo.Version=evidence-bundle -X github.com/blackbeardONE/QSDM/pkg/buildinfo.GitSHA=$short_sha" \
         -o "$outbin" "./$cmddir" 2>/tmp/build_$$_err; then
      echo "== $cmd =="
      echo "  build: FAILED"
      sed 's/^/    /' /tmp/build_$$_err
      rm -f /tmp/build_$$_err
      continue
    fi
    rm -f /tmp/build_$$_err
    local sha
    sha="$(sha256sum "$outbin" | awk '{print $1}')"
    local size
    size="$(stat -c%s "$outbin" 2>/dev/null || stat -f%z "$outbin")"
    local banner
    banner="$("$outbin" --version 2>&1 | head -n 1)"
    [ -z "$banner" ] && banner='(no --version)'
    echo "== $cmd =="
    echo "  sha256: $sha"
    echo "  size:   $size bytes"
    echo "  banner: $banner"
  done
  rm -rf "$workdir"
}
capture_step '09_binaries.txt' 'cmd/* clean builds + sha256 + --version' bin_step

# 10 - soak log scrape (best-effort).
soak_step() {
  local found=0
  for log in "$REPO_ROOT"/_tmp_soak_*; do
    [ -f "$log" ] || continue
    found=1
    local kb
    kb="$(du -k "$log" | awk '{print $1}')"
    echo "== $(basename "$log") (${kb} KB) =="
    tail -n 20 "$log" | sed 's/^/  /'
    echo
  done
  if [ "$found" -eq 0 ]; then
    echo 'no _tmp_soak_*.log files found in repo root'
    echo 'run mempool soak (10 min):'
    echo '  cd QSDM/source && QSDM_SOAK_DURATION=10m go test -tags soak ./tests/ -run TestSoak_Mempool -v'
    echo 'run pubsub soak (10 min, 4 hosts):'
    echo '  cd QSDM/source && QSDM_SOAK_DURATION=10m QSDM_SOAK_HOSTS=4 go test -tags soak ./tests/ -run TestSoak_PubsubMultiHostFanout -v'
  fi
}
capture_step '10_soak_summary.txt' 'most recent soak summaries (best-effort)' soak_step

# 00 - master manifest, written LAST so it hashes every other file.
{
  echo '# QSDM release-evidence bundle'
  echo "# generated: $(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  echo "# git HEAD:  $(cd "$REPO_ROOT" && git rev-parse HEAD)"
  echo "# host:      $(hostname)"
  echo "# go:        $(cd "$SOURCE_DIR" && go version)  (in-module version; matches go.mod directive)"
  echo "# tool:      QSDM/scripts/release_evidence.sh"
  printf -- '%.0s-' {1..72}; echo
  echo 'SHA256                                                            SIZE  FILE'
  for f in "$OUT_DIR"/*; do
    [ -f "$f" ] || continue
    name="$(basename "$f")"
    [ "$name" = '00_MANIFEST.txt' ] && continue
    sha="$(sha256sum "$f" | awk '{print $1}')"
    size="$(stat -c%s "$f" 2>/dev/null || stat -f%z "$f")"
    printf '%s  %9s  %s\n' "$sha" "$size" "$name"
  done
  printf -- '%.0s-' {1..72}; echo
  cat <<'EOF'
# How to review this bundle:
#  1. 02_audit_report.md  - the 81-item security checklist. Flip each
#                            critical/high item to passed/failed/waived
#                            via cmd/auditreport -input <reviewed.json>.
#  2. 03_go_mod_verify    - must end "all modules verified".
#  3. 04_govulncheck      - must report zero reachable findings.
#  4. 06_go_test_full     - last lines must show ok / no FAIL.
#  5. 09_binaries         - every cmd should report go1.25.12+ banner.
#  6. 10_soak_summary     - mempool + pubsub soaks PASS at >= 10 min.
EOF
} >"$OUT_DIR/00_MANIFEST.txt"

echo
echo '==> bundle complete'
echo "    $OUT_DIR"
ls -lh "$OUT_DIR"
