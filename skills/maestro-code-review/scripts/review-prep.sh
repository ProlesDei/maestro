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

# 4.4: mapfile, and "${a[@]}" of an empty array under set -u.
((BASH_VERSINFO[0] * 100 + BASH_VERSINFO[1] >= 404)) || die 1 "needs bash 4.4 or later (macOS: brew install bash)"
# Paths as written, not quoted. Attributes from an empty tree and no
# global attributes file, so a -diff in the PR's or the checkout's
# .gitattributes, or the user's, hides no code from the counts or the
# patch (the tree part needs git 2.40+). Only .git/info/attributes, the
# repo's own, still applies: binary= reports what it, or a real binary,
# leaves uncounted.
attrs=()
empty=$(git hash-object -t tree /dev/null)
git --attr-source="$empty" rev-parse --git-dir >/dev/null 2>&1 && attrs=(--attr-source="$empty")
gd() { git -c core.quotePath=false -c core.attributesFile=/dev/null "${attrs[@]}" diff "$@"; }

# The main branch, from the main checkout's project-setup.md, never the
# PR's tree.
mainwt=$(git worktree list --porcelain | sed -n '1s/^worktree //p')
setup=$mainwt/.maestro/project/project-setup.md
main=
[ -f "$setup" ] && main=$(awk '/^## /{on=($0=="## Main branch"); next} on && NF {print $1; exit}' "$setup")

# The root files agents read as rules, as main has them since the last
# fetch; the main checkout's copy only where main has none.
rules=
load_rules() {
	local f
	rules=
	for f in AGENTS.md AGENTS.override.md CLAUDE.md CLAUDE.local.md .claude/CLAUDE.md GEMINI.md \
		.github/copilot-instructions.md .maestro/project/project-setup.md; do
		if [ -n "$main" ] && git cat-file -e "refs/remotes/origin/$main:$f" 2>/dev/null; then
			rules+=$(git show "refs/remotes/origin/$main:$f")$'\n'
		elif [ -f "$mainwt/$f" ]; then
			rules+=$(cat "$mainwt/$f")$'\n'
		fi
	done
}
# named: the rules name this path, or a folder it sits in (`docs/x/`,
# ./docs/x/ or `docs/x`), as words of their own, so agents may obey it.
# The quoted part of a bash regex matches as written.
named() {
	local d=$1
	[[ $rules =~ (^|[^[:alnum:]_.-])"$d"([^[:alnum:]_/-]|$) ]] && return 0
	while [[ $d == */* ]]; do
		d=${d%/*}
		[[ $rules =~ (^|[^[:alnum:]_-])"$d"/([^[:alnum:]_.-]|$) || $rules == *"\`$d\`"* ]] && return 0
	done
	return 1
}

# File classes, first match wins. agent-text, ci, deps and schema are the
# paths the tier-2 row names. docs and config are the only ones tier 0
# and the light route take, so they are a short list of exact names: a
# doc at the root, or a .md, .rst or .adoc in a root docs/ folder and no
# dot folder, that the rules don't name, nor a folder it sits in. Other
# prose is text, for the session to judge. Instruction files match in any case:
# a case-insensitive disk loads claude.md as CLAUDE.md.
class() {
	local p=$1 l=${1,,}
	case /$l in
	*/agents.md | */agent.md | */agents.override.md | */claude.md | */claude.local.md | \
		*/gemini.md | */conventions.md | */.cursorrules | */.windsurfrules | */.clinerules | \
		*/.clinerules/* | */.mcp.json | */skills/* | */prompts/* | */prompt/* | */agents/* | \
		/.claude/* | /.agents/* | /.codex/* | /.cursor/* | /.grok/* | /.gemini/* | /.maestro/* | \
		/.windsurf/* | /.kiro/* | /.junie/* | /.roo/* | /.continue/* | /.amazonq/* | /.aider* | \
		/.github/copilot-instructions.md | /.github/instructions/* | /.github/chatmodes/* | \
		*.prompt | *.prompt.md | *.mdc)
		echo agent-text
		return
		;;
	esac
	case /$p in
	/.github/* | /.gitlab-ci.yml | /.circleci/* | /.buildkite/* | */Jenkinsfile | /.husky/* | \
		/.githooks/* | */lefthook.yml | */.pre-commit-config.yaml | */Dockerfile* | *.dockerfile | \
		*/docker-compose* | */compose.yml | */compose.yaml | *.tf | *.tfvars | */deploy/* | \
		*/infra/* | */k8s/* | */helm/* | */terraform/* | /.devcontainer/* | */Procfile | \
		*/CODEOWNERS | */fly.toml | */vercel.json | */netlify.toml | */.gitattributes | /.changeset/*)
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
	/README | /README.md | /README.rst | /README.txt | /CHANGELOG | /CHANGELOG.md | /CHANGES.md | \
		/HISTORY.md | /LICENSE | /LICENSE.md | /LICENSE.txt | /LICENCE | /LICENCE.md | /COPYING | \
		/COPYING.md | /NOTICE | /NOTICE.md | /NOTICE.txt | /AUTHORS | /AUTHORS.md | \
		/CONTRIBUTING.md | /CODE_OF_CONDUCT.md | /SECURITY.md | /docs/*.md | /docs/*.rst | \
		/docs/*.adoc | /doc/*.md | /doc/*.rst | /doc/*.adoc)
		if named "$p"; then
			echo agent-text
		elif [[ /$p == */.* ]]; then
			echo text # in a dot folder, where a harness may look
		else
			echo docs
		fi
		;;
	*.md | *.markdown | *.rst | *.adoc | *.txt | *.MD)
		echo text ;;
	*/.gitignore | */.editorconfig | */.prettierignore | */.markdownlintignore | */.mailmap)
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
	full() { echo route=full; echo "why=$1"; exit 0; }
	# With no main or no rules to read, nothing says which docs agents obey.
	[ -n "$main" ] || full "no ## Main branch in the main checkout's project-setup.md"
	load_rules
	[ -n "$rules" ] || full "no AGENTS.md, CLAUDE.md or project-setup.md to read"
	files=$(gh pr view "$pr" --json files --jq '.files[] | "\(.changeType)\t\(.path)"') || die 3 "gh found no PR $pr"
	n=$(printf '%s\n' "$files" | grep -c . || true)
	# gh lists at most 100 files; a PR that big is no light review anyway.
	[ "$n" -gt 0 ] && [ "$n" -lt 100 ] || full "$n files"
	while IFS=$'\t' read -r t f; do
		# A rename lists only its new path, so what it moved stays unseen.
		case $t in ADDED | MODIFIED | DELETED) ;; *) full "$f is ${t,,}" ;; esac
		c=$(class "$f")
		case $c in docs | config) ;; *) full "$f is $c" ;; esac
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

[ -f "$setup" ] || die 3 "no $setup to read the main branch from"
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

# Once more after a pause of 1 to 5 seconds: many sessions fetching in
# one checkout can collide on a ref lock, and must not retry together.
git fetch -q origin || { sleep $((RANDOM % 5 + 1)) && git fetch -q origin; } || die 1 "git fetch origin failed"
git cat-file -e "$H^{commit}" 2>/dev/null || git fetch -q origin "pull/$pr/head" ||
	{ sleep $((RANDOM % 5 + 1)) && git fetch -q origin "pull/$pr/head"; } ||
	die 1 "can't fetch PR $pr's head $H"
git rev-parse -q --verify "refs/remotes/origin/$main^{commit}" >/dev/null || die 3 "no origin/$main after the fetch"
load_rules

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
gd "$B" "$H" >"$C/diff.patch"
gd --stat "$B" "$H" >"$C/stat.txt"
git log --oneline "$B..$H" >"$C/log.txt"
merges=yes
git merge-tree --write-tree "origin/$main" "$H" >/dev/null 2>&1 || {
	[ $? -eq 1 ] || die 1 "git merge-tree failed"
	merges=conflict
}

# files.txt: added, removed, path, class. A binary file counts no lines;
# binary= says how many there are.
lines=0 nfiles=0 tier2=0 light=1
binary=$(gd --numstat --no-renames "$B" "$H" | grep -c '^-	-	' || true)
while IFS=$'\t' read -r a d f; do
	[ "$a" = - ] && a=0
	[ "$d" = - ] && d=0
	c=$(class "$f")
	printf '%s\t%s\t%s\t%s\n' "$a" "$d" "$f" "$c" >>"$C/files.txt"
	lines=$((lines + a + d)) nfiles=$((nfiles + 1))
	case $c in agent-text | ci | deps | schema) tier2=$((tier2 + 1)) ;; esac
	case $c in docs | config) ;; *) light= ;; esac
done < <(gd --numstat --no-renames "$B" "$H")
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
		if T=$(git merge-tree --write-tree "$K" "$B" | head -n 1) && gd "$T" "$H" >"$C/delta.patch"; then
			delta=$(gd --numstat --no-renames "$T" "$H")
		else
			T=
			# A conflict: the files both the author and this PR changed.
			mapfile -t both < <(comm -12 <(gd --name-only "$K" "$H" | sort) <(gd --name-only "$B" "$H" | sort))
			: >"$C/delta.patch"
			delta=
			if [ ${#both[@]} -gt 0 ]; then
				gd "$B" "$H" -- "${both[@]}" >"$C/delta.patch"
				delta=$(gd --numstat --no-renames "$B" "$H" -- "${both[@]}")
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
	echo "binary=$binary"
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
