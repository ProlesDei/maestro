#!/usr/bin/env bash
# review-prep.sh: the fixed steps of a code review, so a session spends no
# calls or tokens on them, and the bot can route a review before it starts.
#
#   review-prep.sh <PR number | maestro/T-<n> branch | T-<n>>
#     Finds the PR, its ticket and the main branch, fetches, makes a
#     detached worktree at the head, writes the diff and its stats to a
#     folder of its own, checks the merge, sorts the changed files and
#     picks full or incremental mode. Prints key=value lines, also saved
#     to $C/prep.txt; wrote= lists the files in $C. A fork's PR, a merged
#     one or one on another base gets no review: it prints stop=<why>
#     and the PR's keys, and fetches and writes nothing.
#   review-prep.sh --route <target>
#     Reads only the PR's file list, writes nothing, and prints
#     route=light when every changed file is docs or inert config that no
#     agent reads as instructions, else route=full.
#
# Run it inside the repo, any worktree. Needs git 2.38+, gh and jq, and
# maestro-ticket for a ticket's verdicts. Exit 2: bad target; 3: no PR or
# no main branch; 1: any other failure.
set -euo pipefail

die() { echo "review-prep: $2" >&2; exit "$1"; }

route=
if [ "${1-}" = --route ]; then route=1; shift; fi
[ $# -eq 1 ] || die 2 "usage: review-prep.sh [--route] <PR number | maestro/T-<n> | T-<n>>"
target=$1

# File classes, first match wins. agent-text, ci, deps and schema are the
# paths the tier-2 row names; docs and config are what tier 0 allows.
class() {
	local p=$1
	case /$p in
	*/AGENTS.md | */CLAUDE.md | */GEMINI.md | */.cursorrules | */.windsurfrules | */.mcp.json | \
		*/skills/* | */prompts/* | */prompt/* | */agents/* | /.claude/* | /.agents/* | /.codex/* | \
		/.cursor/* | /.grok/* | /.gemini/* | /.maestro/* | /.github/copilot-instructions.md | \
		/.github/instructions/* | *.prompt | *.prompt.md | *.mdc)
		echo agent-text ;;
	/.github/* | /.gitlab-ci.yml | /.circleci/* | /.buildkite/* | */Jenkinsfile | /.husky/* | \
		/.githooks/* | */lefthook.yml | */.pre-commit-config.yaml | */Dockerfile* | *.dockerfile | \
		*/docker-compose* | */compose.yml | */compose.yaml | *.tf | *.tfvars | */deploy/* | \
		*/infra/* | */k8s/* | */helm/* | */terraform/* | /.devcontainer/* | */Procfile | \
		*/CODEOWNERS | */fly.toml | */vercel.json | */netlify.toml)
		echo ci ;;
	*/package.json | *-lock.json | */yarn.lock | */pnpm-lock.yaml | */bun.lock | */bun.lockb | \
		*/go.mod | */go.sum | */go.work | */go.work.sum | */Cargo.toml | */requirements*.txt | \
		*/constraints*.txt | */pyproject.toml | */Pipfile | */setup.py | */setup.cfg | \
		*/Gemfile | *.gemspec | */composer.json | */pom.xml | */build.gradle* | \
		*/settings.gradle* | *.csproj | */Directory.Packages.props | */mix.exs | \
		*/Package.swift | */Package.resolved | */Podfile | */pubspec.yaml | */flake.nix | \
		*.lock | *.lockfile)
		echo deps ;;
	*/migrations/* | */migrate/* | */schema/* | */schemas/* | *schema* | *.sql | *.prisma | \
		*.proto | *.graphql | *.avsc)
		echo schema ;;
	*/test/* | */tests/* | */__tests__/* | */spec/* | *_test.* | *.test.* | *.spec.* | */test_*.py)
		echo test ;;
	*.md | *.mdx | *.markdown | *.rst | *.txt | *.adoc | */LICENSE* | */COPYING* | */NOTICE* | \
		*/AUTHORS* | */CHANGELOG*)
		echo docs ;;
	*/.gitignore | */.gitattributes | */.editorconfig | */.mailmap | */.prettierrc* | \
		*/.prettierignore | */.markdownlint* | */.clang-format | */rustfmt.toml | */.rustfmt.toml)
		echo config ;;
	*) echo code ;;
	esac
}

# The PR number, and the ticket its target names.
ticket="" pr=""
case $target in
T-[0-9]* | maestro/T-[0-9]*)
	ticket=${target#maestro/}
	[[ $ticket =~ ^T-[0-9]+$ ]] || die 2 "$target is no ticket id or maestro/T-<n> branch"
	# {owner}: keeps out fork PRs, which anyone can open on any branch name.
	pr=$(gh api "repos/{owner}/{repo}/pulls?head={owner}:maestro/$ticket&state=all&per_page=1" --jq '.[0].number // empty') ||
		die 1 "gh couldn't list the PRs of maestro/$ticket"
	[ -n "$pr" ] || die 3 "no PR from maestro/$ticket"
	;;
'#'[0-9]* | [0-9]*)
	pr=${target#'#'}
	[[ $pr =~ ^[0-9]+$ ]] || die 2 "$target is no PR number"
	;;
*) die 2 "want a PR number, maestro/T-<n> or T-<n>, not $target" ;;
esac

if [ -n "$route" ]; then
	files=$(gh pr view "$pr" --json files --jq '.files[].path') || die 3 "gh found no PR $pr"
	n=$(printf '%s\n' "$files" | grep -c . || true)
	# gh lists at most 100 files; a PR that big is no light review anyway.
	if [ "$n" -eq 0 ] || [ "$n" -ge 100 ]; then
		echo route=full
		echo "why=$n files"
		exit 0
	fi
	while IFS= read -r f; do
		c=$(class "$f")
		case $c in
		docs | config) ;;
		*)
			echo route=full
			echo "why=$f is $c"
			exit 0
			;;
		esac
	done <<<"$files"
	echo route=light
	echo "why=$n files, all docs or inert config"
	exit 0
fi

view=$(gh pr view "$pr" --json number,url,title,body,state,isDraft,headRefName,headRefOid,baseRefName,isCrossRepository) ||
	die 3 "gh found no PR $pr"
H=$(jq -r .headRefOid <<<"$view")
branch=$(jq -r .headRefName <<<"$view")
fork=$(jq -r .isCrossRepository <<<"$view")
[[ $H =~ ^[0-9a-f]{40}$ ]] || die 1 "PR $pr has no head sha"
# Only a branch in this repo named maestro/T-<n> has a ticket.
if [ "$fork" = false ] && [[ $branch =~ ^maestro/(T-[0-9]+)$ ]]; then
	[ -z "$ticket" ] || [ "$ticket" = "${BASH_REMATCH[1]}" ] || die 1 "PR $pr is on $branch, not maestro/$ticket"
	ticket=${BASH_REMATCH[1]}
else
	ticket=
fi

# Main from the main checkout's project-setup.md, never the PR's tree.
mainwt=$(git worktree list --porcelain | sed -n '1s/^worktree //p')
setup=$mainwt/.maestro/project/project-setup.md
[ -f "$setup" ] || die 3 "no $setup to read the main branch from"
main=$(awk '/^## /{on=($0=="## Main branch"); next} on && NF {print $1; exit}' "$setup")
[ -n "$main" ] || die 3 "no branch under ## Main branch in $setup"

state=$(jq -r .state <<<"$view")
base=$(jq -r .baseRefName <<<"$view")
pkeys() {
	echo "ticket=${ticket:-none}"
	echo "pr=$pr"
	echo "url=$(jq -r .url <<<"$view")"
	echo "state=$state"
	echo "draft=$(jq -r .isDraft <<<"$view")"
	echo "fork=$fork"
	echo "branch=$branch"
	echo "base=$base"
	echo "main=$main"
	echo "H=$H"
}
stop=
if [ "$fork" != false ]; then
	stop=fork # never run, or even check out, a fork's code
elif [ "$state" = MERGED ]; then
	stop=merged
elif [ "$base" != "$main" ]; then
	stop="base $base isn't $main"
fi
if [ -n "$stop" ]; then
	pkeys
	echo "stop=$stop"
	exit 0
fi

git fetch -q origin || die 1 "git fetch origin failed"
git cat-file -e "$H^{commit}" 2>/dev/null || git fetch -q origin "pull/$pr/head" ||
	die 1 "can't fetch PR $pr's head $H"
git rev-parse -q --verify "refs/remotes/origin/$main^{commit}" >/dev/null || die 3 "no origin/$main after the fetch"

S=$(date +%s)
C=$(mktemp -d)
R=$(mktemp -d)/review-${ticket:-pr-$pr}-$S
# A failed prep leaves nothing behind for the session to clean up.
cleanup() {
	[ $? -eq 0 ] && return
	git worktree remove --force "$R" 2>/dev/null || true
	rm -rf -- "$C" "${R%/*}"
}
trap cleanup EXIT
git worktree add -q --detach "$R" "$H" || die 1 "git worktree add $R failed"
[ "$(git -C "$R" rev-parse HEAD)" = "$H" ] || die 1 "worktree $R isn't at $H"

B=$(git merge-base "origin/$main" "$H")
jq . <<<"$view" >"$C/pr.json"
git diff "$B" "$H" >"$C/diff.patch"
git diff --stat "$B" "$H" >"$C/stat.txt"
git log --oneline "$B..$H" >"$C/log.txt"
merges=yes
git merge-tree --write-tree "origin/$main" "$H" >/dev/null 2>&1 || {
	[ $? -eq 1 ] || die 1 "git merge-tree failed"
	merges=conflict
}

# files.txt: added, removed, path, class. Binary files count no lines.
lines=0 nfiles=0 tier2=0 light=1
while IFS=$'\t' read -r a d f; do
	[ "$a" = - ] && a=0
	[ "$d" = - ] && d=0
	c=$(class "$f")
	printf '%s\t%s\t%s\t%s\n' "$a" "$d" "$f" "$c" >>"$C/files.txt"
	lines=$((lines + a + d)) nfiles=$((nfiles + 1))
	case $c in agent-text | ci | deps | schema) tier2=$((tier2 + 1)) ;; esac
	case $c in docs | config) ;; *) light= ;; esac
done < <(git diff --numstat --no-renames "$B" "$H")
touch "$C/files.txt"

# K: the sha after the first "at" in review's latest verdict, a Review:
# or a Kickback: that isn't a Kickback: QA fail. On a ticket, from the
# ticket; with none, from PR comments by the repo's owner, members or
# collaborators, since anyone can comment on a public PR. QA reads no diff, so its kickback is never a base.
verdict='startswith("Review:") or (startswith("Kickback:") and (startswith("Kickback: QA fail") | not))'
if [ -n "$ticket" ]; then
	firsts=$(maestro-ticket get "$ticket" --json |
		jq -r '.comments[] | (.first_line // (.body | split("\n") | map(select(test("\\S"))) | .[0] // ""))') ||
		die 1 "maestro-ticket get $ticket failed"
else
	firsts=$(gh api --paginate "repos/{owner}/{repo}/issues/$pr/comments" \
		--jq '.[] | select(.author_association == "OWNER" or .author_association == "MEMBER" or .author_association == "COLLABORATOR") | .body | split("\n") | map(select(test("\\S"))) | .[0] // ""') ||
		die 1 "gh couldn't read PR $pr's comments"
fi
last=$(jq -Rr "select($verdict)" <<<"$firsts" | tail -n 1)
# The word after the first " at " only, and only a full sha.
case $last in *" at "*) K=$(awk '{print $1}' <<<"${last#* at }"); [[ $K =~ ^[0-9a-f]{40}$ ]] || K= ;; *) K= ;; esac

mode=full why="no earlier verdict" T="" dfiles="" dlines=""
if [ -n "$K" ]; then
	if ! git cat-file -e "$K^{commit}" 2>/dev/null || ! git merge-base --is-ancestor "$K" "$H"; then
		why="$K is not in $H's history"
	else
		# The author's change since K, with main's merges taken out.
		if T=$(git merge-tree --write-tree "$K" "$B" | head -n 1) && git diff "$T" "$H" >"$C/delta.patch"; then
			delta=$(git diff --numstat --no-renames "$T" "$H")
		else
			T=
			# A conflict: the files both the author and this PR changed.
			mapfile -t both < <(comm -12 <(git diff --name-only "$K" "$H" | sort) <(git diff --name-only "$B" "$H" | sort))
			: >"$C/delta.patch"
			delta=
			if [ ${#both[@]} -gt 0 ]; then
				git diff "$B" "$H" -- "${both[@]}" >"$C/delta.patch"
				delta=$(git diff --numstat --no-renames "$B" "$H" -- "${both[@]}")
			fi
		fi
		dfiles=$(grep -c . <<<"$delta" || true)
		dlines=$(awk -F'\t' '{a = $1 == "-" ? 0 : $1; d = $2 == "-" ? 0 : $2; n += a + d} END {print n + 0}' <<<"$delta")
		if [ "$dlines" -gt 400 ] || [ "$dfiles" -gt 15 ]; then
			why="delta of $dlines lines in $dfiles files: redone, not fixed"
		else
			mode=incremental why="from review's verdict at $K"
		fi
	fi
fi

{
	pkeys
	echo "stop=none"
	echo "S=$S"
	echo "R=$R"
	echo "C=$C"
	echo "B=$B"
	echo "merges=$merges"
	echo "files=$nfiles"
	echo "lines=$lines"
	echo "tier2_paths=$tier2"
	echo "docs_only=$([ -n "$light" ] && [ "$nfiles" -gt 0 ] && echo yes || echo no)"
	echo "K=${K:-none}"
	echo "T=${T:-none}"
	echo "delta_files=${dfiles:-none}"
	echo "delta_lines=${dlines:-none}"
	echo "mode=$mode"
	echo "mode_why=$why"
	echo "wrote=$C: pr.json diff.patch stat.txt log.txt files.txt$([ -f "$C/delta.patch" ] && echo ' delta.patch') prep.txt"
} | tee "$C/prep.txt"
