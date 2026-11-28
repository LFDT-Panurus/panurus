#!/usr/bin/env bash
#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

# Tests ci/scripts/tag-release.sh offline. It runs a copy of the script in a throwaway repository
# with a bare origin, both in a temporary directory, never in this repository. Run from the
# repository root: `make tag-release-test`.

set -uo pipefail

failures=0
pass() { echo "PASS: $1"; }
fail() { echo "FAIL: $1" >&2; failures=$((failures + 1)); }

script="$(cd "$(dirname "$0")" && pwd)/tag-release.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Isolate git from the caller's configuration (signing, hooks, tag.gpgSign, ...).
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.com
export GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.com
unset GIT_CONFIG_COUNT DRY SIGN MAKEFLAGS MFLAGS MAKELEVEL

repo="$tmp/repo"
git init -q --bare "$tmp/origin.git"
git init -q -b main "$repo"
mkdir -p "$repo/ci/scripts"
cp "$script" "$repo/ci/scripts/tag-release.sh"
printf 'TOP = .\nGO_MODULES := . a b/c\n' >"$repo/Makefile"
for m in . a b/c tools cmd/profiler; do
  mkdir -p "$repo/$m"
  if [ "$m" = . ]; then path=example.com/m; else path="example.com/m/$m"; fi
  echo "module $path" >"$repo/$m/go.mod"
done
git -C "$repo" add -A
git -C "$repo" commit -q -m init
git -C "$repo" remote add origin "$tmp/origin.git"
git -C "$repo" push -q origin main

# A nested, untracked checkout must be ignored.
mkdir -p "$repo/.claude/worktrees/x/z"
echo "module example.com/nested" >"$repo/.claude/worktrees/x/go.mod"
echo "module example.com/nested/z" >"$repo/.claude/worktrees/x/z/go.mod"

run() { "$repo/ci/scripts/tag-release.sh" "$@" 2>&1; }
tags() { git -C "$repo" tag --list | sort | tr '\n' ' '; }
want_tags="v0.1.0 a/v0.1.0 b/c/v0.1.0"
want_push="git push --atomic origin $want_tags"

# expect_fail NAME MESSAGE ARGS... runs the script and expects a failure that prints MESSAGE.
expect_fail() {
  local name="$1" msg="$2" out rc
  shift 2
  out="$(run "$@")"
  rc=$?
  if [ "$rc" -ne 0 ] && grep -qF -- "$msg" <<<"$out"; then
    pass "$name"
  else
    fail "$name: rc=$rc out=$out"
  fi
}

# 1. Bad versions are rejected.
for v in 0.1.0 v0.1 v0.1.0-beta v0.1.0-rc v0.1.0-rc.x v0.1.0+meta "v0.1.0 x"; do
  expect_fail "rejects version '$v'" "version must match" "$v"
done
expect_fail "rejects an unknown flag" "unknown flag" --push v0.1.0

# 2. The dry run lists GO_MODULES in order (not tools, cmd/profiler or the nested checkout), prints the atomic push command, and creates no tags.
out="$(run --dry v0.1.0)"
rc=$?
listed="$(sed -n 's/^  \([^ ]*\)$/\1/p' <<<"$out" | tr '\n' ' ')"
if [ "$rc" -eq 0 ] && [ "$listed" = "$want_tags " ]; then
  pass "dry run lists exactly the GO_MODULES tags"
else
  fail "dry run lists '$listed': rc=$rc out=$out"
fi
if grep -qxF "  $want_push" <<<"$out"; then
  pass "dry run prints the atomic push command"
else
  fail "dry run does not print '$want_push': $out"
fi
if grep -q 'tags are not signed' <<<"$out"; then
  pass "dry run warns that the tags are unsigned"
else
  fail "dry run does not warn about unsigned tags: $out"
fi
if [ -z "$(tags)" ]; then
  pass "dry run creates no tags"
else
  fail "dry run created tags: $(tags)"
fi
out="$(run --dry v0.1.0-rc.1)"
if grep -qxF "  git push --atomic origin v0.1.0-rc.1 a/v0.1.0-rc.1 b/c/v0.1.0-rc.1" <<<"$out"; then
  pass "accepts an rc version"
else
  fail "rc version: $out"
fi

# 3. Module list drift fails.
mkdir -p "$repo/extra" && echo "module example.com/m/extra" >"$repo/extra/go.mod"
git -C "$repo" add extra/go.mod && git -C "$repo" commit -q -m extra
expect_fail "fails on a tracked go.mod that is not in GO_MODULES" \
  "tracked module extra is not in GO_MODULES" --dry v0.1.0
git -C "$repo" reset -q --hard HEAD~1
sed -i.bak 's|^GO_MODULES := .*|GO_MODULES := . a b/c missing|' "$repo/Makefile" && rm "$repo/Makefile.bak"
git -C "$repo" commit -q -am missing
expect_fail "fails on a GO_MODULES entry without go.mod" \
  "module missing is in GO_MODULES but has no tracked go.mod" --dry v0.1.0
git -C "$repo" reset -q --hard HEAD~1

# 4. Uncommitted changes fail; untracked files do not.
echo "# dirty" >>"$repo/a/go.mod"
expect_fail "fails on uncommitted changes" "uncommitted changes" --dry v0.1.0
git -C "$repo" checkout -q -- a/go.mod

# 5. Signing failure creates no tags.
out="$(GIT_CONFIG_COUNT=2 GIT_CONFIG_KEY_0=gpg.program GIT_CONFIG_VALUE_0=false \
  GIT_CONFIG_KEY_1=user.signingkey GIT_CONFIG_VALUE_1=none run --sign v0.1.0)"
rc=$?
if [ "$rc" -ne 0 ] && grep -q 'failed to create v0.1.0' <<<"$out" && [ -z "$(tags)" ]; then
  pass "a signing failure leaves no tags"
else
  fail "a signing failure: rc=$rc tags=$(tags) out=$out"
fi
# A failure on the last tag deletes the tags created before it.
: >"$repo/b/c/go.mod"
git -C "$repo" commit -q -am "no module line"
out="$(run v0.1.0)"
rc=$?
if [ "$rc" -ne 0 ] && grep -q 'failed to create b/c/v0.1.0' <<<"$out" && [ -z "$(tags)" ]; then
  pass "a failure on the last tag deletes the tags created before it"
else
  fail "a failure on the last tag: rc=$rc tags=$(tags) out=$out"
fi
git -C "$repo" reset -q --hard HEAD~1

# 6. A real run creates annotated tags at HEAD and prints the atomic push command.
out="$(run v0.1.0)"
rc=$?
head="$(git -C "$repo" rev-parse HEAD)"
if [ "$rc" -eq 0 ]; then
  pass "tags v0.1.0"
else
  fail "tagging v0.1.0: rc=$rc out=$out"
fi
for t in $want_tags; do
  m="${t%v0.1.0}"
  m="example.com/m${m:+/${m%/}}"
  if [ "$(git -C "$repo" cat-file -t "$t" 2>/dev/null)" = tag ] &&
    [ "$(git -C "$repo" rev-parse "$t^{commit}")" = "$head" ] &&
    [ "$(git -C "$repo" tag -l --format='%(contents:subject)' "$t")" = "$m v0.1.0" ]; then
    pass "$t is annotated at HEAD with message '$m v0.1.0'"
  else
    fail "$t is not an annotated tag at HEAD with message '$m v0.1.0'"
  fi
done
if [ "$(tags)" = "a/v0.1.0 b/c/v0.1.0 v0.1.0 " ]; then
  pass "creates no other tags"
else
  fail "tags: $(tags)"
fi
if grep -qxF "  $want_push" <<<"$out"; then
  pass "prints the atomic push command"
else
  fail "does not print '$want_push': $out"
fi
if [ -z "$(git -C "$repo" ls-remote --tags origin)" ]; then
  pass "does not push"
else
  fail "pushed tags"
fi

# 7. Existing tags, local or on origin, fail.
expect_fail "fails on existing local tags" "a/v0.1.0 (local)" v0.1.0
(cd "$repo" && eval "$want_push" -q) || fail "the printed push command fails"
read -r -a want <<<"$want_tags"
git -C "$repo" tag -d "${want[@]}" >/dev/null
expect_fail "fails on existing tags on origin" "b/c/v0.1.0 (origin)" --dry v0.1.0

# 8. --sign creates signed tags (SSH signing, if ssh-keygen is available).
if command -v ssh-keygen >/dev/null; then
  ssh-keygen -q -t ed25519 -N '' -f "$tmp/key" >/dev/null
  out="$(GIT_CONFIG_COUNT=2 GIT_CONFIG_KEY_0=gpg.format GIT_CONFIG_VALUE_0=ssh \
    GIT_CONFIG_KEY_1=user.signingkey GIT_CONFIG_VALUE_1="$tmp/key" run --sign v0.2.0)"
  if git -C "$repo" cat-file tag a/v0.2.0 2>/dev/null | grep -q 'BEGIN SSH SIGNATURE' &&
    ! grep -q 'not signed' <<<"$out"; then
    pass "--sign creates signed tags"
  else
    fail "--sign: $out"
  fi
else
  echo "SKIP: --sign creates signed tags (no ssh-keygen)"
fi

if [ "$failures" -ne 0 ]; then
  echo "$failures test(s) failed" >&2
  exit 1
fi
echo "all tests passed"
