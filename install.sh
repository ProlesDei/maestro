#!/usr/bin/env bash
set -euo pipefail

repo_url=${MAESTRO_REPO:-https://github.com/ProlesDei/maestro.git}
ref=main
ref_given=false
mode=install
harnesses=".claude/skills .agents/skills"
folders=".maestro .maestro/skills .claude .claude/skills .agents .agents/skills"
top=".maestro/VERSION .maestro/install.sh .maestro/.gitattributes"
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

for p in $folders; do
  if [ -L "$p" ]; then die "$p is a symlink; Maestro only installs into real folders"; fi
  if [ -e "$p" ] && [ ! -d "$p" ]; then die "$p exists and isn't a folder"; fi
done
for p in .maestro/manifest $top; do
  if [ -L "$p" ]; then die "$p is a symlink; Maestro didn't place it"; fi
done
index=$(git rev-parse --git-path index)
if [ -e "$index.lock" ]; then die "git is busy (index.lock exists); try again when it's done"; fi

manifest=.maestro/manifest

names_in_manifest() {
  if [ -f "$manifest" ]; then grep "^d$tab" "$manifest" | cut -f2; fi
}

hash_of() {
  git hash-object -- "$1"
}

snapshot() {
  local n h p sum
  while IFS= read -r n; do
    if [ -d ".maestro/skills/$n" ]; then find ".maestro/skills/$n" ! -type d; fi
  done < "$1" | LC_ALL=C sort | while IFS= read -r p; do
    if [ -L "$p" ]; then
      sum=$(readlink "$p")
      printf 'l\t%s\t%s\n' "$sum" "$p"
    else
      sum=$(hash_of "$p")
      printf 'f\t%s\t%s\n' "$sum" "$p"
    fi
  done
  for p in $top; do
    if [ -f "$p" ] && [ ! -L "$p" ]; then
      sum=$(hash_of "$p")
      printf 'f\t%s\t%s\n' "$sum" "$p"
    else printf 'f\t-\t%s\n' "$p"; fi
  done
  while IFS= read -r n; do
    for h in $harnesses; do
      p="$h/$n"
      if [ -L "$p" ]; then
        sum=$(readlink "$p")
        printf 'l\t%s\t%s\n' "$sum" "$p"
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
  if ! { echo "$manifest"; grep -v "^d$tab" "$manifest" | cut -f3; } | tr '\n' '\0' | xargs -0 git diff --quiet $skip --; then
    rm -f "$names"
    die "Maestro's paths have staged changes that differ from the files; commit or unstage them, then run this again"
  fi
  if [ -z "$skip" ] && git rev-parse -q --verify HEAD >/dev/null; then
    if ! { echo "$manifest"; grep -v "^d$tab" "$manifest" | cut -f3; } | tr '\n' '\0' | xargs -0 git diff --cached --quiet --diff-filter=D --; then
      rm -f "$names"
      die "Maestro's paths have staged deletions; commit or unstage them, then run this again"
    fi
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
  for p in "$manifest" $top; do
    if [ -f "$p" ]; then git add -f -- "$p"
    else git rm -q --cached --ignore-unmatch -- "$p"; fi
  done
  if [ -f .maestro/install.sh ]; then git update-index --chmod=+x -- .maestro/install.sh; fi
  while IFS= read -r n; do
    if [ -d ".maestro/skills/$n" ]; then
      git add -A -f -- ".maestro/skills/$n"
      find ".maestro/skills/$n" -type f -perm -u+x -print0 | xargs -0 git update-index --chmod=+x --
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
  for p in $top; do rm -f "$p"; done
  stage_paths "$names"
  rm -f "$manifest" "$names"
  git rm -q --cached --ignore-unmatch -- "$manifest"
  for d in .maestro/skills .maestro $harnesses; do
    if [ -d "$d" ]; then rmdir "$d" 2>/dev/null || true; fi
  done
  echo "maestro: removed and staged; commit to finish"
  exit 0
fi

if [ "$is_source" = false ] || [ "$ref_given" = true ]; then
  check_untouched
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  git init -q "$tmp"
  git -C "$tmp" fetch -q --depth 1 "$repo_url" "$ref" || die "couldn't fetch $ref from $repo_url"
  git -C "$tmp" -c advice.detachedHead=false checkout -q FETCH_HEAD
  [ -f "$tmp/install.sh" ] && [ -d "$tmp/skills" ] || die "$ref has no install.sh and skills/; pick a newer commit"
  bash "$tmp/install.sh"
  exit $?
fi

stage=$(mktemp -d "$root/.maestro-stage.XXXXXX")
new=$stage/new-names
old=$stage/old-names
names=$stage/names
index_lock=
created=
swapped=()

cleanup_install() {
  local status=$? p failed=false
  if [ "$status" -ne 0 ]; then
    for p in ${swapped[@]+"${swapped[@]}"}; do
      rm -rf -- "$p" || { failed=true; continue; }
      if [ -e "$stage/old/$p" ] || [ -L "$stage/old/$p" ]; then
        mv -- "$stage/old/$p" "$p" || failed=true
      fi
    done
    for p in $created; do rmdir "$p" || failed=true; done
  fi
  if [ -n "$index_lock" ]; then rm -f "$index_lock"; fi
  if [ "$failed" = true ]; then
    echo "maestro: rollback failed; backups are in $stage" >&2
  else
    rm -rf "$stage"
  fi
}
trap cleanup_install EXIT

swap_path() {
  local p=$1
  if [ -e "$p" ] || [ -L "$p" ]; then mv -- "$p" "$stage/old/$p"; fi
  swapped+=("$p")
  if [ -e "$stage/new/$p" ] || [ -L "$stage/new/$p" ]; then mv -- "$stage/new/$p" "$p"; fi
}

if ! (set -C; : > "$index.lock") 2>/dev/null; then
  die "git is busy (index.lock exists); try again when it's done"
fi
index_lock=$index.lock
check_untouched

for d in "$here"/skills/maestro-*/; do
  if [ -f "$d/SKILL.md" ]; then basename "$d"; fi
done > "$new"
[ -s "$new" ] || die "no maestro-* skills found in $here/skills"

names_in_manifest > "$old"
sort -u "$old" "$new" > "$names"

clash=""
while IFS= read -r n; do
  grep -qxF "$n" "$old" && continue
  for p in ".maestro/skills/$n" .claude/skills/"$n" .agents/skills/"$n"; do
    if [ -e "$p" ] || [ -L "$p" ]; then clash="$clash  $p"$'\n'; fi
  done
done < "$new"
[ -z "$clash" ] || die "these paths exist and Maestro didn't place them; rename or remove them first:"$'\n'"$clash"

for d in $folders; do mkdir -p "$stage/new/$d" "$stage/old/$d"; done
while IFS= read -r n; do
  cp -R "$here/skills/$n" "$stage/new/.maestro/skills/$n"
  for h in $harnesses; do
    ln -s "../../.maestro/skills/$n" "$stage/new/$h/$n"
    [ -L "$stage/new/$h/$n" ] || die "$h/$n isn't a symlink; this shell can't make them (on Windows, use WSL)"
  done
done < "$new"
cp "$here/install.sh" "$stage/new/.maestro/install.sh"
chmod +x "$stage/new/.maestro/install.sh"
version=$(git -C "$here" rev-parse HEAD)
printf '%s\n' "$version" > "$stage/new/.maestro/VERSION"
printf '* -text -eol\n' > "$stage/new/.maestro/.gitattributes"
count=$(wc -l < "$new" | tr -d ' ')
if [ -f "$index" ]; then cp "$index" "$stage/index"; fi

for d in $folders; do
  if [ ! -d "$d" ]; then
    mkdir "$d"
    created="$d $created"
  fi
done
while IFS= read -r n; do
  swap_path ".maestro/skills/$n"
  for h in $harnesses; do swap_path "$h/$n"; done
done < "$names"
for p in $top; do swap_path "$p"; done
{
  sed "s/^/d$tab/" "$new"
  snapshot "$new"
} > "$stage/new/$manifest"
swap_path "$manifest"

GIT_INDEX_FILE="$stage/index" stage_paths "$names"
cp -p "$stage/index" "$index_lock"
mv -- "$index_lock" "$index"
trap - EXIT
rm -rf "$stage"

echo "maestro: installed $count skills at ${version:0:12} and staged them"
echo "maestro: commit .maestro/, .claude/skills/ and .agents/skills/ to your default branch"
