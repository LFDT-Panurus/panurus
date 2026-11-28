#!/usr/bin/env bash
#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

# Tests ci/scripts/gomod-requires-check.sh offline. Run from the repository root:
# `make gomod-requires-check-test`.

set -uo pipefail

check=./ci/scripts/gomod-requires-check.sh
failures=0
pass() { echo "PASS: $1"; }
fail() { echo "FAIL: $1" >&2; failures=$((failures + 1)); }

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# expect runs the check on a go.mod with the given body. want is pass or fail; msg must appear in
# the output of a failing run.
expect() {
  local want="$1" name="$2" msg="$3" body="$4" out rc
  printf 'module github.com/LFDT-Panurus/panurus/x\n\ngo 1.27.4\n\n%s\n' "$body" >"$tmp/go.mod"
  out="$("$check" "$tmp/go.mod" 2>&1)"
  rc=$?
  if [ "$want" = pass ]; then
    [ "$rc" -eq 0 ] && pass "$name" || fail "$name: rc=$rc out=$out"
  elif [ "$rc" -ne 0 ] && grep -qF -- "$msg" <<<"$out"; then
    pass "$name"
  else
    fail "$name: rc=$rc out=$out"
  fi
}

R=github.com/LFDT-Panurus/panurus
expect pass "accepts published internal requires and local replaces" "" \
  "replace $R => ../
replace (
	$R/integration v0.0.0 => ../integration
)

require (
	$R v0.28.0
	$R/integration v0.27.0 // indirect
)"
expect pass "accepts placeholders of external modules" "" \
  "require github.com/hyperledger-labs/fabric-smart-client/platform/view/services/comm/host/libp2p v0.0.0-00010101000000-000000000000"
expect pass "ignores comments" "" "// require $R v0.0.0
// toolchain go1.27.4"
expect pass "accepts a module whose path starts with the root path" "" \
  "require github.com/LFDT-Panurus/panurus-other v0.0.0"
expect fail "rejects v0.0.0 in a require block" "placeholder require: $R v0.0.0" \
  "require (
	github.com/stretchr/testify v1.12.1
	$R v0.0.0
)"
expect fail "rejects the zero pseudo-version of a submodule" \
  "placeholder require: $R/integration v0.0.0-00010101000000-000000000000" \
  "require $R/integration v0.0.0-00010101000000-000000000000"
expect fail "rejects an indirect placeholder" "placeholder require: $R/x/token/services/network/evm v0.0.0" \
  "require (
	$R/x/token/services/network/evm v0.0.0 // indirect
)"
expect fail "rejects a toolchain line" "toolchain line: toolchain go1.27.4" "toolchain go1.27.4"

# The repository passes, and a missing file fails.
if out="$("$check" 2>&1)"; then
  pass "accepts the repository"
else
  fail "rejects the repository: $out"
fi
if out="$("$check" "$tmp/absent/go.mod" 2>&1)"; then
  fail "accepts a missing file"
elif grep -q 'not found' <<<"$out"; then
  pass "rejects a missing file"
else
  fail "missing file: unexpected output: $out"
fi

if [ "$failures" -ne 0 ]; then
  echo "$failures test(s) failed" >&2
  exit 1
fi
echo "all tests passed"
