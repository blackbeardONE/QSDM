#!/usr/bin/env bash
# Launches a packaged QSDM Hive.app on a macOS CI runner, twice:
#   1. With QSDM_HIVE_SMOKE_TEST=1 Hive loads its renderer in the hidden main window, probes it, writes
#      smoke-result.json and quits by itself. The result must say ok, with the renderer root, the main bridge and the
#      core APIs present: the same check as the Linux and Windows workflows.
#   2. A normal start: Hive must still be running 30 seconds later. A screenshot of the screen is kept for the
#      workflow's artifact, then Hive is closed.
# usage: smoke_hive_macos.sh <path to QSDM Hive.app> <work dir>
set -euo pipefail

if [[ $# -lt 2 ]]; then
  echo "usage: $0 <QSDM Hive.app> <work-dir>" >&2
  exit 64
fi
app="$1"
work="$2"
bin="$app/Contents/MacOS/QSDM Hive"
mkdir -p "$work"
test -x "$bin"
unset ELECTRON_RUN_AS_NODE
xattr -dr com.apple.quarantine "$app" 2>/dev/null || true

echo "== 1. renderer smoke test ($(uname -m))"
smoke_root="$work/smoke-appdata"
result="$smoke_root/qsdm-hive/logs/smoke-result.json"
QSDM_HIVE_SMOKE_TEST=1 QSDM_HIVE_APPDATA_ROOT="$smoke_root" "$bin" >"$work/smoke.log" 2>&1 &
pid=$!
# The first start of a freshly signed app on a CI runner can take a while (signature checks, a cold disk cache).
for _ in $(seq 1 240); do
  [[ -s "$result" ]] && break
  kill -0 "$pid" 2>/dev/null || break
  sleep 0.5
done
sleep 1
kill "$pid" 2>/dev/null || true
wait "$pid" 2>/dev/null || true
if [[ ! -s "$result" ]]; then
  echo "No smoke result was written."
  tail -n 60 "$work/smoke.log" || true
  tail -n 40 "$smoke_root/qsdm-hive/logs/startup.log" 2>/dev/null || true
  exit 1
fi
python3 - "$result" <<'EOF'
import json, sys
r = json.load(open(sys.argv[1]))
ren = r.get("renderer") or {}
ok = (r.get("status") == "ok" and ren.get("hasRoot") and (ren.get("rootChildren") or 0) > 0
      and ren.get("hasMainBridge") and ren.get("hasCoreStatusApi") and ren.get("hasBrandingFolderPathApi"))
print(json.dumps(r, indent=2))
if not ok:
    raise SystemExit("the renderer smoke result is not ok")
EOF

echo "== 2. a normal start must stay up"
run_root="$work/run-appdata"
QSDM_HIVE_APPDATA_ROOT="$run_root" "$bin" >"$work/run.log" 2>&1 &
pid=$!
sleep 30
if ! kill -0 "$pid" 2>/dev/null; then
  echo "QSDM Hive exited during a normal start."
  tail -n 80 "$work/run.log" || true
  tail -n 40 "$run_root/qsdm-hive/logs/startup.log" 2>/dev/null || true
  exit 1
fi
screencapture -x "$work/qsdm-hive-$(uname -m).png" || echo "screencapture failed (the runner may not allow screen capture)"
kill "$pid" 2>/dev/null || true
wait "$pid" 2>/dev/null || true
grep -q "browser window created" "$run_root/qsdm-hive/logs/startup.log"
echo "MACOS SMOKE OK ($(uname -m))"
