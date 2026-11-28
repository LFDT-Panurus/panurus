#!/usr/bin/env bash
#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

# Fails if a go.mod file requires a Panurus module at a placeholder version (v0.0.0 or
# v0.0.0-00010101000000-...) or has a toolchain line. A published tag keeps these lines, and
# consumers cannot resolve them. Checks the given go.mod files, or every tracked go.mod file.
# Run from the repository root: `make gomod-requires-check`.

set -euo pipefail

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
for f in "${files[@]}"; do
  if [ ! -f "$f" ]; then
    echo "gomod-requires-check: $f not found" >&2
    fail=1
    continue
  fi
  awk -v f="$f" '
    { line = $0; sub(/\/\/.*/, "", line); n = split(line, a, " ") }
    n == 0 { next }
    a[1] == "toolchain" { printf "%s:%d: toolchain line: %s\n", f, NR, $0; bad = 1; next }
    a[1] == "require" && a[2] == "(" { inreq = 1; next }
    inreq && a[1] == ")" { inreq = 0; next }
    {
      path = ""; ver = ""
      if (inreq) { path = a[1]; ver = a[2] }
      else if (a[1] == "require") { path = a[2]; ver = a[3] }
      if (path ~ /^github\.com\/LFDT-Panurus\/panurus(\/|$)/ &&
          (ver == "v0.0.0" || ver ~ /^v0\.0\.0-00010101000000-/)) {
        printf "%s:%d: placeholder require: %s %s\n", f, NR, path, ver; bad = 1
      }
    }
    END { exit bad }
  ' "$f" >&2 || fail=1
done

if [ "$fail" -ne 0 ]; then
  echo "gomod-requires-check: require the last published release of each Panurus module and keep the local replace; remove toolchain lines. See docs/development/versioning.md." >&2
  exit 1
fi
echo "✓ No placeholder Panurus requires or toolchain lines."
