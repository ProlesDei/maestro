#!/usr/bin/env bash
# shellcheck disable=SC2254
set -euo pipefail

rebuildable=(node_modules .venv venv target dist build out .next .turbo __pycache__
	.pytest_cache .mypy_cache .ruff_cache .tox coverage htmlcov '*.egg-info' .coverage
	'*.tsbuildinfo' next-env.d.ts)

die() {
	printf 'prune-safe: %s\n' "$2" >&2
	exit "$1"
}
usage='usage: prune-safe.sh [--dry-run] [--main <branch>] [<rebuildable path>...]'
dry='' main=''
while [[ $# -gt 0 ]]; do
	case $1 in
		--dry-run) dry=1 ;;
		--main)
			[[ $# -ge 2 && -n $2 ]] || die 2 "$usage"
			main=$2
			shift
			;;
		-*) die 2 "$usage" ;;
		*) rebuildable+=("$1") ;;
	esac
	shift
done

top=$(git rev-parse --show-toplevel 2>/dev/null) || die 1 'not inside a git worktree'
own=$(cd -- "$top" && pwd -P)
common=$(git rev-parse --path-format=absolute --git-common-dir) || die 1 'no git common dir'

fetch() { GIT_TERMINAL_PROMPT=0 git fetch --quiet --prune --no-prune-tags origin 2>&1; }
err=$(fetch) || { sleep 2 && err=$(fetch); } || die 1 "git fetch origin failed: $(head -n 1 <<<"$err")"

if [[ -z $main ]]; then
	main=$(git symbolic-ref -q --short refs/remotes/origin/HEAD 2>/dev/null || true)
	main=${main#origin/}
	[[ -n $main ]] || main=$(git ls-remote --symref origin HEAD 2>/dev/null |
		sed -n 's@^ref: refs/heads/\(.*\)[[:space:]]HEAD$@\1@p' || true)
	main=${main:-main}
fi
git check-ref-format "refs/heads/$main" || die 2 "bad main branch: $main"
tip=$(git rev-parse -q --verify "refs/remotes/origin/$main^{commit}") ||
	die 1 "no origin/$main after the fetch; pass --main"

removed=0 kept_n=0 gone_n=0
remove_line() {
	if [[ -n $dry ]]; then echo "would_remove=$1 $2"; else echo "removed=$1 $2"; fi
	removed=$((removed + 1))
}
kept() {
	echo "kept=$1 $2"
	kept_n=$((kept_n + 1))
}
gone() {
	echo "gone=$1 $2"
	gone_n=$((gone_n + 1))
}

cwds=''
if command -v lsof >/dev/null 2>&1; then
	cwds=$(lsof -w -d cwd -Fn 2>/dev/null | grep '^n/' || true)
fi
in_use() {
	awk -v p="n$1" '$0 == p || index($0, p "/") == 1 { found = 1; exit } END { exit !found }' <<<"$cwds"
}

is_rebuildable() {
	local path=${1%/} pat
	for pat in "${rebuildable[@]}"; do
		case $path in $pat) return 0 ;; esac
	done
	for pat in "${rebuildable[@]}"; do
		case ${path##*/} in $pat) return 0 ;; esac
	done
	return 1
}

files_label() {
	local entry
	while IFS= read -r -d '' entry; do
		[[ $entry != XX ]] || { echo unverified; return; }
		[[ ${entry:0:3} == '!! ' ]] || { echo dirty; return; }
		is_rebuildable "${entry:3}" || { echo "ignored:${entry:3}"; return; }
	done < <(git -C "$1" --no-optional-locks status --porcelain -z --ignored=matching 2>/dev/null || printf 'XX\0')
}

closed_pushed() {
	git merge-base --is-ancestor "$2" "refs/remotes/origin/$1" 2>/dev/null && command -v gh >/dev/null 2>&1 &&
		[[ $(gh pr list --head "$1" --state all --json state --jq '.[0].state // ""' 2>/dev/null) == CLOSED ]]
}

freed=''
check_worktree() {
	local wt=$1 branch=$2 flags=$3 real head gd label err
	[[ $branch == refs/heads/maestro/* ]] || return 0
	[[ $flags != *' locked'* ]] || { kept "$wt" locked; return; }
	[[ $flags != *' prunable'* ]] || { kept "$wt" prunable; return; }
	real=$(cd -- "$wt" 2>/dev/null && pwd -P) || { kept "$wt" missing; return; }
	[[ $real != "$own" ]] || { kept "$wt" own; return; }
	head=$(git -C "$wt" rev-parse -q --verify 'HEAD^{commit}' 2>/dev/null) || { kept "$wt" unverified; return; }
	git merge-base --is-ancestor "$head" "$tip" || closed_pushed "${branch#refs/heads/}" "$head" || { kept "$wt" unmerged; return; }
	[[ -n $cwds ]] || { kept "$wt" in-use-unknown; return; }
	! in_use "$real" || { kept "$wt" in-use; return; }
	gd=$(git -C "$wt" rev-parse --absolute-git-dir 2>/dev/null) || { kept "$wt" unverified; return; }
	[[ ! -s $gd/maestro/servers ]] || { kept "$wt" servers; return; }
	! compgen -G "$gd/maestro/saved.*" >/dev/null || { kept "$wt" saved; return; }
	[[ -z $(find "$gd/maestro" -maxdepth 1 -name holder -mmin -10080 -print -quit 2>/dev/null) ]] || { kept "$wt" claimed; return; }
	[[ -z $(find "$gd" -maxdepth 1 -type f -mmin -1440 -print -quit 2>/dev/null) ]] || { kept "$wt" recent; return; }
	label=$(files_label "$wt")
	[[ -z $label ]] || { kept "$wt" "$label"; return; }
	if [[ -n $dry ]]; then
		remove_line worktree "$wt"
		freed+="$branch"$'\n'
	elif err=$(git worktree remove "$wt" 2>&1); then
		remove_line worktree "$wt"
	elif ! grep -qxF "worktree $wt" <<<"$(git worktree list --porcelain)"; then
		gone worktree "$wt"
	else
		kept "$wt" "refused: $(head -n 1 <<<"$err")"
	fi
}

first=1 wt='' branch='' flags=''
while IFS= read -r line <&3; do
	case $line in
		'worktree '*) wt=${line#worktree } ;;
		'branch '*) branch=${line#branch } ;;
		locked*) flags+=' locked' ;;
		prunable*) flags+=' prunable' ;;
		'')
			if [[ -n $wt ]]; then
				[[ -n $first ]] || check_worktree "$wt" "$branch" "$flags"
				first=''
			fi
			wt='' branch='' flags=''
			;;
	esac
done 3< <(git worktree list --porcelain && echo)

checked=$(git worktree list --porcelain | sed -n 's/^branch //p')
while read -r ref sha <&3; do
	name=${ref#refs/heads/}
	if grep -qxF "$ref" <<<"$checked" && ! grep -qxF "$ref" <<<"$freed"; then continue; fi
	git merge-base --is-ancestor "$sha" "$tip" || { kept "$name" unmerged; continue; }
	if [[ -n $dry ]]; then
		remove_line branch "$name $sha"
	elif err=$(git -c "branch.$name.remote=." -c "branch.$name.merge=refs/remotes/origin/$main" branch -d "$name" 2>&1); then
		remove_line branch "$name $sha"
	elif ! git show-ref -q --verify "$ref"; then
		gone branch "$name"
	else
		kept "$name" "refused: $(head -n 1 <<<"$err")"
	fi
done 3< <(git for-each-ref --format='%(refname) %(objectname)' refs/heads/maestro/)

ev=$common/maestro/evidence
if [[ -d $ev && ! -L $common/maestro && ! -L $ev ]] && compgen -G "$ev/pr-*" >/dev/null; then
	if ! command -v gh >/dev/null 2>&1; then
		kept "$ev" gh-missing
	else
		for dir in "$ev"/pr-*; do
			n=${dir##*/pr-}
			[[ $n =~ ^[1-9][0-9]*$ && -d $dir && ! -L $dir ]] || { kept "$dir" not-a-pr-folder; continue; }
			state=$(gh pr view "$n" --json state -q .state 2>/dev/null) || { kept "$dir" unverified; continue; }
			case $state in
				MERGED | CLOSED) ;;
				*) kept "$dir" "pr-$state"; continue ;;
			esac
			recent=$(find "$dir" -mmin -10080 -print -quit 2>/dev/null) || { kept "$dir" unverified; continue; }
			[[ -z $recent ]] || { kept "$dir" recent; continue; }
			if [[ -n $dry ]]; then
				remove_line evidence "$dir"
			elif rm -r -- "$dir" 2>/dev/null; then
				remove_line evidence "$dir"
			elif [[ ! -e $dir ]]; then
				gone evidence "$dir"
			else
				kept "$dir" refused
			fi
		done
	fi
fi

if [[ -n $dry ]]; then
	echo "summary=$removed would go, $kept_n kept, $gone_n gone, main origin/$main"
else
	echo "summary=$removed removed, $kept_n kept, $gone_n gone, main origin/$main"
fi
