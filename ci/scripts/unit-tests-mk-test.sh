#!/usr/bin/env bash
#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

# Tests the targets of unit-tests.mk and the CI workflows that call them. Run from the repository
# root: `make unit-tests-mk-test`.

set -uo pipefail

failures=0
pass() { echo "PASS: $1"; }
fail() { echo "FAIL: $1" >&2; failures=$((failures + 1)); }

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Tests must not depend on the caller's environment. make passes its command-line variables (for
# example RACE=1) to this script through MAKEFLAGS.
unset RACE COVERAGE GORACE MAKEFLAGS MFLAGS MAKELEVEL

# make_db prints the make database for the extra arguments. Its target runs no command under -n.
make_db() { make -s -pn unit-tests-mk-test "$@" 2>/dev/null; }

targets="$(make_db | sed -n 's/^UNIT_TEST_TARGETS := //p')"
if [ -z "$targets" ]; then
  fail "UNIT_TEST_TARGETS is empty"
fi

# Prints the `go test` lines that `make -n` would run for target $1 with the extra arguments.
go_test_lines() {
  local target="$1"
  shift
  make -n "$target" "$@" 2>/dev/null | grep -E '(^|[^[:alnum:]_-])go test '
}

# 1. Without RACE every go test runs without -race; with RACE=1 every go test runs with -race.
for t in $targets unit-tests unit-tests-race; do
  lines="$(go_test_lines "$t")"
  if [ -z "$lines" ]; then
    fail "$t runs no go test"
    continue
  fi
  if [ "$t" = unit-tests-race ]; then
    without="$(grep -v -- ' -race' <<<"$lines")"
    [ -z "$without" ] && pass "$t: every go test uses -race" || fail "$t: go test without -race: $without"
    continue
  fi
  with="$(grep -- ' -race' <<<"$lines")"
  [ -z "$with" ] && pass "$t: no go test uses -race by default" || fail "$t: go test with -race by default: $with"
  without="$(go_test_lines "$t" RACE=1 | grep -v -- ' -race')"
  [ -z "$without" ] && pass "$t RACE=1: every go test uses -race" || fail "$t RACE=1: go test without -race: $without"
done

# 2. RACE=1 exports GORACE=history_size=7; the default does not set GORACE.
# The make database is captured first: grep -q under pipefail would kill make with SIGPIPE.
db_race="$(make_db RACE=1)"
db_default="$(make_db)"
if grep -qx 'GORACE = history_size=7' <<<"$db_race"; then
  pass "RACE=1 sets GORACE=history_size=7"
else
  fail "RACE=1 does not set GORACE=history_size=7"
fi
if grep -q '^GORACE ' <<<"$db_default"; then
  fail "GORACE is set without RACE=1"
else
  pass "GORACE is not set by default"
fi

# 3. COVERAGE=1 writes a profile to COVERAGE_PROFILE for the coverage targets of tests.yml.
for t in unit-tests-main unit-tests-evm; do
  lines="$(go_test_lines "$t" COVERAGE=1 COVERAGE_PROFILE=/x/p.cov)"
  if grep -q -- '-coverpkg=./... -coverprofile=/x/p.cov' <<<"$lines"; then
    pass "$t COVERAGE=1 writes COVERAGE_PROFILE"
  else
    fail "$t COVERAGE=1 does not write COVERAGE_PROFILE"
  fi
done

# 4. unit-tests-cmd tests every cmd/* module.
cmd_modules=" $(make -n unit-tests-cmd 2>/dev/null | sed -nE 's/.*for m in ([^;]*);.*/\1/p') "
for m in cmd/*/go.mod; do
  m="${m%/go.mod}"
  if [[ "$cmd_modules" == *" $m "* ]]; then
    pass "unit-tests-cmd tests $m"
  else
    fail "unit-tests-cmd does not test $m"
  fi
done

# 5. unit-tests-check accepts the repository and rejects every kind of drift.
expect_check() {
  local want="$1" name="$2" msg="$3"
  shift 3
  local out rc
  out="$(make -s unit-tests-check "$@" 2>&1)"
  rc=$?
  if [ "$want" = pass ]; then
    [ "$rc" -eq 0 ] && pass "$name" || fail "$name: $out"
  elif [ "$rc" -ne 0 ] && grep -q -- "$msg" <<<"$out"; then
    pass "$name"
  else
    fail "$name: rc=$rc out=$out"
  fi
}
expect_check pass "unit-tests-check passes on the repository" ""
expect_check fail "unit-tests-check rejects a module without a target" \
  "module x/token/services/network/evm has no target" \
  UNIT_TEST_TARGETS="$(sed 's/unit-tests-evm//' <<<"$targets")"
expect_check fail "unit-tests-check rejects a cmd module that unit-tests-cmd skips" \
  "module cmd/profiler has no target" UNIT_TEST_CMD_MODULES="cmd/tokengen"
expect_check fail "unit-tests-check rejects an exempt module with tests" "exempt module integration has tests" \
  UNIT_TEST_EXEMPT_MODULES="tools integration"
grep -v -- '- tests: unit-tests-cmd' .github/workflows/tests.yml >"$tmp/missing.yml"
expect_check fail "unit-tests-check rejects a workflow without a target" "do not match UNIT_TEST_TARGETS" \
  UNIT_TEST_WORKFLOWS="$tmp/missing.yml"
{ cat .github/workflows/tests.yml; echo '          - tests: unit-tests-unknown'; } >"$tmp/extra.yml"
expect_check fail "unit-tests-check rejects a workflow with an unknown target" "do not match UNIT_TEST_TARGETS" \
  UNIT_TEST_WORKFLOWS="$tmp/extra.yml"
expect_check fail "unit-tests-check rejects a missing workflow" "not found" \
  UNIT_TEST_WORKFLOWS="$tmp/absent.yml"
# Drop the first `docker: true` of the nightly matrix (unit-tests-main).
awk '/^[[:space:]]+docker: true/ && !done {done=1; next} {print}' \
  .github/workflows/nightly-race-unit-tests.yml >"$tmp/nodocker.yml"
expect_check fail "unit-tests-check rejects workflows whose docker flags differ" "docker/foundry flags" \
  UNIT_TEST_WORKFLOWS=".github/workflows/tests.yml $tmp/nodocker.yml"
if grep -qE '^checks-fast:.* unit-tests-check( |$)' <<<"$db_default" &&
  grep -qE '^checks-fast:.* unit-tests-mk-test( |$)' <<<"$db_default"; then
  pass "checks-fast runs unit-tests-check and unit-tests-mk-test"
else
  fail "checks-fast does not run unit-tests-check and unit-tests-mk-test"
fi

# 6. unit-tests-main fails when it finds no regression packages, instead of testing the repository root.
out="$(make -s unit-tests-main UNIT_REGRESSION_RE=no-such-package 2>&1)"
if [ $? -ne 0 ] && grep -q 'no packages or no regression packages found' <<<"$out"; then
  pass "unit-tests-main fails without regression packages"
else
  fail "unit-tests-main does not fail without regression packages: $out"
fi

# selected prints the packages that target $1 passes to go test, one go test call per line.
selected_calls() { make -s "$1" UNIT_GO_TEST=echo 2>/dev/null; }
selected() { selected_calls "$1" | tr ' ' '\n' | grep '^github.com/' | sort; }

# 7. unit-tests-main runs every main-module package once; the regression packages run in a separate
# call without the test timeout.
main_all="$(go list ./... | sort)"
main_selected="$(selected unit-tests-main)"
if [ -n "$main_all" ] && [ "$main_all" = "$main_selected" ]; then
  pass "unit-tests-main runs every main-module package exactly once"
else
  fail "unit-tests-main does not run every main-module package once: $(diff <(echo "$main_all") <(echo "$main_selected"))"
fi
calls="$(selected_calls unit-tests-main)"
regression_call="$(grep -- '-timeout 0' <<<"$calls" | tr ' ' '\n' | grep '^github.com/')"
other_calls="$(grep -v -- '-timeout 0' <<<"$calls" | tr ' ' '\n' | grep '^github.com/')"
if [ -n "$regression_call" ] && ! grep -v regression <<<"$regression_call" | grep -q . &&
  ! grep -q regression <<<"$other_calls"; then
  pass "unit-tests-main runs exactly the regression packages with -timeout 0"
else
  fail "unit-tests-main mixes regression and other packages: $calls"
fi

# 8. unit-tests-integration selects the plain unit-test packages and skips the Ginkgo suites.
pkgs="$(selected unit-tests-integration)"
for p in nwo/token token token/fungible/topology token/fungible/sdk/party token/common/sdk/fdlog; do
  if grep -qx "github.com/LFDT-Panurus/panurus/integration/$p" <<<"$pkgs"; then
    pass "integration unit tests include $p"
  else
    fail "integration unit tests miss $p"
  fi
done
for p in token/fungible/dlog token/fungible/fabtoken token/dvp/dlog token/nft/dlog token/interop/dlog; do
  if grep -qx "github.com/LFDT-Panurus/panurus/integration/$p" <<<"$pkgs"; then
    fail "integration unit tests include the Ginkgo suite $p"
  else
    pass "integration unit tests skip the Ginkgo suite $p"
  fi
done

# 9. The workflows: PRs and pushes run without race, the nightly run with RACE=1 and no coverage upload.
tests_yml=.github/workflows/tests.yml
nightly_yml=.github/workflows/nightly-race-unit-tests.yml
if grep -qE 'make [^#]*RACE=' "$tests_yml"; then
  fail "$tests_yml sets RACE"
else
  pass "$tests_yml runs the unit tests without race"
fi
if grep -q 'run: make ${{ matrix.tests }} RACE=1$' "$nightly_yml"; then
  pass "$nightly_yml runs every target with RACE=1"
else
  fail "$nightly_yml does not run the targets with RACE=1"
fi
if grep -q 'coverallsapp' "$nightly_yml"; then
  fail "$nightly_yml uploads coverage"
else
  pass "$nightly_yml uploads no coverage"
fi
if grep -q "^    - cron: " "$nightly_yml" && grep -q 'github.rest.issues.create(' "$nightly_yml" &&
  grep -q 'github.rest.issues.createComment(' "$nightly_yml" &&
  grep -q "^    if: failure() && github.event_name == 'schedule'$" "$nightly_yml"; then
  pass "$nightly_yml is scheduled and opens or updates an issue on a scheduled failure"
else
  fail "$nightly_yml is not scheduled or opens no issue"
fi
if [ -e .github/workflows/nightly-fsc.yml ]; then
  fail "nightly-fsc.yml still exists"
else
  pass "nightly-fsc.yml is removed"
fi
on="$(sed -n '/^on:$/,/^[a-z]/p' "$tests_yml")"
if grep -qE '^  push:$' <<<"$on" && grep -qE '^  pull_request:$' <<<"$on" &&
  grep -qE '^    branches: \[ main,' <<<"$on"; then
  pass "$tests_yml runs on pushes and pull requests to main"
else
  fail "$tests_yml does not run on pushes and pull requests to main"
fi
agg="$(sed -n '/^  unit-tests:$/,/^  [a-z-]*:$/p' "$tests_yml")"
if grep -q 'needs: utest' <<<"$agg" && grep -q 'if: always()' <<<"$agg" &&
  grep -q 'needs.utest.result }}" = success' <<<"$agg"; then
  pass "$tests_yml has the aggregating unit-tests job"
else
  fail "$tests_yml has no aggregating unit-tests job"
fi
if grep -qE '^[[:space:]]+- check-success=unit-tests$' .mergify.yml; then
  pass "the Mergify queue requires the unit-tests check"
else
  fail "the Mergify queue does not require the unit-tests check"
fi

if [ "$failures" -ne 0 ]; then
  echo "$failures test(s) failed" >&2
  exit 1
fi
echo "all tests passed"
