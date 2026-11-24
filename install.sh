#!/usr/bin/env bash
set -euo pipefail

repo_url=${MAESTRO_REPO:-https://github.com/ProlesDei/maestro.git}
ref=main
ref_given=false
mode=install
harnesses=".claude/skills .agents/skills"
tab=$(printf '\t')

die() { echo "maestro: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case $1 in
    --ref) [ $# -ge 2 ] || die "--ref needs a commit, tag or branch"; ref=$2; ref_given=true; shift 2 ;;
    --uninstall) mode=uninstall; shift ;;
    -h|--help) echo "usage: install.sh [--ref <commit|tag|branch>] [--uninstall]"; exit 0 ;;
    *) die "unknown option: $1 (see --help)" ;;
  esac
done

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
root=$(git rev-parse --show-toplevel 2>/dev/null) || die "run this from inside your project's git repo"
root=$(cd "$root" && pwd -P)
cd "$root"

is_source=false
if [ -d "$here/skills" ] && [ "$(git -C "$here" rev-parse --show-toplevel 2>/dev/null)" = "$here" ]; then
  is_source=true
fi
[ "$is_source" = true ] && [ "$here" = "$root" ] && die "run this from your project's repo, not from the Maestro clone"

for p in .maestro .maestro/skills .claude .claude/skills .agents .agents/skills; do
  if [ -L "$p" ]; then die "$p is a symlink; Maestro only installs into real folders"; fi
  if [ -e "$p" ] && [ ! -d "$p" ]; then die "$p exists and isn't a folder"; fi
done
for p in .maestro/manifest .maestro/VERSION .maestro/install.sh; do
  if [ -L "$p" ]; then die "$p is a symlink; Maestro didn't place it"; fi
done
if [ -e "$(git rev-parse --git-path index.lock)" ]; then die "git is busy (index.lock exists); try again when it's done"; fi

manifest=.maestro/manifest

names_in_manifest() {
  if [ -f "$manifest" ]; then grep "^d$tab" "$manifest" | cut -f2; fi
}

hash_of() {
  local x=""
  if [ -x "$1" ]; then x="+x"; fi
  printf '%s%s' "$(git hash-object --no-filters -- "$1")" "$x"
}

snapshot() {
  local n h p
  while IFS= read -r n; do
    if [ -d ".maestro/skills/$n" ]; then find ".maestro/skills/$n" ! -type d; fi
  done < "$1" | LC_ALL=C sort | while IFS= read -r p; do
    if [ -L "$p" ]; then
      printf 'l\t%s\t%s\n' "$(readlink "$p")" "$p"
    else
      printf 'f\t%s\t%s\n' "$(hash_of "$p")" "$p"
    fi
  done
  for p in .maestro/VERSION .maestro/install.sh; do
    if [ -f "$p" ] && [ ! -L "$p" ]; then printf 'f\t%s\t%s\n' "$(hash_of "$p")" "$p"
    else printf 'f\t-\t%s\n' "$p"; fi
  done
  while IFS= read -r n; do
    for h in $harnesses; do
      p="$h/$n"
      if [ -L "$p" ]; then printf 'l\t%s\t%s\n' "$(readlink "$p")" "$p"
      elif [ -e "$p" ]; then printf 'l\t-\t%s\n' "$p"
      else printf 'l\tmissing\t%s\n' "$p"; fi
    done
  done < "$1"
}

present_only() {
  local kind sum p
  while IFS="$tab" read -r kind sum p; do
    if [ "$sum" = missing ] || [ "$sum" = - ]; then continue; fi
    if [ -e "$p" ] || [ -L "$p" ]; then printf '%s\t%s\t%s\n' "$kind" "$sum" "$p"; fi
  done
}

check_untouched() {
  [ -e .maestro ] || return 0
  [ -f "$manifest" ] || die ".maestro/ exists but Maestro didn't place it (no $manifest); move it aside first"
  local names filter=cat skip=""
  if [ "${1:-}" = allow_missing ]; then filter=present_only; skip=--diff-filter=d; fi
  names=$(mktemp)
  names_in_manifest > "$names"
  if ! grep -v "^d$tab" "$manifest" | cut -f3 | tr '\n' '\0' | xargs -0 git diff --quiet $skip --; then
    rm -f "$names"
    die "Maestro's paths have staged changes that differ from the files; commit or unstage them, then run this again"
  fi
  if ! diff <(grep -v "^d$tab" "$manifest" | $filter) <(snapshot "$names" | $filter) > "$names.diff"; then
    echo "maestro: these paths changed since the last install:" >&2
    grep '^[<>]' "$names.diff" | cut -f3 | LC_ALL=C sort -u | head -20 | sed 's/^/  /' >&2
    rm -f "$names" "$names.diff"
    die "commit your changes somewhere else or undo them (git checkout / git clean), then run this again"
  fi
  rm -f "$names" "$names.diff"
}

stage_paths() {
  local n h p
  for p in "$manifest" .maestro/VERSION .maestro/install.sh; do
    if [ -f "$p" ]; then git add -f -- "$p"
    else git rm -q --cached --ignore-unmatch -- "$p"; fi
  done
  while IFS= read -r n; do
    if [ -d ".maestro/skills/$n" ]; then git add -A -f -- ".maestro/skills/$n"
    else git rm -r -q --cached --ignore-unmatch -- ".maestro/skills/$n"; fi
    for h in $harnesses; do
      if [ -L "$h/$n" ]; then git add -f -- "$h/$n"
      else git rm -q --cached --ignore-unmatch -- "$h/$n"; fi
    done
  done < "$1"
}

if [ "$mode" = uninstall ]; then
  [ -f "$manifest" ] || die "no $manifest here; nothing Maestro placed to remove"
  check_untouched allow_missing
  names=$(mktemp)
  names_in_manifest > "$names"
  while IFS= read -r n; do
    for h in $harnesses; do
      if [ -L "$h/$n" ]; then rm "$h/$n"; fi
    done
    rm -rf ".maestro/skills/$n"
  done < "$names"
  rm -f .maestro/VERSION .maestro/install.sh
  stage_paths "$names"
  rm -f "$manifest" "$names"
  git rm -q --cached --ignore-unmatch -- "$manifest"
  for d in .maestro/skills .maestro $harnesses; do
    if [ -d "$d" ]; then rmdir "$d" 2>/dev/null || true; fi
  done
  echo "maestro: removed and staged; commit to finish"
  exit 0
fi

check_untouched

if [ "$is_source" = false ] || [ "$ref_given" = true ]; then
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  git init -q "$tmp"
  git -C "$tmp" fetch -q --depth 1 "$repo_url" "$ref" || die "couldn't fetch $ref from $repo_url"
  git -C "$tmp" -c advice.detachedHead=false checkout -q FETCH_HEAD
  [ -f "$tmp/install.sh" ] && [ -d "$tmp/skills" ] || die "$ref has no install.sh and skills/; pick a newer commit"
  bash "$tmp/install.sh"
  exit $?
fi

new=$(mktemp)
old=$(mktemp)
stage=""
trap 'rm -f "$new" "$old"; if [ -n "$stage" ]; then rm -rf "$stage"; fi' EXIT

for d in "$here"/skills/maestro-*/; do
  if [ -f "$d/SKILL.md" ]; then basename "$d"; fi
done > "$new"
[ -s "$new" ] || die "no maestro-* skills found in $here/skills"

names_in_manifest > "$old"

clash=""
while IFS= read -r n; do
  grep -qxF "$n" "$old" && continue
  for p in ".maestro/skills/$n" .claude/skills/"$n" .agents/skills/"$n"; do
    if [ -e "$p" ] || [ -L "$p" ]; then clash="$clash  $p"$'\n'; fi
  done
done < "$new"
[ -z "$clash" ] || die "these paths exist and Maestro didn't place them; rename or remove them first:"$'\n'"$clash"

mkdir -p .maestro/skills .claude/skills .agents/skills
stage=$(mktemp -d "$root/.maestro/.stage.XXXXXX")
while IFS= read -r n; do
  cp -R "$here/skills/$n" "$stage/$n"
done < "$new"

while IFS= read -r n; do
  if grep -qxF "$n" "$new"; then continue; fi
  for h in $harnesses; do
    if [ -L "$h/$n" ]; then rm "$h/$n"; fi
  done
  rm -rf ".maestro/skills/$n"
done < "$old"

while IFS= read -r n; do
  if [ -d ".maestro/skills/$n" ]; then mv ".maestro/skills/$n" "$stage/$n.old"; fi
  mv "$stage/$n" ".maestro/skills/$n"
  for h in $harnesses; do
    ln -sfn "../../.maestro/skills/$n" "$h/$n"
    [ -L "$h/$n" ] || die "$h/$n isn't a symlink; this shell can't make them (on Windows, use WSL)"
  done
done < "$new"
rm -rf "$stage"
stage=""

cp "$here/install.sh" .maestro/install.sh
chmod +x .maestro/install.sh
git -C "$here" rev-parse HEAD > .maestro/VERSION

{
  sed "s/^/d$tab/" "$new"
  snapshot "$new"
} > "$manifest"

stage_paths "$old"
stage_paths "$new"

echo "maestro: installed $(wc -l < "$new" | tr -d ' ') skills at $(cut -c1-12 .maestro/VERSION) and staged them"
echo "maestro: commit .maestro/, .claude/skills/ and .agents/skills/ to your default branch"
