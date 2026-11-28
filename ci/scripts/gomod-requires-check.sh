#!/usr/bin/env bash
#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

# Checks the requires of Panurus modules (github.com/LFDT-Panurus/panurus and its submodules) in
# the given go.mod files, or in every tracked go.mod file. It fails if a go.mod:
#   - requires a Panurus module at a placeholder version (v0.0.0, v0.0.0-00010101000000-...);
#   - requires a Panurus module below the first release whose go.mod declares the LFDT path (see
#     min_version); older releases declare github.com/hyperledger-labs/fabric-token-sdk/... or a
#     wrong submodule path;
#   - requires a Panurus module that has no unversioned local replace in the same go.mod;
#   - has a toolchain line.
# A tag publishes a go.mod as it is, and consumers cannot resolve these versions.
# Parses with `go mod edit -json` (offline) and jq. Run from the repository root:
# `make gomod-requires-check`.

set -euo pipefail

R=github.com/LFDT-Panurus/panurus

# min_version prints the first release of module $1 whose go.mod declares its LFDT path, found by
# reading `module` in the go.mod of every tag. It prints nothing for a module without such a release.
min_version() {
  case "$1" in
  "$R") echo v0.14.1 ;;
  "$R/integration" | "$R/cmd/artifactgen" | "$R/cmd/skicleanup" | "$R/cmd/tokengen" | \
    "$R/cmd/token_validation_service") echo v0.14.2 ;;
  "$R/cmd/node") echo v0.15.1 ;;
  "$R/cmd/tokendiag" | "$R/x/token/services/network/evm") echo v0.24.0 ;;
  # Tags up to v0.27.0 declare .../token/services/identity/storage/kvs/hashicorp.
  "$R/token/services/storage/db/kvs/hashicorp") echo v0.28.0 ;;
  esac
}

# version_lt succeeds if semantic version $1 is lower than release $2 (vX.Y.Z). A pre-release or
# pseudo-version of vX.Y.Z is lower than vX.Y.Z.
version_lt() {
  local a="${1#v}" b="${2#v}" pre="" i
  case "$a" in *-*) pre=1 ;; esac
  a="${a%%[-+]*}"
  local -a x y
  IFS=. read -r -a x <<<"$a"
  IFS=. read -r -a y <<<"$b"
  for i in 0 1 2; do
    if [ "$((10#${x[i]:-0}))" -lt "$((10#${y[i]:-0}))" ]; then return 0; fi
    if [ "$((10#${x[i]:-0}))" -gt "$((10#${y[i]:-0}))" ]; then return 1; fi
  done
  [ -n "$pre" ]
}

if [ "$#" -eq 0 ]; then
  files=()
  while IFS= read -r f; do files+=("$f"); done < <(git ls-files -- go.mod '*/go.mod')
  if [ "${#files[@]}" -eq 0 ]; then
    echo "gomod-requires-check: no tracked go.mod files" >&2
    exit 1
  fi
else
  files=("$@")
fi

fail=0
err() {
  echo "$1" >&2
  fail=1
}
for f in "${files[@]}"; do
  if [ ! -f "$f" ]; then
    err "$f: not found"
    continue
  fi
  if ! json="$(GOTOOLCHAIN=local GOWORK=off GOFLAGS='' go mod edit -json "$f" 2>&1)"; then
    err "$f: cannot parse: $json"
    continue
  fi
  toolchain="$(jq -r '.Toolchain // empty' <<<"$json")"
  if [ -n "$toolchain" ]; then
    err "$f: toolchain line: $toolchain"
  fi
  replaces=" $(jq -r '.Replace[]? | select(.Old.Version == null and .New.Version == null) | .Old.Path' <<<"$json" | tr '\n' ' ') "
  while IFS=$'\t' read -r path ver; do
    [ -n "$path" ] || continue
    case "$path" in "$R" | "$R"/*) ;; *) continue ;; esac
    if [[ "$replaces" != *" $path "* ]]; then
      err "$f: no unversioned local replace for $path"
    fi
    if [ "$ver" = v0.0.0 ] || [[ "$ver" == v0.0.0-00010101000000-* ]]; then
      err "$f: placeholder require: $path $ver"
      continue
    fi
    if ! [[ "$ver" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-+].*)?$ ]]; then
      err "$f: require $path $ver is not a semantic version"
      continue
    fi
    min="$(min_version "$path")"
    if [ -z "$min" ]; then
      err "$f: $path has no release with its LFDT path; add it to min_version in $0"
    elif version_lt "$ver" "$min"; then
      err "$f: require $path $ver is below $min, the first release with the LFDT path"
    fi
  done < <(jq -r '.Require[]? | [.Path, .Version] | @tsv' <<<"$json")
done

if [ "$fail" -ne 0 ]; then
  echo "gomod-requires-check: require the last published release of each Panurus module, keep its local replace, and remove toolchain lines. See docs/development/versioning.md." >&2
  exit 1
fi
echo "✓ Panurus requires are published releases with local replaces; no toolchain lines."
