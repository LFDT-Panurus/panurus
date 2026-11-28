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
# An inherited repository location would make git act on another repository.
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_COMMON_DIR GIT_OBJECT_DIRECTORY \
  GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_NAMESPACE GIT_PREFIX

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
for v in 0.1.0 v0.1 v0.1.0-beta v0.1.0-rc v0.1.0-rc.x v0.1.0+meta "v0.1.0 x" \
  v01.2.3 v0.01.0 v0.1.00 v0.1.0-rc.01; do
  expect_fail "rejects version '$v'" "version must match" "$v"
done
for v in v2.0.0 v10.1.0-rc.1; do
  expect_fail "rejects major version '$v'" "module paths" --dry "$v"
done
expect_fail "rejects an unknown flag" "unknown flag" --push v0.1.0

# 2. The dry run lists GO_MODULES in order (not tools, cmd/profiler or the nested checkout), prints
# the atomic push command, and creates no tags.
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
out="$(run --dry v1.0.0)"
if grep -qxF "  git push --atomic origin v1.0.0 a/v1.0.0 b/c/v1.0.0" <<<"$out"; then
  pass "accepts major version 1"
else
  fail "major version 1: $out"
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
# set_makefile CONTENT commits CONTENT as the Makefile.
set_makefile() { printf '%b' "$1" >"$repo/Makefile" && git -C "$repo" commit -q -am makefile; }
set_makefile 'GO_MODULES := . a b/c a\n'
expect_fail "fails on a duplicate GO_MODULES entry" "module a is listed more than once" --dry v0.1.0
git -C "$repo" reset -q --hard HEAD~1
set_makefile 'GO_MODULES := . a b/c # modules\n'
expect_fail "fails on a GO_MODULES comment" "must be on one line without comments" --dry v0.1.0
git -C "$repo" reset -q --hard HEAD~1
set_makefile 'GO_MODULES := . a \\\n\tb/c\n'
expect_fail "fails on a continued GO_MODULES line" "must be on one line without comments" --dry v0.1.0
git -C "$repo" reset -q --hard HEAD~1

# 4. Every module path must match its tag prefix; quotes and comments are allowed.
echo "module example.com/elsewhere" >"$repo/a/go.mod"
git -C "$repo" commit -q -am elsewhere
expect_fail "fails on a module path that does not match its directory" \
  "module a declares module path 'example.com/elsewhere', want 'example.com/m/a'" --dry v0.1.0
git -C "$repo" reset -q --hard HEAD~1
echo "module example.com/m/b/c/v2" >"$repo/b/c/go.mod"
git -C "$repo" commit -q -am v2
expect_fail "fails on a /v2 module path" "want 'example.com/m/b/c'" --dry v0.1.0
git -C "$repo" reset -q --hard HEAD~1
printf 'module "example.com/m/a" // the a module\n' >"$repo/a/go.mod"
git -C "$repo" commit -q -am quoted
git -C "$repo" push -q origin main
if run --dry v0.1.0 >/dev/null; then
  pass "accepts a quoted module path with a comment"
else
  fail "a quoted module path with a comment: $(run --dry v0.1.0)"
fi

# 5. HEAD must be on a branch of origin.
git -C "$repo" commit -q --allow-empty -m local
expect_fail "fails when HEAD is not on origin" "is not on any branch of origin" --dry v0.1.0
git -C "$repo" reset -q --hard HEAD~1

# 6. Uncommitted changes fail; untracked files do not.
echo "# dirty" >>"$repo/a/go.mod"
expect_fail "fails on uncommitted changes" "uncommitted changes" --dry v0.1.0
git -C "$repo" checkout -q -- a/go.mod

# 7. A signing failure creates no tags.
out="$(GIT_CONFIG_COUNT=2 GIT_CONFIG_KEY_0=gpg.program GIT_CONFIG_VALUE_0=false \
  GIT_CONFIG_KEY_1=user.signingkey GIT_CONFIG_VALUE_1=none run --sign v0.1.0)"
rc=$?
if [ "$rc" -ne 0 ] && grep -q 'tagging did not complete' <<<"$out" && [ -z "$(tags)" ]; then
  pass "a signing failure leaves no tags"
else
  fail "a signing failure: rc=$rc tags=$(tags) out=$out"
fi
# A closed stdout (SIGPIPE) leaves either every tag or none.
"$repo/ci/scripts/tag-release.sh" v0.1.0 2>/dev/null | head -1 >/dev/null
case "$(tags)" in
  "" | "a/v0.1.0 b/c/v0.1.0 v0.1.0 ") pass "a closed stdout leaves every tag or none" ;;
  *) fail "a closed stdout leaves partial tags: $(tags)" ;;
esac
read -r -a want <<<"$want_tags"
git -C "$repo" tag -d "${want[@]}" >/dev/null 2>&1

# 8. A real run creates annotated tags at HEAD and prints the atomic push command.
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

# 9. Existing tags, local or on origin, fail.
expect_fail "fails on existing local tags" "a/v0.1.0 (local)" v0.1.0
(cd "$repo" && eval "$want_push" -q) || fail "the printed push command fails"
git -C "$repo" tag -d "${want[@]}" >/dev/null
expect_fail "fails on existing tags on origin" "b/c/v0.1.0 (origin)" --dry v0.1.0

# 10. Signing, with SSH keys (if ssh-keygen is available).
if command -v ssh-keygen >/dev/null; then
  ssh-keygen -q -t ed25519 -N '' -f "$tmp/key" >/dev/null
  # The signing program counts its calls; call SIGN_AT fails (SIGN_MODE=fail) or sends SIGTERM to
  # tag-release.sh, the parent of git (SIGN_MODE=term).
  cat >"$tmp/sign" <<'SIGN'
#!/usr/bin/env bash
n=$(($(cat "$SIGN_COUNT" 2>/dev/null || echo 0) + 1))
echo "$n" >"$SIGN_COUNT"
if [ "$n" = "${SIGN_AT:-}" ]; then
  case "$SIGN_MODE" in
    fail) exit 1 ;;
    term) kill -TERM "$(ps -o ppid= -p "$PPID" | tr -d ' ')" ;;
  esac
fi
exec ssh-keygen "$@"
SIGN
  chmod +x "$tmp/sign"
  ssh_run() {
    GIT_CONFIG_COUNT=3 GIT_CONFIG_KEY_0=gpg.format GIT_CONFIG_VALUE_0=ssh \
      GIT_CONFIG_KEY_1=user.signingkey GIT_CONFIG_VALUE_1="$tmp/key" \
      GIT_CONFIG_KEY_2=gpg.ssh.program GIT_CONFIG_VALUE_2="$tmp/sign" SIGN_COUNT="$tmp/count.$1" "${@:2}"
  }
  out="$(ssh_run 1 run --sign v0.2.0)"
  if git -C "$repo" cat-file tag a/v0.2.0 2>/dev/null | grep -q 'BEGIN SSH SIGNATURE' &&
    ! grep -q 'not signed' <<<"$out"; then
    pass "--sign creates signed tags"
  else
    fail "--sign: $out"
  fi
  out="$(ssh_run 2 env GIT_CONFIG_COUNT=4 GIT_CONFIG_KEY_3=tag.gpgSign GIT_CONFIG_VALUE_3=true \
    "$repo/ci/scripts/tag-release.sh" v0.3.0 2>&1)"
  if git -C "$repo" cat-file tag a/v0.3.0 2>/dev/null | grep -q 'BEGIN SSH SIGNATURE' &&
    ! grep -q 'not signed' <<<"$out"; then
    pass "tag.gpgSign=true signs without the unsigned warning"
  else
    fail "tag.gpgSign=true: $out"
  fi
  out="$(ssh_run 3 env SIGN_AT=3 SIGN_MODE=fail "$repo/ci/scripts/tag-release.sh" --sign v0.4.0 2>&1)"
  rc=$?
  if [ "$rc" -ne 0 ] && grep -q 'created a/v0.4.0' <<<"$out" && [ -z "$(git -C "$repo" tag -l '*v0.4.0')" ]; then
    pass "a failure on the last tag deletes the tags created before it"
  else
    fail "a failure on the last tag: rc=$rc tags=$(git -C "$repo" tag -l '*v0.4.0' | tr '\n' ' ') out=$out"
  fi
  out="$(ssh_run 4 env SIGN_AT=2 SIGN_MODE=term "$repo/ci/scripts/tag-release.sh" --sign v0.5.0 2>&1)"
  rc=$?
  if [ "$rc" -eq 143 ] && [ -z "$(git -C "$repo" tag -l '*v0.5.0')" ]; then
    pass "SIGTERM while tagging deletes the tags created so far"
  else
    fail "SIGTERM while tagging: rc=$rc tags=$(git -C "$repo" tag -l '*v0.5.0' | tr '\n' ' ') out=$out"
  fi
else
  echo "SKIP: the signing tests (no ssh-keygen)"
fi

if [ "$failures" -ne 0 ]; then
  echo "$failures test(s) failed" >&2
  exit 1
fi
echo "all tests passed"
