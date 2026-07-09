#!/usr/bin/env bash
set -euo pipefail

usage() {
    echo "Usage: $0 [--dry] <version>"
    echo "  --dry    Print tags that would be created without creating them"
    echo "  version  Git tag version, e.g. v0.13.0"
    exit 1
}

DRY=false
VERSION=""
for arg in "$@"; do
    case "$arg" in
        --dry) DRY=true ;;
        -*) echo "Error: unknown flag $arg" >&2; usage ;;
        *) [[ -n "$VERSION" ]] && usage; VERSION="$arg" ;;
    esac
done
[[ -z "$VERSION" ]] && usage

if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Error: version must match vMAJOR.MINOR.PATCH (got: $VERSION)" >&2
    exit 1
fi

REPO_ROOT="$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"

# Collect all module paths: root first, then submodules excluding tools
MODULES=(".")
while IFS= read -r gomod; do
    dir="${gomod%/go.mod}"
    dir="${dir#./}"
    [[ "$dir" == "tools" ]] && continue
    MODULES+=("$dir")
done < <(find "$REPO_ROOT" -name go.mod -not -path '*/vendor/*' -not -path "$REPO_ROOT/go.mod" | sed "s|$REPO_ROOT/||" | sort)

# Build the list of tags to create
declare -a TAGS
for module in "${MODULES[@]}"; do
    if [[ "$module" == "." ]]; then
        TAGS+=("$VERSION")
    else
        TAGS+=("${module}/${VERSION}")
    fi
done

# Check for conflicts before creating anything
CONFLICTS=()
for tag in "${TAGS[@]}"; do
    if git -C "$REPO_ROOT" tag --list "$tag" | grep -q .; then
        CONFLICTS+=("$tag")
    fi
done

if [[ ${#CONFLICTS[@]} -gt 0 ]]; then
    echo "Warning: the following tags already exist — aborting without creating any tags:" >&2
    for tag in "${CONFLICTS[@]}"; do
        echo "  $tag" >&2
    done
    exit 1
fi

# Create all tags at HEAD
HEAD="$(git -C "$REPO_ROOT" rev-parse HEAD)"

if $DRY; then
    echo "Dry run — would create ${#TAGS[@]} tag(s) at $HEAD:"
    for tag in "${TAGS[@]}"; do
        echo "  $tag"
    done
    exit 0
fi

echo "Tagging ${#TAGS[@]} module(s) at $HEAD"
for tag in "${TAGS[@]}"; do
    git -C "$REPO_ROOT" tag "$tag"
    echo "  created $tag"
done

echo "Done. Run 'git push origin --tags' to publish."
