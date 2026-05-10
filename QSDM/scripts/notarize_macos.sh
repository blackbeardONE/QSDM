#!/bin/bash
# Notarise + staple a built QSDM macOS binary, then optionally pack it
# into a signed DMG. This is the in-repo scaffolding for the platform-
# packaging step the project's NEXT_STEPS.md flags as external — every
# command path is laid out here so the only thing that's external is the
# operator-supplied Apple Developer ID + App-Specific Password.
#
# Required environment (provided as GitHub Actions secrets at release
# time, never committed):
#
#   APPLE_DEVELOPER_ID_APPLICATION    "Developer ID Application: Foo (ABCD12345)"
#   APPLE_DEVELOPER_ID_INSTALLER      "Developer ID Installer: Foo (ABCD12345)"  (optional, for DMG)
#   APPLE_NOTARYTOOL_KEYCHAIN_PROFILE name of the keychain profile that holds
#                                     your App-Specific Password and team id;
#                                     create once on the runner with
#                                       xcrun notarytool store-credentials \
#                                         "$APPLE_NOTARYTOOL_KEYCHAIN_PROFILE" \
#                                         --apple-id "$APPLE_ID_EMAIL" \
#                                         --password "$APPLE_APP_SPECIFIC_PASSWORD" \
#                                         --team-id  "$APPLE_TEAM_ID"
#
# Required arguments:
#
#   $1  path to the built binary (e.g. ./qsdm)
#   $2  optional output DMG path (e.g. dist/qsdm-darwin-arm64.dmg);
#       when omitted, the script signs + notarises the binary in place
#       and skips DMG packaging.
#
# Exit codes:
#   0  success (signed, notarised, stapled; DMG signed if produced)
#   1  precondition failed (missing tool, missing env, missing input)
#   2  codesign failed
#   3  notarytool submit failed or returned a non-Accepted status
#   4  stapler failed

set -euo pipefail

err() { printf '\033[31mERROR:\033[0m %s\n' "$*" >&2; }
log() { printf '\033[32m[notarize]\033[0m %s\n' "$*"; }

if [[ "$(uname -s)" != "Darwin" ]]; then
    err "this script is macOS only"
    exit 1
fi

BIN="${1:-}"
DMG="${2:-}"

if [[ -z "${BIN}" || ! -f "${BIN}" ]]; then
    err "usage: $0 <binary> [<output.dmg>]"
    exit 1
fi

: "${APPLE_DEVELOPER_ID_APPLICATION:?missing APPLE_DEVELOPER_ID_APPLICATION (Developer ID Application: ...)}"
: "${APPLE_NOTARYTOOL_KEYCHAIN_PROFILE:?missing APPLE_NOTARYTOOL_KEYCHAIN_PROFILE (xcrun notarytool store-credentials profile name)}"

for cmd in codesign xcrun stapler hdiutil; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
        err "$cmd not found on PATH (install Xcode command-line tools)"
        exit 1
    fi
done

# --- 1. codesign the binary with hardened runtime ---------------------------
log "codesign --options runtime ${BIN}"
codesign \
    --force \
    --options runtime \
    --timestamp \
    --sign "${APPLE_DEVELOPER_ID_APPLICATION}" \
    "${BIN}" \
    || { err "codesign failed"; exit 2; }
codesign --verify --strict --verbose=2 "${BIN}"

# --- 2. submit to Apple notarisation ----------------------------------------
# notarytool requires a zip OR DMG; ship a zip for the binary path.
ZIP="${BIN}.zip"
log "ditto -c -k --sequesterRsrc --keepParent ${BIN} ${ZIP}"
rm -f "${ZIP}"
ditto -c -k --sequesterRsrc --keepParent "${BIN}" "${ZIP}"

log "xcrun notarytool submit ${ZIP} (waits for Apple acceptance)"
SUBMIT_LOG="$(mktemp -t qsdm-notarytool)"
xcrun notarytool submit "${ZIP}" \
    --keychain-profile "${APPLE_NOTARYTOOL_KEYCHAIN_PROFILE}" \
    --wait \
    --output-format plist > "${SUBMIT_LOG}" || { err "notarytool submit failed"; cat "${SUBMIT_LOG}"; exit 3; }

# Plist parse: status must be "Accepted".
STATUS="$(plutil -extract status raw "${SUBMIT_LOG}" 2>/dev/null || echo unknown)"
if [[ "${STATUS}" != "Accepted" ]]; then
    err "notarisation status was '${STATUS}', expected 'Accepted'"
    cat "${SUBMIT_LOG}"
    exit 3
fi
log "notarisation Accepted"

# --- 3. staple the ticket to the binary -------------------------------------
log "xcrun stapler staple ${BIN}"
xcrun stapler staple "${BIN}" || { err "stapler failed"; exit 4; }
xcrun stapler validate "${BIN}"

rm -f "${ZIP}"

# --- 4. (optional) build + sign a DMG --------------------------------------
if [[ -n "${DMG}" ]]; then
    log "hdiutil create ${DMG}"
    DMG_DIR="$(mktemp -d -t qsdm-dmg)"
    cp "${BIN}" "${DMG_DIR}/"
    hdiutil create \
        -volname "QSDM" \
        -srcfolder "${DMG_DIR}" \
        -format UDZO \
        -ov \
        "${DMG}"
    rm -rf "${DMG_DIR}"

    log "codesign DMG"
    codesign \
        --force \
        --timestamp \
        --sign "${APPLE_DEVELOPER_ID_APPLICATION}" \
        "${DMG}" \
        || { err "DMG codesign failed"; exit 2; }

    # DMG can also be notarised + stapled.
    log "notarise DMG"
    xcrun notarytool submit "${DMG}" \
        --keychain-profile "${APPLE_NOTARYTOOL_KEYCHAIN_PROFILE}" \
        --wait \
        --output-format plist > "${SUBMIT_LOG}" || { err "DMG notarytool submit failed"; cat "${SUBMIT_LOG}"; exit 3; }
    STATUS="$(plutil -extract status raw "${SUBMIT_LOG}" 2>/dev/null || echo unknown)"
    if [[ "${STATUS}" != "Accepted" ]]; then
        err "DMG notarisation status was '${STATUS}'"
        cat "${SUBMIT_LOG}"
        exit 3
    fi
    xcrun stapler staple "${DMG}" || { err "DMG stapler failed"; exit 4; }
    xcrun stapler validate "${DMG}"
    log "DMG ready: ${DMG}"
fi

rm -f "${SUBMIT_LOG}"
log "done"
