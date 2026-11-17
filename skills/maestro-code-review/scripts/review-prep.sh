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
#     agent reads as instructions, else route=full, with pr= and lines=,
#     its added plus deleted lines.
#   review-prep.sh --qa <target>
#     The fixed steps of a QA test (maestro-qa-lead): the same target,
#     stops, fetch, diff files and sorting, then what QA needs in place
#     of review's. A run name no other run shares, run=qa-<id>-<6
#     letters>, names the worktree; today's main, G, is merged into it
#     (merged=yes|conflict; a conflict is aborted and exits 0, since QA
#     posts a fail for it), and the commit is never pushed. E is the
#     run's evidence folder in the repo's shared git folder. review_go is
#     the sha review's latest verdict passed; qa_last, qa_last_at and
#     qa_last_main are QA's own latest verdict. K is that verdict's sha
#     only when it is a fail that tested, at a sha in H's history: then
#     mode=retest, with the author's delta since K as review works it
#     out. delta_classes and main_since_classes name the file classes
#     the delta, and main since that verdict's G, changed.
#
# Run it inside the repo, any worktree. Needs git 2.38+, gh and jq, and
# maestro-ticket for a ticket's verdicts. Exit 2: bad target; 3: no PR or
# no main branch; 1: any other failure.
set -euo pipefail

die() { echo "review-prep: $2" >&2; exit "$1"; }

route="" qa=""
case ${1-} in
--route) route=1; shift ;;
--qa) qa=1; shift ;;
esac
[ $# -eq 1 ] || die 2 "usage: review-prep.sh [--route | --qa] <PR number | maestro/T-<n> | T-<n>>"
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
# PR's tree. Where the main repo is bare, the install sits in the linked
# worktree that holds .maestro/src/ (install.sh allows only one), a clone
# of Maestro: its .git is looked for, since git tracks no path of that
# name, so no PR's tree can pass for the install.
wts=$(git worktree list --porcelain)
mainwt=$(sed -n '1s/^worktree //p' <<<"$wts")
if [ "$(sed -n 2p <<<"$wts")" = bare ]; then
	while IFS= read -r w; do
		[ -e "$w/.maestro/src/.git" ] && { mainwt=$w; break; }
	done < <(sed -n 's/^worktree //p' <<<"$wts")
fi
setup=$mainwt/.maestro/project/project-setup.md
# Read as install.sh check reads it: \r, spaces, the heading's case, a
# leading bullet and backticks don't count.
main=
[ -f "$setup" ] && main=$(awk '{sub(/\r$/, ""); gsub(/^[ \t]+|[ \t]+$/, "")}
	/^## / {h=substr($0, 4); gsub(/^[ \t]+|[ \t]+$/, "", h); on=(tolower(h)=="main branch"); next}
	on && $0 != "" && !/^#/ {sub(/^[-* ]+/, ""); gsub(/^[`* ]+|[`* ]+$/, ""); print; exit}' "$setup")

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
	# One read of the PR: its files, and its changed lines, which size the
	# review's time limit.
	pv=$(gh pr view "$pr" --json files,additions,deletions) || die 3 "gh found no PR $pr"
	echo "pr=$pr"
	echo "lines=$(jq -r '(.additions // 0) + (.deletions // 0)' <<<"$pv")"
	full() { echo route=full; echo "why=$1"; exit 0; }
	# With no main or no rules to read, nothing says which docs agents obey.
	[ -n "$main" ] || full "no ## Main branch in the main checkout's project-setup.md"
	load_rules
	[ -n "$rules" ] || full "no AGENTS.md, CLAUDE.md or project-setup.md to read"
	files=$(jq -r '.files[] | "\(.changeType)\t\(.path)"' <<<"$pv")
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
# QA's run name, on a stop too: it keys the note QA posts for one. Six
# letters from the OS, so runs started in one second, by hundreds of
# sessions, still share no worktree, evidence folder or write key.
qarun=
if [ -n "$qa" ]; then
	abc=abcdefghijklmnopqrstuvwxyz rand=""
	for b in $(od -An -N6 -tu1 /dev/urandom); do rand+=${abc:b % 26:1}; done
	[ ${#rand} -eq 6 ] || die 1 "no 6 random bytes from /dev/urandom"
	qarun=qa-${ticket:-pr-$pr}-$rand
fi
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
	[ -z "$qarun" ] || echo "run=$qarun"
	exit 0
fi

# Many sessions fetching in one checkout collide on ref locks: each fetch
# takes only main, not every branch others push, and a failure gets a
# random pause and two more tries, as the working principles ask.
retry() {
	local i
	for i in 1 2 3; do
		"$@" && return 0
		[ "$i" = 3 ] || sleep $((RANDOM % 5 + 1))
	done
	return 1
}
retry git fetch -q origin "+refs/heads/$main:refs/remotes/origin/$main" || die 1 "git fetch origin $main failed"
git cat-file -e "$H^{commit}" 2>/dev/null || retry git fetch -q origin "pull/$pr/head" ||
	die 1 "can't fetch PR $pr's head $H"
git rev-parse -q --verify "refs/remotes/origin/$main^{commit}" >/dev/null || die 3 "no origin/$main after the fetch"
load_rules

S=$(date +%s)
id=${ticket:-pr-$pr}
run=review-$id-$S E="" G=""
# What main is for the rest of the run: in QA mode its sha, not the ref,
# which another session's fetch can move while this one works.
tip=origin/$main
if [ -n "$qa" ]; then
	run=$qarun
	G=$(git rev-parse "refs/remotes/origin/$main^{commit}")
	tip=$G
	# In the shared git folder, never the worktree: the PR controls that.
	E=$(git rev-parse --path-format=absolute --git-common-dir)/maestro/evidence/$id/$run
fi
C=$(mktemp -d)
R=$(mktemp -d)/$run
# A failed prep leaves nothing behind for the session to clean up. Of
# the evidence folders only this run's goes: others may be writing in
# the ticket's.
cleanup() {
	[ $? -eq 0 ] && return
	git worktree remove --force "$R" 2>/dev/null || true
	rm -rf -- "$C" "${R%/*}"
	[ -z "$E" ] || rm -rf -- "$E"
}
trap cleanup EXIT
# bash runs no EXIT trap when a signal kills it, and a run's time limit
# ends it with one: exit, so the trap runs.
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
git worktree add -q --detach "$R" "$H" || die 1 "git worktree add $R failed"
[ "$(git -C "$R" rev-parse HEAD)" = "$H" ] || die 1 "worktree $R isn't at $H"

merged=yes
if [ -n "$qa" ]; then
	mkdir -p "$E" || die 1 "can't make $E"
	# What ships is H on today's main, so that is what QA runs. The merge
	# commit stays in this worktree and is never pushed, so it takes a
	# stand-in author where git has none, no signature, and no hook: a
	# hook is a check, and the session runs the checks.
	ident=()
	{ git -C "$R" var GIT_AUTHOR_IDENT && git -C "$R" var GIT_COMMITTER_IDENT; } >/dev/null 2>&1 ||
		ident=(-c user.name=maestro-qa -c user.email=maestro-qa@localhost)
	if ! err=$(git -C "$R" "${ident[@]}" -c commit.gpgsign=false -c core.hooksPath=/dev/null \
		merge -q --ff --no-edit "$G" 2>&1); then
		# Only a merge git stopped half way is a conflict.
		git -C "$R" rev-parse -q --verify MERGE_HEAD >/dev/null ||
			die 1 "git merge of $main at $G into $R failed: $(tail -n 1 <<<"$err")"
		git -C "$R" merge --abort || die 1 "git merge --abort in $R failed"
		merged=conflict
	fi
fi

B=$(git merge-base "$tip" "$H")
jq . <<<"$view" >"$C/pr.json"
gd "$B" "$H" >"$C/diff.patch"
gd --stat "$B" "$H" >"$C/stat.txt"
git log --oneline "$B..$H" >"$C/log.txt"
merges=yes
# QA's real merge above has already said.
[ -n "$qa" ] || git merge-tree --write-tree "origin/$main" "$H" >/dev/null 2>&1 || {
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
		jq -r '.comments[] | (.first_line // (.body | split("\n") | map(select(test("\\S"))) | .[0] // "")) | sub("^[ \t]+"; "")') ||
		die 1 "maestro-ticket get $ticket failed"
else
	firsts=$(gh api --paginate "repos/{owner}/{repo}/issues/$pr/comments" \
		--jq '.[] | select(.author_association == "OWNER" or .author_association == "MEMBER" or .author_association == "COLLABORATOR") | .body | split("\n") | map(select(test("\\S"))) | .[0] // ""') ||
		die 1 "gh couldn't read PR $pr's comments"
fi
# sha_after <line> <text>: the full sha right after the first <text> in
# the line, or nothing.
sha_after() {
	[[ $1 == *"$2"* ]] || return 0
	[[ ${1#*"$2"} =~ ^([0-9a-f]{40})([^[:alnum:]]|$) ]] && echo "${BASH_REMATCH[1]}"
	return 0
}
# classes: the distinct classes of the paths on stdin, on one line.
classes() {
	local f out
	out=$(while IFS= read -r f; do if [ -n "$f" ]; then class "$f"; fi; done | sort -u | tr '\n' ' ')
	out=${out% }
	echo "${out:-none}"
}
inc=incremental by="review's verdict" why="no earlier verdict" delta=""
if [ -n "$qa" ]; then
	# What review passed: its latest verdict, a Kickback: of its own
	# included, and only when that one opens as a GO.
	rlast=$(jq -Rr "select($verdict)" <<<"$firsts" | tail -n 1)
	review_go=
	[[ $rlast != "Review: GO at "* ]] || review_go=$(sha_after "$rlast" "Review: GO at ")
	# QA's own latest verdict. Only a fail that tested is a base for a
	# retest: a pass or a "not tested:" checked nothing to carry over.
	inc=retest by="QA's fail" why="no earlier QA verdict" K=""
	last=$(grep -E '^(QA: pass|Kickback: QA fail)' <<<"$firsts" | tail -n 1 || true)
	qa_last=none qa_at="" qa_main=""
	if [ -n "$last" ]; then
		qa_at=$(sha_after "$last" " at ")
		qa_main=$(sha_after "$last" " on $main at ")
		case $last in
		"QA: pass"*) qa_last=pass why="QA's last verdict is a pass" ;;
		*"not tested:"*) qa_last=not-tested why="QA's last fail tested nothing" ;;
		*) qa_last=fail K=$qa_at why="QA's last fail names no sha" ;;
		esac
	fi
else
	last=$(jq -Rr "select($verdict)" <<<"$firsts" | tail -n 1)
	# The word after the first " at " only, and only a full sha.
	case $last in *" at "*) K=$(awk '{print $1}' <<<"${last#* at }"); [[ $K =~ ^[0-9a-f]{40}$ ]] || K= ;; *) K= ;; esac
fi

mode=full T="" dfiles="" dlines="" dclasses=""
if [ -n "$K" ]; then
	if ! git cat-file -e "$K^{commit}" 2>/dev/null || ! git merge-base --is-ancestor "$K" "$H"; then
		why="$K is not in $H's history"
		# QA's K is only ever a sha a retest can start from.
		[ -z "$qa" ] || K=
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
		[ -z "$qa" ] || dclasses=$(cut -f3- <<<"$delta" | classes)
		if [ "$dlines" -gt 400 ] || [ "$dfiles" -gt 15 ]; then
			why="delta of $dlines lines in $dfiles files: redone, not fixed"
		else
			mode=$inc why="from $by at $K"
		fi
	fi
fi
# What main changed since the G of QA's last verdict; unknown when that
# verdict names no main, or one this repo doesn't have.
if [ -n "$qa" ]; then
	msince=unknown
	if [ -n "$qa_main" ] && git cat-file -e "$qa_main^{commit}" 2>/dev/null; then
		msince=$(gd --name-only --no-renames "$qa_main" "$G" | classes)
	fi
fi

{
	pkeys
	echo "stop=none"
	echo "S=$S"
	if [ -n "$qa" ]; then
		echo "run=$run"
		echo "R=$R"
		echo "C=$C"
		echo "E=$E"
		echo "B=$B"
		echo "G=$G"
		echo "merged=$merged"
	else
		echo "R=$R"
		echo "C=$C"
		echo "B=$B"
		echo "merges=$merges"
	fi
	echo "files=$nfiles"
	echo "lines=$lines"
	echo "binary=$binary"
	echo "tier2_paths=$tier2"
	echo "docs_only=$([ -n "$light" ] && [ "$nfiles" -gt 0 ] && echo yes || echo no)"
	if [ -n "$qa" ]; then
		echo "review_go=${review_go:-none}"
		echo "qa_last=$qa_last"
		echo "qa_last_at=${qa_at:-none}"
		echo "qa_last_main=${qa_main:-none}"
	fi
	echo "K=${K:-none}"
	echo "T=${T:-none}"
	echo "delta_files=${dfiles:-none}"
	echo "delta_lines=${dlines:-none}"
	if [ -n "$qa" ]; then
		echo "delta_classes=${dclasses:-none}"
		echo "main_since_classes=$msince"
	fi
	echo "mode=$mode"
	echo "mode_why=$why"
	echo "wrote=$C: pr.json diff.patch stat.txt log.txt files.txt$([ -f "$C/delta.patch" ] && echo ' delta.patch') prep.txt"
} | tee "$C/prep.txt"
