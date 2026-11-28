#!/usr/bin/env bash
#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

# Offline tests of ci/scripts/pr-issue-check.sh. The GitHub API is replaced by a stub that serves
# JSON fixtures. Run from the repository root: `make pr-issue-check-test`.

set -uo pipefail

script=ci/scripts/pr-issue-check.sh
failures=0
pass() { echo "PASS: $1"; }
fail() { echo "FAIL: $1" >&2; failures=$((failures + 1)); }

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# 1. refs parser: body -> expected output (one "<kind> <n>" per line, joined with ';').
check_refs() {
  local name="$1" body="$2" want="$3" got rc
  got="$(printf '%b' "$body" | "$script" refs | tr '\n' ';')"
  rc=$?
  if [ $rc -eq 0 ] && [ "$got" = "$want" ]; then pass "refs: $name"; else fail "refs: $name: rc=$rc got '$got' want '$want'"; fi
}
check_refs "Fixes" 'Fixes #12' 'close 12;'
check_refs "colon" 'fixes: #12' 'close 12;'
check_refs "upper case" 'CLOSES #3' 'close 3;'
check_refs "past tense" 'Resolved #4' 'close 4;'
check_refs "Part of" 'Part of #5' 'part 5;'
check_refs "part  of, two spaces" 'part  of #6' 'part 6;'
check_refs "many with duplicates" 'Fixes #9\nfixes #2\nCloses #9\nPart of #2\nPart of #1' 'close 2;close 9;part 1;part 2;'
check_refs "CRLF" 'Fixes #7\r\nPart of #8\r\n' 'close 7;part 8;'
check_refs "empty body" '' ''
check_refs "no reference" 'just a description\nsee #3' ''
check_refs "prefixes #3" 'prefixes #3' ''
check_refs "cross-repo ref" 'Fixes LFDT-Panurus/panurus#7' ''
check_refs "issue URL" 'Fixes https://github.com/LFDT-Panurus/panurus/issues/7' ''
check_refs "only first of a list" 'Fixes #1 and #2' 'close 1;'
check_refs "leading zeros" 'Fixes #0012' 'close 12;'
check_refs "too long number" 'Fixes #12345678901' ''

# 2. link and priority against fixtures. pr <n> <author> <labels-json> <body>; issue <n> <labels-json>.
fx="$tmp/fx"
mkdir -p "$fx"
cat >"$tmp/gh" <<'EOF'
#!/usr/bin/env bash
echo "$2" >>"$CALLS"
case "$2" in */pulls/*) f="$FX/pull-${2##*/}" ;; */issues/*) f="$FX/issue-${2##*/}" ;; esac
if [ -f "$f" ]; then cat "$f"; else echo '{"message":"Not Found","status":"404"}'; exit 1; fi
EOF
chmod +x "$tmp/gh"
labels_json() { local l out=""; for l in "$@"; do out="$out,{\"name\":\"$l\"}"; done; echo "[${out#,}]"; }
pr() { jq -n --arg u "$2" --argjson l "$3" --arg b "$(printf '%b' "$4")" '{user:{login:$u},labels:$l,body:$b}' >"$fx/pull-$1"; }
issue() { jq -n --argjson l "$2" '{labels:$l}' >"$fx/issue-$1"; }
ispr() { echo '{"labels":[],"pull_request":{}}' >"$fx/issue-$1"; }

issue 10 "$(labels_json priority/P3 bug)"
issue 11 "$(labels_json priority/P1)"
issue 12 "$(labels_json)"
issue 13 "$(labels_json priority/P0 EPIC)"
ispr 14

pr 1 alice "$(labels_json priority/P3)" 'Fixes #10'
pr 2 alice "$(labels_json)" 'no link'
pr 3 'dependabot[bot]' "$(labels_json dependencies)" 'Bumps x'
pr 4 alice "$(labels_json needs-decision)" 'no link'
pr 5 alice "$(labels_json priority/P1)" 'Fixes #10\nCloses #11'
pr 6 alice "$(labels_json priority/P3)" 'Fixes #10\nCloses #11'
pr 7 alice "$(labels_json priority/P3)" 'Fixes #10\nPart of #13'
pr 8 alice "$(labels_json priority/P0)" 'Part of #13'
pr 9 alice "$(labels_json)" 'Fixes #12'
pr 15 alice "$(labels_json)" 'Fixes #14\nFixes #99'
pr 16 alice "$(labels_json priority/P3 priority/P2)" 'Fixes #10'
pr 17 alice "$(labels_json)" 'Fixes #10'
pr 18 alice "$(labels_json priority/P3)" 'fixes: #10'
many() { local i out=""; for ((i = 1; i <= $1; i++)); do out="$out Fixes #$i"; done; echo "$out"; }
pr 19 alice "$(labels_json priority/P3)" "$(many 20)"  # 20 unique refs: at the cap
pr 20 alice "$(labels_json priority/P3)" "$(many 21)"  # 21 unique refs: over the cap

check_mode() {
  local mode="$1" n="$2" want="$3" rc
  : >"$tmp/calls"
  FX="$fx" GH="$tmp/gh" GH_REPO=o/r CALLS="$tmp/calls" "$script" "$mode" "$n" >"$tmp/out" 2>&1
  rc=$?
  if [ $rc -eq "$want" ]; then pass "$mode #$n rc=$rc"; else fail "$mode #$n: rc=$rc want $want: $(cat "$tmp/out")"; fi
}
check_mode link 1 0     # Fixes an issue
check_mode link 2 1     # no link
check_mode link 3 0     # Dependabot exempt
check_mode link 4 0     # needs-decision exempt
check_mode link 8 0     # Part of only
check_mode link 15 1    # refs to a PR and to a missing issue only
check_mode priority 1 0 # same priority
check_mode priority 2 0 # no link: reported by link
check_mode priority 3 0 # Dependabot without link
check_mode priority 4 0 # needs-decision without link
check_mode priority 5 0 # highest of two closing refs
check_mode priority 6 1 # lower than the highest
check_mode priority 7 0 # epic ignored when a closing ref exists
check_mode priority 8 0 # Part of used when there is no closing ref
check_mode priority 9 0 # issue without priority: warning only
check_mode priority 15 0 # no valid issue
check_mode priority 16 1 # two priority labels
check_mode priority 17 1 # PR without priority
check_mode priority 18 0 # colon form
check_mode link 19 0    # 20 references: allowed
check_mode link 20 1    # 21 references: rejected
check_mode priority 20 1 # 21 references: rejected
check_mode link abc 2   # invalid PR number
check_mode bogus 1 2    # invalid mode

# Over the cap, only the PR itself is read: no issue lookups.
check_mode link 20 1
if [ "$(wc -l <"$tmp/calls" | tr -d ' ')" = 1 ]; then pass "cap: no issue lookups"; else fail "cap: $(wc -l <"$tmp/calls") API calls"; fi

if [ "$failures" -ne 0 ]; then
  echo "$failures check(s) failed" >&2
  exit 1
fi
echo "all checks passed"
