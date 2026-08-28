#!/usr/bin/env bash
#
# find_prs_with_comment.sh
#
# Finds all PRs in a GitHub repo that have a comment (issue comment OR
# review comment) containing a given substring.
#
# Portable version: avoids `mapfile`/`readarray` so it works on macOS's
# default Bash 3.2 as well as Bash 4+/5+.
#
# Requires: gh (GitHub CLI), jq
#
# Usage:
#   ./find_prs_with_comment.sh <owner>/<repo> "<search string>" [state]
#
# Examples:
#   ./find_prs_with_comment.sh myorg/panurus "Token Validation Benchmark"
#   ./find_prs_with_comment.sh myorg/panurus "Token Validation Benchmark" open
#
# state (optional) can be: open | closed | all   (default: all)

set -euo pipefail

REPO="${1:?Usage: $0 <owner>/<repo> \"<search string>\" [state]}"
SEARCH="${2:?Usage: $0 <owner>/<repo> \"<search string>\" [state]}"
STATE="${3:-all}"

if ! command -v gh >/dev/null 2>&1; then
    echo "Error: gh CLI is not installed or not on PATH." >&2
    exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
    echo "Error: jq is required but not installed." >&2
    exit 1
fi

echo "Repo:   $REPO" >&2
echo "State:  $STATE" >&2
echo "Search: $SEARCH" >&2
echo "----------------------------------------" >&2

# 1. Fetch all PR numbers (paginated) matching the requested state.
#    Portable alternative to `mapfile` for Bash 3.2 (macOS default).
PR_NUMBERS=()
while IFS= read -r line; do
    [[ -n "$line" ]] && PR_NUMBERS+=("$line")
done < <(
    gh pr list \
        --repo "$REPO" \
        --state "$STATE" \
        --limit 1000 \
        --json number \
        --jq '.[].number'
)

if [[ ${#PR_NUMBERS[@]} -eq 0 ]]; then
    echo "No PRs found for repo $REPO (state=$STATE)." >&2
    exit 0
fi

echo "Scanning ${#PR_NUMBERS[@]} PR(s)..." >&2
echo >&2

MATCHES=()

for num in "${PR_NUMBERS[@]}"; do
    # a) Issue-style (conversation) comments on the PR
    ISSUE_HIT=$(gh api \
        --paginate \
        "repos/$REPO/issues/${num}/comments" \
        --jq '.[].body' 2>/dev/null | grep -F -- "$SEARCH" || true)

    # b) Inline code review comments on the PR
    REVIEW_HIT=$(gh api \
        --paginate \
        "repos/$REPO/pulls/${num}/comments" \
        --jq '.[].body' 2>/dev/null | grep -F -- "$SEARCH" || true)

    # c) Review summary comments (the top-level comment left with a review)
    REVIEWSUM_HIT=$(gh api \
        --paginate \
        "repos/$REPO/pulls/${num}/reviews" \
        --jq '.[].body' 2>/dev/null | grep -F -- "$SEARCH" || true)

    if [[ -n "$ISSUE_HIT" || -n "$REVIEW_HIT" || -n "$REVIEWSUM_HIT" ]]; then
        URL="https://github.com/${REPO}/pull/${num}"
        TITLE=$(gh pr view "$num" --repo "$REPO" --json title --jq '.title')
        echo "PR #$num: $TITLE"
        echo "  $URL"
        MATCHES+=("$num")
    fi
done

echo >&2
echo "----------------------------------------" >&2
if [[ ${#MATCHES[@]} -eq 0 ]]; then
    echo "No PRs found with a comment containing: \"$SEARCH\"" >&2
else
    echo "Found ${#MATCHES[@]} matching PR(s): ${MATCHES[*]}" >&2
fi
