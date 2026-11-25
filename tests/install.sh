#!/usr/bin/env bash
set -u

repo=$(cd "$(dirname "$0")/.." && pwd -P)
s=$(mktemp -d)
trap 'rm -rf "$s"' EXIT
export HOME=$s GIT_CONFIG_NOSYSTEM=1 GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
fail=0
ok() { echo "ok   $*"; }
bad() { echo "FAIL $*"; sed 's/^/     /' "$s/out"; fail=1; }

git clone -q --no-local "$repo" "$s/src" || exit 1
rm -rf "$s/src/skills" && cp -R "$repo/skills" "$repo/install.sh" "$s/src/"
git -C "$s/src" add -A && git -C "$s/src" commit -q --allow-empty -m "working tree"
git init -q --bare "$s/remote.git" && git -C "$s/src" push -q "$s/remote.git" HEAD:refs/heads/main
git -C "$s/remote.git" config uploadpack.allowAnySHA1InWant true
export MAESTRO_REPO=file://$s/remote.git
new=$(git -C "$s/src" rev-parse HEAD)

fresh() { rm -rf "$s/t" && git init -q "$s/t" && git -C "$s/t" commit -q --allow-empty -m init; }
inst() { (cd "$s/t" && bash "$s/src/install.sh" "$@") > "$s/out" 2>&1; }
upd() { (cd "$s/t" && bash .maestro/install.sh "$@") > "$s/out" 2>&1; }
commit() { git -C "$s/t" commit -q --allow-empty -m step > /dev/null; }
tracked() { git -C "$s/t" ls-files -s; }
look() {
  (cd "$s/t" && git status --porcelain --ignored && find . -path ./.git -prune -o -print | LC_ALL=C sort &&
    find .git -maxdepth 1 -name 'maestro-stage*')
}
refuses() {
  local name=$1 before rc; shift
  before=$(look); "$@"; rc=$?
  if [ "$rc" = 1 ] && [ "$before" = "$(look)" ]; then ok "refuses: $name"; else bad "refuses: $name (exit $rc or files changed)"; fi
}

fresh
if inst && commit && [ "$(cat "$s/t/.maestro/VERSION")" = "$new" ] && [ -z "$(git -C "$s/t" status --porcelain)" ] &&
  [ -f "$s/t/.claude/skills/maestro-planner/SKILL.md" ] && [ -L "$s/t/.agents/skills/maestro-planner" ]; then
  ok "fresh install"
else bad "fresh install"; fi
tracked > "$s/want"

if upd && commit && [ "$(tracked)" = "$(cat "$s/want")" ]; then ok "update to the same version"; else bad "update to the same version"; fi

for r in 40beeee f2d5758 4ebafc4 6e7ed26; do
  fresh
  sha=$(git -C "$s/src" rev-parse "$r^{commit}")
  if inst --ref "$sha" && commit && upd && commit && [ "$(tracked)" = "$(cat "$s/want")" ] &&
    [ -z "$(git -C "$s/t" status --porcelain)" ]; then ok "update from $r"; else bad "update from $r"; fi
done

if upd --uninstall && commit && [ -z "$(tracked)" ] && [ "$(ls -A "$s/t")" = .git ]; then
  ok "uninstall leaves nothing"
else bad "uninstall leaves nothing"; fi

fresh
mkdir "$s/t/.maestro" && echo "appId: x" > "$s/t/.maestro/login.yaml" && echo '*.yaml text' > "$s/t/.gitattributes"
git -C "$s/t" add -A && commit
attr() { git -C "$s/t" check-attr text -- "$1" | sed 's/.*: //'; }
if inst && commit && [ "$(attr .maestro/login.yaml)" = set ] && [ "$(attr .maestro/skills/maestro-planner/SKILL.md)" = unset ] &&
  upd && commit && upd --uninstall && commit && [ "$(git -C "$s/t" ls-files)" = "$(printf '.gitattributes\n.maestro/login.yaml')" ] &&
  [ "$(ls -A "$s/t/.maestro")" = login.yaml ]; then
  ok "shares .maestro/ with the team's own files"
else bad "shares .maestro/ with the team's own files"; fi

fresh
mkdir -p "$s/t/.claude/skills/maestro-planner" && echo x > "$s/t/.claude/skills/maestro-planner/SKILL.md"
git -C "$s/t" add -A && commit
refuses "foreign path clash" inst

fresh && git -C "$s/t" config core.symlinks false
refuses "core.symlinks=false" inst

fresh && mkdir "$s/t/app" && echo x > "$s/t/app/a" && git -C "$s/t" add -A && commit
git -C "$s/t" sparse-checkout set --cone app
refuses "sparse checkout without .maestro/" inst
git -C "$s/t" sparse-checkout add .maestro .claude .agents
if inst && commit; then ok "sparse checkout with .maestro/ installs"; else bad "sparse checkout with .maestro/ installs"; fi

fresh && inst && commit
echo extra >> "$s/t/.maestro/skills/maestro-planner/SKILL.md"
refuses "edited file" upd
git -C "$s/t" checkout -q -- .maestro
touch "$s/t/.git/index.lock"
refuses "index.lock" upd
rm -f "$s/t/.git/index.lock"
{ echo "format 9"; tail -n +2 "$s/t/.maestro/manifest"; } > "$s/m" && mv "$s/m" "$s/t/.maestro/manifest"
refuses "newer manifest format" upd

exit "$fail"
