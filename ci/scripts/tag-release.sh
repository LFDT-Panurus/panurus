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

if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$ ]]; then
    die "version must match vMAJOR.MINOR.PATCH or vMAJOR.MINOR.PATCH-rc.N (got: $VERSION)"
fi

REPO_ROOT="$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"
cd "$REPO_ROOT"

# Tracked modules that are deliberately not tagged:
#   tools        - development tools, never released.
#   cmd/profiler - not in GO_MODULES until its go.mod is fixed (PR #2706, §3.3).
# A module listed in GO_MODULES is tagged even if it is listed here.
UNTAGGED_MODULES=(tools cmd/profiler)

# The modules to tag are GO_MODULES in the Makefile, in that order.
gomodules_lines="$(sed -nE 's/^GO_MODULES[[:space:]]*:?=[[:space:]]*//p' Makefile)"
[[ -n "$gomodules_lines" ]] || die "no GO_MODULES line in $REPO_ROOT/Makefile"
[[ "$gomodules_lines" != *$'\n'* ]] || die "more than one GO_MODULES line in $REPO_ROOT/Makefile"
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

ERRORS=()
for module in "${MODULES[@]}"; do
    contains "$module" "${TRACKED[@]}" || ERRORS+=("module $module is in GO_MODULES but has no tracked go.mod")
done
for module in "${TRACKED[@]}"; do
    contains "$module" "${MODULES[@]}" && continue
    contains "$module" "${UNTAGGED_MODULES[@]}" && continue
    ERRORS+=("tracked module $module is not in GO_MODULES; add it there or to UNTAGGED_MODULES in $0")
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
    echo "Warning: the tags are not signed; pass --sign (make SIGN=1) to sign them" >&2
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

# Create all tags at HEAD. On failure, delete the tags created so far.
echo "Tagging ${#TAGS[@]} module(s) at $HEAD"
CREATED=()
for i in "${!TAGS[@]}"; do
    tag="${TAGS[$i]}"
    module_path="$(sed -n 's/^module[[:space:]]*//p' "${MODULES[$i]}/go.mod")"
    if [[ -z "$module_path" ]] || ! git tag "$TAG_FLAG" -m "$module_path $VERSION" "$tag" "$HEAD"; then
        echo "Error: failed to create $tag; deleting the tags created so far" >&2
        [[ ${#CREATED[@]} -eq 0 ]] || git tag -d "${CREATED[@]}" >&2
        exit 1
    fi
    CREATED+=("$tag")
    echo "  created $tag"
done

echo "Done. Publish the tags with:"
echo "  $PUSH_CMD"
