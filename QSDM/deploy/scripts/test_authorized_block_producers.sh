#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=authorized-block-producers.sh
source "${SCRIPT_DIR}/authorized-block-producers.sh"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_eq() {
  local want="$1"
  local got="$2"
  local label="$3"
  [[ "$want" == "$got" ]] || fail "$label (got: $got; want: $want)"
}

assert_eq 'true' "$(qsdm_normalize_bool TEST_BOOL YES)" 'boolean normalization accepts yes'
assert_eq 'false' "$(qsdm_normalize_bool TEST_BOOL '')" 'boolean normalization defaults false'
if qsdm_normalize_bool TEST_BOOL maybe >/dev/null 2>&1; then
  fail 'boolean normalization accepted an invalid value'
fi

input=' producer-one , producer-two,producer-one , producer-three '
assert_eq '3' "$(qsdm_authorized_block_producer_count "$input")" 'allowlist count de-duplicates values'
expected_toml=$'authorized_block_producers = [\n  "producer-one",\n  "producer-two",\n  "producer-three"\n]'
assert_eq "$expected_toml" "$(qsdm_render_authorized_block_producers_toml "$input")" 'allowlist TOML rendering'
assert_eq 'authorized_block_producers = []' "$(qsdm_render_authorized_block_producers_toml ' , , ' )" 'empty allowlist rendering'
if qsdm_render_authorized_block_producers_toml $'valid\ninvalid' >/dev/null 2>&1; then
  fail 'allowlist renderer accepted a newline-containing ID'
fi
if qsdm_render_authorized_block_producers_toml 'producer/with-slash' >/dev/null 2>&1; then
  fail 'allowlist renderer accepted an unsafe producer ID'
fi

for installer in "${SCRIPT_DIR}/../install-ubuntu-vps.sh" "${SCRIPT_DIR}/../bring-up-validator.sh"; do
  bash -n "$installer"
  grep -Fq 'authorized-block-producers.sh' "$installer" || fail "$(basename "$installer") does not source the shared parser"
  grep -Fq 'QSDM_AUTHORIZED_BLOCK_PRODUCERS' "$installer" || fail "$(basename "$installer") does not accept the allowlist setting"
  grep -Fq 'strict_secrets =' "$installer" || fail "$(basename "$installer") does not persist strict mode"
  grep -Fq 'authorized_block_producers' "$installer" || fail "$(basename "$installer") does not render the allowlist"
done

printf 'authorized block producer installer tests passed\n'
