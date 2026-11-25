#!/usr/bin/env bash
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.." || exit 1
fail=0
bad() { echo "FAIL $*"; fail=1; }

scripts=$(git ls-files --cached --others --exclude-standard '*.sh' | while read -r f; do [ -f "$f" ] && echo "$f"; done)
for f in $scripts; do bash -n "$f" || bad "bash -n $f"; done
if command -v shellcheck >/dev/null 2>&1; then
  shellcheck -S warning $scripts || bad shellcheck
else
  echo "skip shellcheck: not installed"
fi

if git ls-files -z | xargs -0 grep -lP '[\x{200B}-\x{200F}\x{202A}-\x{202E}\x{2066}-\x{2069}\x{FEFF}]' 2>/dev/null; then
  bad "zero-width or bidi characters in the files above"
fi

python3 -I - <<'EOF' || fail=1
import os, re, subprocess, sys
files = [f for f in subprocess.run(['git', 'ls-files', '--cached', '--others', '--exclude-standard', '*.md'], capture_output=True, text=True).stdout.split() if os.path.isfile(f)]
def anchors(path):
    out = set()
    for line in open(path, encoding='utf-8'):
        m = re.match(r'#+\s+(.*)', line)
        if m:
            a = re.sub(r'[^\w\- ]', '', m.group(1).strip().lower()).replace(' ', '-')
            out.add(a)
    return out
bad = 0
for f in files:
    text = open(f, encoding='utf-8').read()
    for target in re.findall(r'\]\(([^)\s]+)\)', text):
        if re.match(r'[a-z]+:', target):
            continue
        path, _, anchor = target.partition('#')
        full = os.path.normpath(os.path.join(os.path.dirname(f), path)) if path else f
        if not os.path.exists(full):
            print(f'FAIL {f}: missing link {target}'); bad = 1
        elif anchor and full.endswith('.md') and anchor not in anchors(full):
            print(f'FAIL {f}: missing anchor {target}'); bad = 1
for d in sorted(os.listdir('skills')):
    skill = os.path.join('skills', d, 'SKILL.md')
    if not os.path.isfile(skill):
        continue
    head = open(skill, encoding='utf-8').read().split('---')[1]
    if not d.startswith('maestro-') or f'name: {d}\n' not in head:
        print(f'FAIL {skill}: name must be {d} and start with maestro-'); bad = 1
    yaml = os.path.join('skills', d, 'agents', 'openai.yaml')
    manual = 'disable-model-invocation: true' in head
    implicit_off = os.path.isfile(yaml) and 'allow_implicit_invocation: false' in open(yaml).read()
    if manual != implicit_off:
        print(f'FAIL {skill}: disable-model-invocation and openai.yaml allow_implicit_invocation disagree'); bad = 1
sys.exit(bad)
EOF

[ "$fail" = 0 ] && echo "ok: all checks passed"
exit "$fail"
