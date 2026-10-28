#!/usr/bin/env bash
#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#
# Prints the Go packages of the zkatsnark module that belong to a unit-test shard, one per line.
# Run from x/token/core/zkatsnark. The shards let CI run the (slow, under -race) unit tests of the
# module in parallel jobs. "rest" is everything no other shard claims, so a new package is always
# tested somewhere. The regression package is run by `make unit-tests-regression`, not here.

set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "Usage: $0 <validator|setup|crypto|prover|rest|all>" >&2
  exit 1
fi

SHARD="$1"

# Anchored regexes of the packages each named shard claims.
VALIDATOR='/nogh/v1/validator$'
SETUP='/nogh/v1/setup$'
CRYPTO='/nogh/v1/crypto(/|$)'
PROVER='/nogh/v1/prover$'

all() {
  local out
  if [ "$(go list -m 2>/dev/null)" != "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark" ]; then
    echo "run this script from x/token/core/zkatsnark" >&2
    exit 1
  fi
  out="$(go list ./... | grep -v 'regression' | grep -v 'protos-go' || true)"
  if [ -z "$out" ]; then
    echo "no packages found: run this script from x/token/core/zkatsnark" >&2
    exit 1
  fi
  echo "$out"
}

# select prints the packages of "all" matching (or, with -v, not matching) the given regex, and fails if
# there are none: an empty list would make `go test` silently run on the current directory instead.
select_pkgs() {
  local out
  out="$(all | grep "$@" || true)"
  if [ -z "$out" ]; then
    echo "shard '$SHARD' is empty" >&2
    exit 1
  fi
  echo "$out"
}

case "$SHARD" in
  all)       all ;;
  validator) select_pkgs -E "$VALIDATOR" ;;
  setup)     select_pkgs -E "$SETUP" ;;
  crypto)    select_pkgs -E "$CRYPTO" ;;
  prover)    select_pkgs -E "$PROVER" ;;
  rest)      select_pkgs -vE "$VALIDATOR|$SETUP|$CRYPTO|$PROVER" ;;
  *)
    echo "unknown shard '$SHARD'" >&2
    exit 1
    ;;
esac
