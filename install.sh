#!/usr/bin/env bash
set -euo pipefail

repo_url=${MAESTRO_REPO:-https://github.com/ProlesDei/maestro.git}
ref=main
ref_given=false
mode=install
harnesses=".claude/skills .agents/skills"
manifest=.maestro/manifest
tab=$(printf '\t')

die() { echo "maestro: $*" >&2; exit 1; }
trap 'exit 130' INT; trap 'exit 143' TERM; trap 'exit 129' HUP

while [ $# -gt 0 ]; do
  case $1 in
    --ref) [ $# -ge 2 ] || die "--ref needs a tag, branch or full commit sha"; ref=$2; ref_given=true; shift 2 ;;
    --uninstall) mode=uninstall; shift ;;
    -h|--help) echo "usage: install.sh [--ref <tag|branch|full sha>] [--uninstall]"; exit 0 ;;
    *) die "unknown option: $1 (see --help)" ;;
  esac
done

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
is_source=false
if [ -d "$here/skills" ] && [ "$(git -C "$here" rev-parse --show-toplevel 2>/dev/null)" = "$here" ]; then
  is_source=true
fi

if [ "$mode" = install ] && { [ "$is_source" = false ] || [ "$ref_given" = true ]; }; then
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  git init -q "$tmp"
  git -C "$tmp" fetch -q --depth 1 "$repo_url" "$ref" || die "couldn't fetch $ref from $repo_url"
  git -C "$tmp" -c advice.detachedHead=false checkout -q FETCH_HEAD
  [ -f "$tmp/install.sh" ] && [ -d "$tmp/skills" ] || die "$ref has no install.sh and skills/; pick a newer one"
  bash "$tmp/install.sh"
  exit
fi

root=$(git rev-parse --show-toplevel 2>/dev/null) || die "run this from inside your project's git repo"
root=$(cd "$root" && pwd -P)
cd "$root"
linked=
for link in .claude/skills .agents/skills; do
  for real in .agents/skills .claude/skills; do
    if [ "$link" != "$real" ] && [ -L "$link" ] && [ -d "$real" ] && [ ! -L "$real" ] &&
      [ "$(cd "$link" && pwd -P)" = "$(cd "$real" && pwd -P)" ]; then harnesses=$real linked=$link; fi
  done
done
[ "$is_source" = true ] && [ "$here" = "$root" ] && die "run this from your project's repo, not from the Maestro clone"

index=$(git rev-parse --git-path index)
stage=$(mktemp -d "$(git rev-parse --path-format=absolute --git-path maestro-stage.XXXXXX)")
prev=$stage/prev-manifest
index_lock=
created=
finished=false

cleanup() {
  local p failed=false
  trap '' INT TERM HUP
  if [ "$finished" = false ] && [ -f "$stage/incoming" ]; then
    while IFS= read -r p; do
      if [ -e "$stage/old/$p" ] || [ -L "$stage/old/$p" ]; then
        { rm -rf -- "$p" && mv -- "$stage/old/$p" "$p"; } || failed=true
      elif [ ! -e "$stage/new/$p" ] && [ ! -L "$stage/new/$p" ] && grep -qxF -- "$p" "$stage/incoming"; then
        rm -rf -- "$p" || failed=true
      fi
    done < "$stage/all"
    for p in $created; do rmdir "$p" || failed=true; done
  fi
  if [ -n "$index_lock" ]; then rm -f "$index_lock"; fi
  if [ "$failed" = true ]; then
    echo "maestro: rollback failed; backups are in $stage" >&2
  else
    rm -rf "$stage"
  fi
}
trap cleanup EXIT

if ! (set -C; : > "$index.lock") 2>/dev/null; then
  die "git is busy (index.lock exists); try again when it's done"
fi
index_lock=$index.lock

units() {
  awk -F'\t' '$1=="d"{d[$2]=1; print $2; next}
    $1=="f"||$1=="l"{s=$3; while (sub("/[^/]*$", "", s)) if (s in d) next; print $3}' "$1" | LC_ALL=C sort -u
}

parents() {
  local p
  while IFS= read -r p; do
    while [ "${p%/*}" != "$p" ]; do p=${p%/*}; echo "$p"; done
  done | LC_ALL=C sort -u
}

state() {
  local p
  while IFS= read -r p; do
    if [ -L "$p" ]; then printf 'l\t%s\t%s\n' "$(readlink "$p")" "$p"
    elif [ -f "$p" ]; then printf 'f\t%s\t%s\n' "$(git hash-object --stdin < "$p")" "$p"
    elif [ -e "$p" ]; then printf '?\t-\t%s\n' "$p"
    fi
  done
}

snapshot() {
  {
    awk -F'\t' '$1=="f"||$1=="l"{print $3}' "$prev"
    awk -F'\t' '$1=="d"{print $2}' "$prev" | while IFS= read -r d; do
      if [ -d "$d" ]; then find "$d" ! -type d; fi
    done
  } | LC_ALL=C sort -u | state | LC_ALL=C sort
}

present_only() {
  local kind sum p
  while IFS="$tab" read -r kind sum p; do
    if [ -e "$p" ] || [ -L "$p" ]; then printf '%s\t%s\t%s\n' "$kind" "$sum" "$p"; fi
  done
}

check_untouched() {
  local want=cat skip='' changed next rmcmd h
  if [ "$mode" = uninstall ]; then want=present_only; skip=--diff-filter=d; fi
  if ! diff <(grep "^[fl]$tab" "$prev" | $want | LC_ALL=C sort) <(snapshot) > "$stage/diff"; then
    echo "maestro: these paths differ from what Maestro placed:" >&2
    grep '^[<>]' "$stage/diff" | cut -f3 | LC_ALL=C sort -u | head -20 | sed 's/^/  /' >&2
    if [ "$mode" = uninstall ]; then next="and commit"; else next="and then the install one-liner in INSTALL.md"; fi
    rmcmd="git rm -rfq --ignore-unmatch .maestro"
    for h in $harnesses; do rmcmd="$rmcmd $h/maestro-*"; done
    [ -z "$linked" ] || rmcmd="$rmcmd; mkdir -p $harnesses"
    die "undo those changes, then run this again. If you committed them (a formatter rewrote files, say), run $rmcmd $next"
  fi
  { echo "$manifest"; cat "$stage/old-units"; } | tr '\n' '\0' > "$stage/specs"
  changed=$({
    xargs -0 git diff --name-only ${skip:+"$skip"} -- < "$stage/specs"
    if [ "$mode" = install ]; then xargs -0 git diff --cached --name-only --diff-filter=D -- < "$stage/specs"; fi
  })
  if [ -n "$changed" ]; then
    echo "maestro: git's index holds changes to these paths that the files don't have:" >&2
    printf '%s\n' "$changed" | LC_ALL=C sort -u | head -20 | sed 's/^/  /' >&2
    die "run git add -f on them so the index matches the files, then run this again"
  fi
}

stage_paths() {
  local p
  while IFS= read -r p; do
    if [ -e "$p" ] || [ -L "$p" ]; then
      git add -A -f -- "$p"
      find "$p" -type f -perm -u+x -exec git update-index --chmod=+x -- {} +
    else git rm -r -q --cached --ignore-unmatch -- "$p"; fi
  done
}

swap_path() {
  local p=$1
  if [ -e "$p" ] || [ -L "$p" ]; then mv -- "$p" "$stage/old/$p"; fi
  if [ -e "$stage/new/$p" ] || [ -L "$stage/new/$p" ]; then mv -- "$stage/new/$p" "$p"; fi
}

if [ -e .maestro ]; then
  [ -f "$manifest" ] || die ".maestro/ exists but Maestro didn't place it (no $manifest); move it aside first"
  case $(head -n 1 "$manifest") in
    "format 1") cp "$manifest" "$prev" ;;
    format*) die "$manifest isn't format 1, so a newer Maestro placed it; run the install one-liner in INSTALL.md, which runs the newest installer" ;;
    *) awk -F'\t' -v OFS='\t' '$1=="d"{$2=".maestro/skills/" $2} {print}' "$manifest" > "$prev" ;;
  esac
  bad=$(awk -F'\t' '$1=="d"{p=$2} $1=="f"||$1=="l"{p=$3} $1!~/^[dfl]$/{next}
    (p ~ /(^|\/)\.\.(\/|$)/ || p !~ /^(\.maestro\/|\.(claude|agents)\/skills\/maestro-)./) && n++ < 20 {print "  " p}' "$prev")
  [ -z "$bad" ] || die "$manifest names paths outside .maestro/, .claude/skills/maestro-* and .agents/skills/maestro-*; Maestro won't touch them:"$'\n'"$bad"
  units "$prev" | { if [ -n "$linked" ]; then grep -v "^$linked/" || true; else cat; fi; } > "$stage/old-units"
elif [ "$mode" = uninstall ]; then
  die "no $manifest here; nothing Maestro placed to remove"
else
  : > "$stage/old-units"
fi

echo "format 1" > "$stage/plan"
if [ "$mode" = install ]; then
  for d in .maestro/skills $harnesses; do mkdir -p "$stage/new/$d"; done
  for d in "$here"/skills/maestro-*/; do
    n=$(basename "$d")
    [ -f "$d/SKILL.md" ] || continue
    cp -R "$here/skills/$n" "$stage/new/.maestro/skills/$n"
    for h in $harnesses; do
      ln -s "../../.maestro/skills/$n" "$stage/new/$h/$n"
      [ -L "$stage/new/$h/$n" ] || die "this shell can't make symlinks (on Windows, use WSL)"
    done
    printf 'd\t.maestro/skills/%s\n' "$n" >> "$stage/plan"
  done
  count=$(grep -c "^d$tab" "$stage/plan") || die "no maestro-* skills found in $here/skills"
  cp "$here/install.sh" "$stage/new/.maestro/install.sh"
  chmod +x "$stage/new/.maestro/install.sh"
  version=$(git -C "$here" rev-parse HEAD)
  printf '%s\n' "$version" > "$stage/new/.maestro/VERSION"
  printf '* -text -eol\n' > "$stage/new/.maestro/.gitattributes"
  (cd "$stage/new" && find . ! -type d | sed 's|^\./||' | LC_ALL=C sort | state) >> "$stage/plan"
  cp "$stage/plan" "$stage/new/$manifest"
fi
units "$stage/plan" > "$stage/new-units"
{ cat "$stage/old-units" "$stage/new-units"; echo "$manifest"; } | LC_ALL=C sort -u > "$stage/all"
parents < "$stage/all" > "$stage/dirs"

while IFS= read -r d; do
  if [ -L "$d" ]; then die "$d is a symlink; Maestro only installs into real folders"; fi
  if [ -e "$d" ] && [ ! -d "$d" ]; then die "$d exists and isn't a folder"; fi
done < "$stage/dirs"

if [ -f "$manifest" ]; then check_untouched; fi

clash=$(LC_ALL=C comm -13 "$stage/old-units" "$stage/new-units" | while IFS= read -r p; do
  if [ -e "$p" ] || [ -L "$p" ]; then echo "  $p"; fi
done)
[ -z "$clash" ] || die "these paths exist and Maestro didn't place them; rename or remove them first:"$'\n'"$clash"

if [ -f "$index" ]; then cp -p "$index" "$stage/index"; fi
while IFS= read -r p; do
  if [ -e "$stage/new/$p" ] || [ -L "$stage/new/$p" ]; then echo "$p"; fi
done < "$stage/all" > "$stage/incoming"
while IFS= read -r d; do
  mkdir -p "$stage/old/$d"
  if [ ! -d "$d" ]; then mkdir "$d"; created="$d $created"; fi
done < "$stage/dirs"
while IFS= read -r p; do swap_path "$p"; done < "$stage/all"

GIT_INDEX_FILE="$stage/index" stage_paths < "$stage/all"
cp -p "$stage/index" "$index_lock"
trap '' INT TERM HUP
mv -- "$index_lock" "$index"
finished=true
index_lock=
while IFS= read -r p; do
  if [ -n "$linked" ] && [ "${p%/*}" = "$harnesses" ]; then continue; fi
  if [ ! -e "$p" ] && [ ! -L "$p" ]; then rmdir -p "${p%/*}" 2>/dev/null || true; fi
done < "$stage/all"

if [ "$mode" = uninstall ]; then
  echo "maestro: removed and staged; commit to finish"
else
  echo "maestro: installed $count skills at $version and staged them"
  echo "maestro: commit .maestro/, .claude/skills/ and .agents/skills/ to your default branch"
fi
