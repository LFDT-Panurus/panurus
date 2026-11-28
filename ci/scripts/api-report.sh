#!/usr/bin/env bash
#
# Copyright IBM Corp. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#
# Prints a Markdown API compatibility report (apidiff, module mode) for the
# Go modules of this repository, HEAD against a base git ref.
# Report-only: exits 0 unless --fail-on-incompatible is set.
# See docs/development/api-compatibility.md.

set -euo pipefail

usage() {
	cat <<EOF
Usage: $0 [--base REF] [--all] [--fail-on-incompatible] [MODULE_DIR...]

  --base REF               base git ref (default: \$BASE, else merge-base of HEAD and origin/main)
  --all                    report every module, not only the modules changed since the base
  --fail-on-incompatible   exit 1 if any module has unfiltered incompatible changes

MODULE_DIR defaults to \$GO_MODULES, else GO_MODULES from the Makefile.
EOF
}

base="${BASE:-}"
all=0
fail_on_incompatible=0
modules=()
while [ $# -gt 0 ]; do
	case "$1" in
	--base)
		[ $# -ge 2 ] || { usage >&2; exit 2; }
		base="$2"
		shift 2
		;;
	--all) all=1; shift ;;
	--fail-on-incompatible) fail_on_incompatible=1; shift ;;
	-h | --help) usage; exit 0 ;;
	-*) usage >&2; exit 2 ;;
	*) modules+=("$1"); shift ;;
	esac
done

export GOWORK=off

command -v apidiff >/dev/null || { echo "apidiff not found; run 'make install-tools'" >&2; exit 2; }

root="$(git rev-parse --show-toplevel)"
cd "$root"

if [ -z "$base" ]; then
	base_label="merge-base with origin/main"
	base="$(git merge-base HEAD origin/main)" || { echo "cannot compute merge-base with origin/main; pass --base" >&2; exit 2; }
fi
base_label="${base_label:-$base}"
base_sha="$(git rev-parse --verify --quiet "${base}^{commit}")" || { echo "unknown base ref: $base" >&2; exit 2; }
head_sha="$(git rev-parse HEAD)"

if [ ${#modules[@]} -eq 0 ]; then
	if [ -n "${GO_MODULES:-}" ]; then
		read -r -a modules <<<"$GO_MODULES"
	else
		read -r -a modules <<<"$(sed -n 's/^GO_MODULES *:= *//p' Makefile)"
	fi
fi
[ ${#modules[@]} -gt 0 ] || { echo "no modules" >&2; exit 2; }

# Filter from the release-process proposal, section 3.7: mocks, fakes and testutils.
noise_re='(^|/)(mocks?|fakes?|testutils)(\.|/|:|$)'
# Exported Test* helpers of the integration module.
integration_re='\.Test[A-Za-z0-9_]*(:|\.)'

tmp="$(mktemp -d "${TMPDIR:-/tmp}/api-report.XXXXXX")"
base_wt="$tmp/base"
# shellcheck disable=SC2329 # invoked by the EXIT trap
cleanup() {
	git worktree remove --force "$base_wt" >/dev/null 2>&1 || true
	rm -rf "$tmp"
}
trap cleanup EXIT
git worktree add --quiet --detach "$base_wt" "$base_sha"

modpath() { sed -n 's/^module[[:space:]]*//p' "$1" | tr -d '"[:space:]'; }

# Directories of every module at base or HEAD, tools/ included.
read -r -a all_module_dirs <<<"$({ git ls-tree -r --name-only "$base_sha"; git ls-tree -r --name-only "$head_sha"; } |
	sed -n 's|/go\.mod$||p' | sort -u | tr '\n' ' ')"

# changed DIR: true if a .go file or go.mod of the module at DIR differs between base and HEAD.
# Nested modules are excluded.
changed() {
	local dir="$1" m spec=()
	if [ "$dir" = "." ]; then
		spec=(':(glob)**/*.go' 'go.mod')
	else
		spec=(":(glob)$dir/**/*.go" "$dir/go.mod")
	fi
	for m in "${all_module_dirs[@]}"; do
		[ "$m" = "$dir" ] && continue
		if [ "$dir" = "." ] || [[ "$m" == "$dir"/* ]]; then
			spec+=(":(exclude)$m/")
		fi
	done
	! git diff --quiet "$base_sha" "$head_sha" -- "${spec[@]}"
}

summary="$tmp/summary.md"
details="$tmp/details.md"
: >"$summary"
: >"$details"
total_incompatible=0

for dir in "${modules[@]}"; do
	if [ ! -f "$root/$dir/go.mod" ]; then
		echo "| \`$dir\` | - | - | - | not a module at HEAD |" >>"$summary"
		continue
	fi
	if [ ! -f "$base_wt/$dir/go.mod" ]; then
		echo "| \`$dir\` | - | - | - | new module |" >>"$summary"
		continue
	fi
	if [ "$all" -eq 0 ] && ! changed "$dir"; then
		continue
	fi

	name="$(echo "$dir" | tr '/.' '__')"
	head_mod="$(modpath "$root/$dir/go.mod")"
	base_mod="$(modpath "$base_wt/$dir/go.mod")"
	note=""
	if [ "$head_mod" != "$base_mod" ]; then
		note="**MODULE PATH CHANGED**: \`$base_mod\` -> \`$head_mod\`"
	fi

	echo "api-report: $dir" >&2
	err="$tmp/$name.err"
	if ! (cd "$base_wt/$dir" && apidiff -m -w "$tmp/$name.base" "$base_mod") 2>"$err" ||
		! (cd "$root/$dir" && apidiff -m -w "$tmp/$name.head" "$head_mod") 2>>"$err" ||
		! apidiff -m "$tmp/$name.base" "$tmp/$name.head" >"$tmp/$name.out" 2>>"$err"; then
		echo "| \`$dir\` | - | - | - | apidiff failed |" >>"$summary"
		{
			echo "## \`$dir\`"
			echo
			echo "apidiff failed:"
			echo
			echo '```text'
			tail -n 20 "$err"
			echo '```'
			echo
		} >>"$details"
		continue
	fi

	# Split the apidiff output into incompatible and compatible lines.
	awk -v inc="$tmp/$name.inc" -v com="$tmp/$name.com" '
		/^Incompatible changes:/ { out = inc; next }
		/^Compatible changes:/   { out = com; next }
		/^- / && out != ""       { print substr($0, 3) > out }
	' "$tmp/$name.out"
	touch "$tmp/$name.inc" "$tmp/$name.com"

	re="$noise_re"
	if [ "$dir" = "integration" ]; then
		re="($noise_re)|($integration_re)"
	fi
	grep -Ev "$re" "$tmp/$name.inc" >"$tmp/$name.inc.kept" || true
	grep -Ev "$re" "$tmp/$name.com" >"$tmp/$name.com.kept" || true
	cat "$tmp/$name.inc" "$tmp/$name.com" | grep -E "$re" >"$tmp/$name.filtered" || true

	n_inc="$(wc -l <"$tmp/$name.inc.kept" | tr -d ' ')"
	n_com="$(wc -l <"$tmp/$name.com.kept" | tr -d ' ')"
	n_fil="$(wc -l <"$tmp/$name.filtered" | tr -d ' ')"
	total_incompatible=$((total_incompatible + n_inc))
	echo "| \`$dir\` | $n_inc | $n_com | $n_fil | ${note:-} |" >>"$summary"

	{
		echo "## \`$dir\`"
		echo
		[ -n "$note" ] && { echo "$note"; echo; }
		echo "$n_inc incompatible, $n_com compatible, $n_fil filtered."
		echo
		if [ "$n_inc" -gt 0 ]; then
			echo "### Incompatible"
			echo
			echo '```text'
			cat "$tmp/$name.inc.kept"
			echo '```'
			echo
		fi
		if [ "$n_com" -gt 0 ]; then
			echo "### Compatible"
			echo
			echo '```text'
			cat "$tmp/$name.com.kept"
			echo '```'
			echo
		fi
		if [ "$n_fil" -gt 0 ]; then
			echo "<details><summary>Filtered (mocks, fakes, testutils, integration Test* helpers)</summary>"
			echo
			echo '```text'
			cat "$tmp/$name.filtered"
			echo '```'
			echo
			echo "</details>"
			echo
		fi
	} >>"$details"
done

echo "# API compatibility report"
echo
echo "Base \`$(git rev-parse --short "$base_sha")\` ($base_label), head \`$(git rev-parse --short "$head_sha")\`. Advisory only."
echo
if [ -s "$summary" ]; then
	echo "| Module | Incompatible | Compatible | Filtered | Note |"
	echo "|---|---|---|---|---|"
	cat "$summary"
	echo
	cat "$details"
else
	echo "No module changed."
fi

if [ "$fail_on_incompatible" -eq 1 ] && [ "$total_incompatible" -gt 0 ]; then
	exit 1
fi
exit 0
