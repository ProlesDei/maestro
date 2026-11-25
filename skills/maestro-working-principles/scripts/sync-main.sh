#!/usr/bin/env bash
set -eu

die() {
	printf 'sync-main: %s\n' "$2" >&2
	exit "$1"
}

if ! { [[ $# == 1 ]] && git check-ref-format "refs/heads/$1"; }; then die 2 'usage: sync-main.sh <main>'; fi
main=$1
top=$(git rev-parse --show-toplevel) || die 1 not_a_worktree
cd "$top"
git_dir=$(git rev-parse --absolute-git-dir)
for op in MERGE_HEAD:merge rebase-merge:rebase rebase-apply:rebase CHERRY_PICK_HEAD:cherry-pick REVERT_HEAD:revert; do
	[[ ! -e $git_dir/${op%%:*} ]] || die 3 "${op#*:} in progress: finish or abort it first"
done
[[ -z $(git status --porcelain) ]] || die 3 'uncommitted changes: commit them first (never stash)'

GIT_TERMINAL_PROMPT=0 git fetch --quiet --no-tags --refmap= origin "refs/heads/$main" || die 1 fetch_failed
sha=$(git rev-parse --verify 'FETCH_HEAD^{commit}')
printf 'main=%s\n' "$sha"
if git merge-base --is-ancestor "$sha" HEAD; then
	echo result=up_to_date
	exit 0
fi

saved=
while IFS= read -r -d '' path; do
	in_way=$path
	until [[ -L $in_way || -e $in_way && ($in_way == "$path" || ! -d $in_way) ]]; do
		[[ $in_way == */* ]] || continue 2
		in_way=${in_way%/*}
	done
	[[ -z $(git ls-files -- "$in_way") ]] || continue
	[[ -n $saved ]] || saved=$(mktemp -d "${TMPDIR:-/tmp}/maestro-saved.XXXXXXXX")
	mkdir -p -- "$saved/$(dirname -- "$in_way")"
	mv -- "$in_way" "$saved/$in_way"
	printf 'saved=%s\n' "$in_way"
done < <(git diff -z --name-only --no-renames --diff-filter=A HEAD "$sha" --)
[[ -z $saved ]] || printf 'saved_dir=%s\n' "$saved"

if git merge --quiet --ff --no-edit -m "Merge origin/$main" "$sha" >/dev/null; then
	echo result=merged
	exit 0
fi
[[ -e $git_dir/MERGE_HEAD ]] || die 1 merge_failed
echo result=conflict
git diff --name-only --diff-filter=U | sed 's/^/conflict=/'
exit 4
