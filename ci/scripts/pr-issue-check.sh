#!/usr/bin/env bash
#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

# Checks the PR metadata rules of docs/development/general.md#automation.
#
#   pr-issue-check.sh refs < body    print one "<close|part> <n>" line per issue reference
#   pr-issue-check.sh link <pr>      fail if the PR links no issue of this repository
#   pr-issue-check.sh priority <pr>  fail if the PR priority/* label differs from its issue's
#
# Env: GH_REPO (default LFDT-Panurus/panurus), GH (gh command, default gh). Needs jq.

set -euo pipefail

repo="${GH_REPO:-LFDT-Panurus/panurus}"
gh_cmd="${GH:-gh}"
# Each reference costs one API call; the PR author controls the body. Cap it to protect the
# repository-wide GITHUB_TOKEN rate limit.
max_refs=20

usage() {
  echo "usage: $0 refs | link <pr> | priority <pr>" >&2
  exit 2
}

# parse_refs reads a PR body on stdin. It prints "close <n>" for Fixes/Closes/Resolves #n (GitHub
# closing keywords) and "part <n>" for Part of #n, case-insensitive, sorted and unique. No reference
# is not an error.
parse_refs() {
  { tr -d '\r' | grep -ioE '(^|[^[:alnum:]_])(close[sd]?|fix(e[sd])?|resolve[sd]?|part[[:space:]]+of)[[:space:]]*:?[[:space:]]*#[0-9]+' || true; } |
    awk '{ n = $0; sub(/.*#/, "", n); if (length(n) > 9) next
           k = (tolower($0) ~ /part[[:space:]]+of/) ? "part" : "close"; print k, n + 0 }' |
    sort -u -k1,1 -k2,2n
}

# issue_info prints "issue <P|->" (best priority/Pn label, - if none), "pr", or "missing" for #$1.
issue_info() {
  local out
  if ! out="$("$gh_cmd" api "repos/$repo/issues/$1" 2>/dev/null)"; then
    if jq -e '.status == "404" or .status == "410"' >/dev/null 2>&1 <<<"$out"; then
      echo missing
      return 0
    fi
    echo "::error::cannot read issue #$1 of $repo" >&2
    return 1
  fi
  jq -r 'if .pull_request != null then "pr" else
           "issue " + ([.labels[].name | select(test("^priority/P[0-9]$")) | ltrimstr("priority/")]
                       | sort | first // "-") end' <<<"$out"
}

mode="${1:-}"
case "$mode" in
  refs)
    [ $# -eq 1 ] || usage
    parse_refs
    exit 0
    ;;
  link | priority)
    [ $# -eq 2 ] || usage
    pr="$2"
    [[ "$pr" =~ ^[0-9]+$ ]] || usage
    ;;
  *) usage ;;
esac

pr_json="$("$gh_cmd" api "repos/$repo/pulls/$pr")"
author="$(jq -r '.user.login' <<<"$pr_json")"
labels="$(jq -r '.labels[].name' <<<"$pr_json")"
refs="$(jq -r '.body // ""' <<<"$pr_json" | parse_refs)"

if [ "$mode" = link ] && { grep -qx needs-decision <<<"$labels" || [ "$author" = "dependabot[bot]" ]; }; then
  echo "PR #$pr is exempt (needs-decision label or Dependabot author)"
  exit 0
fi

nrefs="$(grep -c . <<<"$refs" || true)"
if [ "$nrefs" -gt "$max_refs" ]; then
  echo "::error::PR #$pr body has $nrefs issue references; at most $max_refs are allowed. Remove some."
  exit 1
fi

# Resolve every reference once. Lines: "<close|part> <n> <P|->" for valid issues of this repository.
valid=""
while read -r kind n; do
  [ -n "$n" ] || continue
  info="$(issue_info "$n")"
  case "$info" in
    missing) echo "::warning::#$n does not exist in $repo; ignored" ;;
    pr) echo "::warning::#$n is a pull request, not an issue; ignored" ;;
    *) valid="${valid}${kind} ${n} ${info#issue }"$'\n' ;;
  esac
done <<<"$refs"

if [ "$mode" = link ]; then
  if [ -z "$valid" ]; then
    echo "::error::PR #$pr links no issue. Add 'Fixes #N' (or Closes/Resolves), or 'Part of #N' for partial work, to the PR body."
    exit 1
  fi
  echo "PR #$pr links: $(awk 'NF { printf "%s#%s ", ($1 == "part" ? "part of " : ""), $2 }' <<<"$valid")"
  exit 0
fi

# priority: closing refs decide; Part of refs (often epics) only when there is no closing ref.
set_lines="$(awk '$1 == "close"' <<<"$valid")"
[ -n "$set_lines" ] || set_lines="$(awk '$1 == "part"' <<<"$valid")"
if [ -z "$set_lines" ]; then
  echo "PR #$pr links no issue; nothing to compare (see pr-linked-issue)"
  exit 0
fi
listing="$(awk '{ printf "#%s=%s ", $2, ($3 == "-" ? "none" : "priority/" $3) }' <<<"$set_lines")"
expected="$(awk '$3 != "-" { print $3 }' <<<"$set_lines" | sort | head -n 1)"
if [ -z "$expected" ]; then
  echo "::warning::no linked issue has a priority/* label ($listing); the issue needs triage"
  exit 0
fi
pr_prio="$(grep -E '^priority/' <<<"$labels" || true)"
count="$(grep -c . <<<"$pr_prio" || true)"
if [ "$count" -gt 1 ]; then
  echo "::error::PR #$pr has more than one priority label: $(tr '\n' ' ' <<<"$pr_prio")"
  exit 1
fi
if [ "$pr_prio" != "priority/$expected" ]; then
  echo "::error::PR #$pr must carry priority/$expected (highest of linked issues: $listing), has: ${pr_prio:-none}. A maintainer or triager sets it; the check re-runs on 'labeled'."
  exit 1
fi
echo "PR #$pr carries priority/$expected (linked issues: $listing)"
