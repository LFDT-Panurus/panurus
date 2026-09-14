#!/usr/bin/env bash
#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

# Runs `govulncheck ./...` in the current module and fails unless every
# reachable vulnerability it reports is listed in the repo-wide allowlist at
# ci/govulncheck-allowlist.txt. govulncheck itself has no suppression
# mechanism, so this wrapper is how known, unfixable findings (see the
# allowlist file for the reasoning behind each one) are kept from blocking
# every future CI run.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ALLOWLIST="$SCRIPT_DIR/../govulncheck-allowlist.txt"

OUTPUT="$(govulncheck ./... 2>&1)"
STATUS=$?

echo "$OUTPUT"

if [ "$STATUS" -eq 0 ]; then
  exit 0
fi

FOUND_IDS="$(echo "$OUTPUT" | grep -oE '^Vulnerability #[0-9]+: GO-[0-9]{4}-[0-9]+' | grep -oE 'GO-[0-9]{4}-[0-9]+' | sort -u)"

if [ -z "$FOUND_IDS" ]; then
  # govulncheck failed for a reason other than a reachable vulnerability
  # (e.g. a build error) — surface that as-is.
  exit "$STATUS"
fi

ALLOWED_IDS="$(grep -oE '^GO-[0-9]{4}-[0-9]+' "$ALLOWLIST" 2>/dev/null || true)"

UNALLOWED_IDS="$(comm -23 <(echo "$FOUND_IDS") <(echo "$ALLOWED_IDS" | sort -u))"

if [ -n "$UNALLOWED_IDS" ]; then
  echo ""
  echo "govulncheck found reachable vulnerabilities not present in $ALLOWLIST:"
  echo "$UNALLOWED_IDS"
  echo "Fix them, or add a justified entry to the allowlist if no fix exists."
  exit 1
fi

echo ""
echo "All reachable vulnerabilities are allow-listed in $ALLOWLIST; treating as pass."
exit 0
