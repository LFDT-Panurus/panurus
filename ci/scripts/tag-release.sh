#!/usr/bin/env bash
set -euo pipefail

usage() {
    echo "Usage: $0 [--dry] [--sign] <version>"
    echo "  --dry    Run every check and print the tags that would be created, without creating them"
    echo "  --sign   Sign the tags with 'git tag -s' (uses your git signing config)"
    echo "  version  Git tag version, e.g. v0.13.0 or v0.13.0-rc.1"
    exit 1
}

die() { echo "Error: $*" >&2; exit 1; }

DRY=false
SIGN=false
VERSION=""
for arg in "$@"; do
    case "$arg" in
        --dry) DRY=true ;;
        --sign) SIGN=true ;;
        -*) echo "Error: unknown flag $arg" >&2; usage ;;
        *) [[ -n "$VERSION" ]] && usage; VERSION="$arg" ;;
    esac
done
[[ -z "$VERSION" ]] && usage

N='(0|[1-9][0-9]*)'
if [[ ! "$VERSION" =~ ^v$N\.$N\.$N(-rc\.$N)?$ ]]; then
    die "version must match vMAJOR.MINOR.PATCH or vMAJOR.MINOR.PATCH-rc.N, without leading zeros (got: $VERSION)"
fi
if [[ "${BASH_REMATCH[1]}" != 0 && "${BASH_REMATCH[1]}" != 1 ]]; then
    die "major version ${BASH_REMATCH[1]} needs /v${BASH_REMATCH[1]} module paths, which this script does not support (got: $VERSION)"
fi

REPO_ROOT="$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"
cd "$REPO_ROOT"

# Tracked modules that are deliberately not tagged:
#   tools        - development tools, never released.
#   cmd/profiler - not in GO_MODULES until its go.mod is fixed.
# A module listed in GO_MODULES is tagged even if it is listed here.
UNTAGGED_MODULES=(tools cmd/profiler)

# The modules to tag are GO_MODULES in the Makefile, in that order.
gomodules_lines="$(sed -nE 's/^GO_MODULES[[:space:]]*:?=[[:space:]]*//p' Makefile)"
[[ -n "$gomodules_lines" ]] || die "no GO_MODULES line in $REPO_ROOT/Makefile"
[[ "$gomodules_lines" != *$'\n'* ]] || die "more than one GO_MODULES line in $REPO_ROOT/Makefile"
[[ "$gomodules_lines" != *'#'* && "$gomodules_lines" != *\\ ]] ||
    die "GO_MODULES must be on one line without comments in $REPO_ROOT/Makefile"
read -r -a MODULES <<<"$gomodules_lines"
[[ ${#MODULES[@]} -gt 0 ]] || die "GO_MODULES is empty"

# Compare GO_MODULES with the go.mod files tracked by git. Untracked go.mod files,
# for example in nested worktrees, are ignored.
TRACKED=()
while IFS= read -r gomod; do
    [[ "$gomod" == go.mod ]] && TRACKED+=(".") && continue
    [[ "$gomod" == */go.mod ]] && TRACKED+=("${gomod%/go.mod}")
done < <(git ls-files -- '*go.mod')
[[ ${#TRACKED[@]} -gt 0 ]] || die "git tracks no go.mod file"

contains() {
    local needle="$1"
    shift
    local e
    for e in "$@"; do [[ "$e" == "$needle" ]] && return 0; done
    return 1
}

# module_path prints the module path declared in go.mod $1, without quotes or comments.
module_path() {
    awk '$1 == "module" { $1 = ""; print; exit }' "$1" | sed -E 's#//.*$##; s#^[[:space:]]+##; s#[[:space:]]+$##; s#^"(.*)"$#\1#'
}

contains "." "${TRACKED[@]}" || die "no tracked go.mod at the repository root"
ROOT_PATH="$(module_path go.mod)"
[[ -n "$ROOT_PATH" ]] || die "go.mod declares no module path"

ERRORS=()
MODULE_PATHS=()
SEEN=()
for module in "${MODULES[@]}"; do
    if [[ ${#SEEN[@]} -gt 0 ]] && contains "$module" "${SEEN[@]}"; then
        ERRORS+=("module $module is listed more than once in GO_MODULES")
    fi
    SEEN+=("$module")
    if ! contains "$module" "${TRACKED[@]}"; then
        ERRORS+=("module $module is in GO_MODULES but has no tracked go.mod")
        MODULE_PATHS+=("")
        continue
    fi
    want="$ROOT_PATH"
    [[ "$module" == "." ]] || want="$ROOT_PATH/$module"
    got="$(module_path "$module/go.mod")"
    [[ "$got" == "$want" ]] || ERRORS+=("module $module declares module path '$got', want '$want'")
    MODULE_PATHS+=("$got")
done
for module in "${TRACKED[@]}"; do
    contains "$module" "${MODULES[@]}" && continue
    contains "$module" "${UNTAGGED_MODULES[@]}" && continue
    ERRORS+=("tracked module $module is not in GO_MODULES; add it there or to UNTAGGED_MODULES in ci/scripts/tag-release.sh")
done
if [[ ${#ERRORS[@]} -gt 0 ]]; then
    printf 'Error: %s\n' "${ERRORS[@]}" >&2
    exit 1
fi

# Build the list of tags to create
declare -a TAGS
for module in "${MODULES[@]}"; do
    if [[ "$module" == "." ]]; then
        TAGS+=("$VERSION")
    else
        TAGS+=("${module}/${VERSION}")
    fi
done

# Untracked files are allowed: they are not part of the tagged commit.
[[ -z "$(git status --porcelain --untracked-files=no)" ]] || die "the working tree has uncommitted changes; commit or revert them first"

# Only tag a commit that origin already has, so that no unreviewed commit is published with the tags.
git fetch -q origin || die "cannot fetch origin"
[[ -n "$(git branch -r --contains HEAD)" ]] ||
    die "HEAD $(git rev-parse --short HEAD) is not on any branch of origin; push and merge it first"

# Check for conflicts, locally and on origin, before creating anything
REMOTE_TAGS="$(git ls-remote --tags --refs origin | cut -f2)" || die "cannot list the tags on origin"
CONFLICTS=()
for tag in "${TAGS[@]}"; do
    if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
        CONFLICTS+=("$tag (local)")
    fi
    if grep -qxF "refs/tags/$tag" <<<"$REMOTE_TAGS"; then
        CONFLICTS+=("$tag (origin)")
    fi
done

if [[ ${#CONFLICTS[@]} -gt 0 ]]; then
    echo "Error: the following tags already exist — aborting without creating any tags:" >&2
    for tag in "${CONFLICTS[@]}"; do
        echo "  $tag" >&2
    done
    exit 1
fi

HEAD="$(git rev-parse HEAD)"
PUSH_CMD="git push --atomic origin ${TAGS[*]}"

if $SIGN; then
    TAG_FLAG=-s
else
    TAG_FLAG=-a
    # With tag.gpgSign=true, git signs annotated tags anyway.
    if [[ "$(git config --bool tag.gpgSign || true)" != true ]]; then
        echo "Warning: the tags are not signed; pass --sign (make SIGN=1) to sign them" >&2
    fi
fi

if $DRY; then
    echo "Dry run — would create ${#TAGS[@]} annotated tag(s) at $HEAD:"
    for tag in "${TAGS[@]}"; do
        echo "  $tag"
    done
    echo "Then publish them with:"
    echo "  $PUSH_CMD"
    exit 0
fi

# Create all tags at HEAD. If the script stops before all are created (an error or a signal),
# delete every tag of TAGS: none of them existed before.
rollback() {
    trap '' PIPE INT TERM
    local tag
    echo "Error: tagging did not complete; deleting the tags created so far" >&2
    for tag in "${TAGS[@]}"; do
        if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
            git tag -d "$tag" >&2 || true
        fi
    done
}
trap rollback EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 141' PIPE

echo "Tagging ${#TAGS[@]} module(s) at $HEAD"
for i in "${!TAGS[@]}"; do
    git tag "$TAG_FLAG" -m "${MODULE_PATHS[$i]} $VERSION" "${TAGS[$i]}" "$HEAD"
    echo "  created ${TAGS[$i]}"
done
trap - EXIT INT TERM PIPE

echo "Done. Publish the tags with:"
echo "  $PUSH_CMD"
