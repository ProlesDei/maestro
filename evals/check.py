#!/usr/bin/env python3
"""Check the repo's Markdown against its formats: playbooks
(playbooks/README.md), golden tasks (evals/README.md) and skills
(skills/README.md); runner requests against docs/runner.md; that every
skill a playbook names exists; that ticket writes to a held ticket pass
the lease token; that links and backticked repo paths resolve; and that
README's diagram embeds match docs/*.mermaid.
Standard library only.

Run from anywhere: python3 evals/check.py
Exit 0 when clean, 1 when any check fails.
"""

import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PLAYBOOKS = os.path.join(ROOT, "playbooks")
EVALS = os.path.join(ROOT, "evals")
SKILLS = os.path.join(ROOT, "skills")
PROFILES = os.path.join(ROOT, "bot-profiles")
RUNNER = os.path.join(ROOT, "docs", "runner.md")
TICKET_SUBCOMMANDS = {"get", "list", "events", "doctor", "create", "comment",
                      "transition"}

TRIGGERS = {"request", "sent-back", "new-work", "scheduled"}
STATUSES = {"backlog", "todo", "in_progress", "blocked", "in_review", "in_qa",
            "ready_to_release"}
TYPES = {"feature", "bug", "chore", "docs", "investigation"}
ROLES = {"harness", "runner", "bot"}
MODELS = {"Frontier", "Mid-tier", "No tokens", "Bot"}
SECTIONS = ["Goal", "When to use", "Steps", "Stop and block when", "Wrap-up"]
EVAL_SECTIONS = ["Setup", "Input", "Expected", "Must not", "Checks"]
FIELD_ORDER = ["Goal", "Role", "Skill", "Model", "Run", "Skip when",
               "Done when", "Evidence"]
REQUIRED_FIELDS = ["Goal", "Role", "Skill", "Model", "Done when", "Evidence"]

# Playbooks allowed to set `done` at wrap-up. Ship tickets: only the
# Operations Manager (brief). Scout tickets: their owner (decision 0009).
DONE_ALLOWED = {
    "operations-manager/release",
    "operations-manager/infrastructure-request",
    "planner/plan-project",
    "planner/accept-report",
    "chief-of-staff/handle-escalation",
}

errors = []
warnings = []


def err(path, msg):
    errors.append(f"{os.path.relpath(path, ROOT)}: {msg}")


def warn(path, msg):
    warnings.append(f"{os.path.relpath(path, ROOT)}: {msg}")


def frontmatter(text):
    m = re.match(r"---\n(.*?)\n---\n", text, re.S)
    if not m:
        return None, text
    fields = {}
    for line in m.group(1).splitlines():
        if ":" in line:
            k, v = line.split(":", 1)
            fields[k.strip()] = v.strip()
    return fields, text[m.end():]


def sections(body):
    """Map '## Heading' -> section text."""
    out = {}
    parts = re.split(r"^## (.+)$", body, flags=re.M)
    for i in range(1, len(parts), 2):
        out[parts[i].strip()] = parts[i + 1]
    return out


def backticked(s):
    return re.findall(r"`([^`]+)`", s)


def runner_commands():
    """Request names from the Requests table in docs/runner.md, without
    their arguments: `worktree add <id> [...]` -> "worktree add"."""
    text = open(RUNNER).read()
    sec = sections(text).get("Requests", "")
    names = set()
    for line in sec.splitlines():
        if line.startswith("| `"):
            for c in backticked(line.split("|")[1]):
                words = []
                for w in c.split():
                    if w.startswith(("<", "[", "--")):
                        break
                    words.append(w)
                if words:
                    names.add(" ".join(words))
    if not names:
        err(RUNNER, "no Requests table")
    return names


def runner_request_ok(seg, names):
    return any(seg == n or seg.startswith(n + " ") for n in names)


def skill_kind(name):
    for kind in ("harness", "bot"):
        if os.path.exists(os.path.join(SKILLS, kind, name, "SKILL.md")):
            return kind
    return None


def check_step(path, num, title, text, cmds, skills_used):
    fields = []
    values = {}
    for line in text.splitlines():
        m = re.match(r"- \*\*([^*]+):\*\* ?(.*)$", line)
        if m:
            fields.append(m.group(1))
            values[m.group(1)] = m.group(2).strip()
    where = f"step {num} ({title})"
    for f in REQUIRED_FIELDS:
        if f not in values or not values[f]:
            err(path, f"{where}: missing **{f}:**")
    unknown = [f for f in fields if f not in FIELD_ORDER]
    for f in unknown:
        err(path, f"{where}: unknown field **{f}:**")
    known = [f for f in fields if f in FIELD_ORDER]
    if known != sorted(known, key=FIELD_ORDER.index):
        err(path, f"{where}: fields out of order: {', '.join(known)}")
    role = values.get("Role", "")
    skill_v = backticked(values.get("Skill", ""))
    model = values.get("Model", "").split(",")[0].strip()
    if role not in ROLES:
        err(path, f"{where}: Role must be one of {sorted(ROLES)}, got {role!r}")
    if len(skill_v) != 1:
        err(path, f"{where}: Skill must name exactly one skill in backticks")
        skill = None
    else:
        skill = skill_v[0]
    if model not in MODELS:
        err(path, f"{where}: Model must start with one of {sorted(MODELS)}")
    if role == "runner":
        if skill != "runner":
            err(path, f"{where}: runner steps use Skill `runner`")
        if model != "No tokens":
            err(path, f"{where}: runner steps are Model No tokens")
        if "Run" not in values:
            err(path, f"{where}: runner steps need **Run:**")
        else:
            segs = [s for s in backticked(values["Run"]) if not s.startswith("<")]
            if not segs:
                err(path, f"{where}: Run names no command")
            for s in segs:
                if not runner_request_ok(s, cmds):
                    err(path, f"{where}: runner request {s!r} "
                              "is not in docs/runner.md")
                elif s.split()[0] == "ticket" and (
                        len(s.split()) < 2 or s.split()[1] not in TICKET_SUBCOMMANDS):
                    err(path, f"{where}: runner can't run {s!r}")
    else:
        if "Run" in values:
            err(path, f"{where}: only runner steps have **Run:**")
    if role == "harness":
        if skill and skill_kind(skill) != "harness":
            err(path, f"{where}: harness steps name a skill in skills/harness/")
        if model not in {"Frontier", "Mid-tier"}:
            err(path, f"{where}: harness steps are Frontier or Mid-tier")
    if role == "bot":
        if skill and skill != "none" and skill_kind(skill) != "bot":
            err(path, f"{where}: bot steps name a skill in skills/bot/ or `none`")
        if model != "Bot":
            err(path, f"{where}: bot steps are Model Bot")
    if skill == "none" and role != "bot":
        err(path, f"{where}: Skill `none` is only for bot decisions")
    if skill and skill != "none":
        skills_used.setdefault(skill, []).append(f"{os.path.relpath(path, PLAYBOOKS)} {num}")


def check_playbook(path, role_dir, cmds, skills_used):
    text = open(path).read()
    fm, body = frontmatter(text)
    name = os.path.basename(path)[:-3]
    key = f"{role_dir}/{name}"
    if fm is None:
        err(path, "no frontmatter")
        return key, None
    for f in ["name", "description", "role", "trigger", "claims", "types"]:
        if not fm.get(f):
            err(path, f"frontmatter missing {f}")
    if fm.get("name") != name:
        err(path, f"name {fm.get('name')!r} doesn't match the file name")
    if ": " in fm.get("description", "") and fm["description"][0] not in "\"'":
        err(path, "description has ': ', which YAML can't read unquoted")
    if fm.get("role") != role_dir:
        err(path, f"role {fm.get('role')!r} doesn't match the folder")
    if not os.path.exists(os.path.join(PROFILES, role_dir, "persona.md")):
        err(path, f"no bot profile for role {role_dir}")
    if fm.get("trigger") not in TRIGGERS:
        err(path, f"trigger must be one of {sorted(TRIGGERS)}")
    claims = fm.get("claims")
    if claims != "none" and claims not in STATUSES:
        err(path, f"claims must be none or a claimable status, got {claims!r}")
    types = [t.strip() for t in fm.get("types", "").split(",")]
    if types not in (["any"], ["none"]) and not set(types) <= TYPES:
        err(path, f"types must be any, none, or from {sorted(TYPES)}")
    if (claims == "none") != (types == ["none"]):
        err(path, "types is none exactly when claims is none")

    secs = sections(body)
    for s in SECTIONS:
        if s not in secs:
            err(path, f"missing section ## {s}")
    steps = re.split(r"^### (\d+)\. (.+)$", secs.get("Steps", ""), flags=re.M)
    if len(steps) < 4:
        err(path, "no steps")
    expect = 1
    for i in range(1, len(steps), 3):
        num, title, stext = int(steps[i]), steps[i + 1].strip(), steps[i + 2]
        if num != expect:
            err(path, f"step {num} should be numbered {expect}")
        expect = num + 1
        check_step(path, num, title, stext, cmds, skills_used)

    wrap = secs.get("Wrap-up", "")
    for s in re.findall(r"^- Skill: (.+)$", wrap, flags=re.M):
        for sk in backticked(s):
            if re.fullmatch(r"[a-z][a-z0-9-]*", sk):
                skills_used.setdefault(sk, []).append(f"{key}.md wrap-up")
    # Harness skills named in prose (for example `maestro-escalate`).
    for sk in set(backticked(text)):
        if re.fullmatch(r"maestro-[a-z-]+", sk) and sk != "maestro-ticket":
            skills_used.setdefault(sk, []).append(f"{key}.md prose")
    if not re.search(r"^- Skill: `", wrap, flags=re.M):
        err(path, "Wrap-up must name its skill (`- Skill: ...`)")
    for line in wrap.splitlines():
        if line.startswith("- Status:") and "`done`" in line and key not in DONE_ALLOWED:
            err(path, "only the Operations Manager (ship) or a scout's owner sets done")
    if "Tier:" in text:
        err(path, "old **Tier:** line; use **Model:**")
    return key, fm


def check_index(found):
    path = os.path.join(PLAYBOOKS, "README.md")
    sec = sections(open(path).read()).get("Index", "")
    rows = {}
    for line in sec.splitlines():
        m = re.match(r"\| \[`([^`]+)`\]\(([^)]+)\) \| ([^|]+) \| ([^|]+) \| ([^|]+) \| ([^|]+) \|", line)
        if m:
            rows[m.group(1)] = [c.strip() for c in m.groups()[2:]]
    for key, fm in found.items():
        if key not in rows:
            err(path, f"index is missing {key}")
            continue
        if fm and rows[key] != [fm.get("role"), fm.get("trigger"), fm.get("claims"), fm.get("types")]:
            err(path, f"index row for {key} doesn't match its frontmatter")
    for key in rows:
        if key not in found:
            err(path, f"index lists {key}, which doesn't exist")


def check_evals(found):
    tasks = {}
    for role in sorted(os.listdir(EVALS)):
        d = os.path.join(EVALS, role)
        if not os.path.isdir(d) or role.startswith(("_", ".")) or role == "fixtures":
            continue
        for f in sorted(os.listdir(d)):
            if not f.endswith(".md") or f.startswith("_"):
                continue
            path = os.path.join(d, f)
            fm, body = frontmatter(open(path).read())
            if fm is None:
                err(path, "no frontmatter")
                continue
            pb = fm.get("playbook")
            if pb not in found:
                err(path, f"playbook {pb!r} doesn't exist")
            if not f.startswith(os.path.basename(pb or "") ):
                err(path, "file name must start with the playbook name")
            if fm.get("status") not in {"draft", "passing", "failing"}:
                err(path, "status must be draft, passing or failing")
            if not fm.get("fixture"):
                err(path, "frontmatter missing fixture")
            secs = sections(body)
            for s in EVAL_SECTIONS:
                if s not in secs:
                    err(path, f"missing section ## {s}")
            tasks.setdefault(pb, []).append(fm.get("status"))
    for key in found:
        if key not in tasks:
            err(os.path.join(EVALS, key + ".md"), "playbook has no golden task")
    return tasks


def markdown_files():
    """Every Markdown file outside .git, tools/ and the decision log
    (history), and the design handoff brief (kept as written, 0409)."""
    out = []
    for dirpath, dirs, files in os.walk(ROOT):
        rel = os.path.relpath(dirpath, ROOT)
        dirs[:] = [d for d in dirs if d != ".git"
                   and os.path.normpath(os.path.join(rel, d)) not in ("tools", "docs/decisions")]
        for f in files:
            if f.endswith(".md") and f != "HANDOFF.md":
                out.append(os.path.join(dirpath, f))
    return sorted(out)


REPO_PATH = re.compile(r"`((?:bot-profiles|skills|docs|playbooks|tools|evals)/[^`\s]*"
                       r"|README\.md|AGENTS\.md|CLAUDE\.md)`")


def check_links():
    links = paths = 0
    for path in markdown_files():
        dirpath = os.path.dirname(path)
        text = open(path).read()
        for m in REPO_PATH.finditer(text):
            target = m.group(1).rstrip("/.,")
            if re.search(r"[<*{]|\.\.\.|…", target):
                continue
            paths += 1
            if not os.path.exists(os.path.join(ROOT, target)):
                err(path, f"missing repo path: {target}")
        text = re.sub(r"`[^`\n]*`", "", text)  # links inside code spans aren't links
        for target in re.findall(r"\]\(([^)\s]+)\)", text):
            if re.match(r"[a-z]+:", target) or target.startswith("#"):
                continue
            links += 1
            rel = target.split("#", 1)[0]
            if not os.path.exists(os.path.normpath(os.path.join(dirpath, rel))):
                err(path, f"broken link: {target}")
    return links, paths


def check_embeds():
    path = os.path.join(ROOT, "README.md")
    readme = open(path).read()
    for src in ("docs/team.mermaid", "docs/bot-anatomy.mermaid"):
        marker = f"<!-- Source: {src}. Edit that file, then copy it here. -->"
        m = re.search(re.escape(marker) + r"\n```mermaid\n(.*?)```", readme, re.S)
        if not m or m.group(1) != open(os.path.join(ROOT, src)).read():
            err(path, f"embed differs from {src}")


def check_skills():
    count = 0
    for kind in ("bot", "harness"):
        d = os.path.join(SKILLS, kind)
        for name in sorted(os.listdir(d)):
            path = os.path.join(d, name, "SKILL.md")
            if not os.path.exists(path):
                continue
            count += name[0] != "_"
            text = open(path).read()
            m = re.match(r"---\n(.*?)\n---\n", text, re.S)
            if not m:
                err(path, "no frontmatter")
                continue
            fm = m.group(1) + "\n"
            fields, _ = frontmatter(text)
            if name[0] != "_" and fields.get("name") != name:
                err(path, "name doesn't match the folder")
            desc = fields.get("description", "")
            if not desc:
                err(path, "no description")
            elif ": " in desc and desc[0] not in "\"'":
                err(path, "description has ': ', which YAML can't read unquoted")
            if "metadata:\n  internal: true\n" not in fm:
                err(path, "not marked metadata.internal")
            if kind == "harness":
                if "disable-model-invocation: true" not in fm:
                    err(path, "harness skill without disable-model-invocation")
                if name[0] != "_":
                    if not name.startswith("maestro-"):
                        err(path, "harness skill without the maestro- prefix")
                    if not os.path.exists(os.path.join(d, name, "agents", "openai.yaml")):
                        err(path, "harness skill without agents/openai.yaml")
            elif name.startswith("maestro-"):
                err(path, "bot skill with the maestro- prefix")
    return count


TICKET_WRITE = re.compile(r"^(?:[A-Z_]+=\S*\s+)*(?:maestro-)?ticket\s+(?:--\S+\s+)*"
                          r"(transition|comment|create|release|heartbeat|keepalive|update|claim)\b")
# Fewer writes found than this means the check lost track of some; lower it
# only when a write was really removed.
MIN_TICKET_WRITES = 35
# Writes that need the lease token even when the line says nobody holds the
# ticket: they only act on a lease (0221).
LEASE_ONLY = {"release", "keepalive"}
STORE_TYPES = {"ship", "scout"}
UNHELD = re.compile(r"nobody holds|unclaimed|no live lease|live lease alone")


def ticket_write_files():
    for root in (PLAYBOOKS, SKILLS, PROFILES):
        for dirpath, dirs, files in os.walk(root):
            for f in files:
                if f.endswith(".md"):
                    yield os.path.join(dirpath, f)


def check_ticket_writes():
    """Writes to a held ticket carry the lease token (decisions 0203, 0431,
    0434, 0221), and every claim starts its keepalive (0240). Reply evidence
    lines (`... → exit 0`) are summaries, not commands."""
    count = 0
    for path in sorted(ticket_write_files()):
        fence = False
        lines = re.sub(r"\\\n\s*", " ", open(path).read()).splitlines()
        for n, line in enumerate(lines, 1):
            if line.lstrip().startswith("```"):
                fence = not fence
                continue
            if fence:
                if "→" in line:
                    continue
                spans = [line.strip().lstrip("- ")]
            else:
                spans = backticked(line)
            for span in spans:
                m = TICKET_WRITE.match(span)
                if not m:
                    continue
                count += 1
                where = f"line {n}: `{span[:60]}`"
                if "--add-try" in span:
                    err(path, f"{where}: no --add-try; the store counts tries (0432)")
                for t in re.findall(r"--type (\S+)", span):
                    if t not in STORE_TYPES and not t.startswith("<"):
                        err(path, f"{where}: --type {t}; the store takes ship or scout (0430)")
                if m.group(1) == "create":
                    if re.search(r"--type scout\b", span) and "--owner" not in span:
                        err(path, f"{where}: a scout ticket needs --owner (0202)")
                    continue
                if m.group(1) == "claim":
                    for flag in ("--holder", "--keepalive"):
                        if not re.search(re.escape(flag) + r"\b", span):
                            err(path, f"{where}: a claim needs {flag}; nothing else "
                                      "heartbeats (0240)")
                    continue
                if m.group(1) == "heartbeat":
                    if "--" in span or "Never" not in line:
                        err(path, f"{where}: sessions never heartbeat; the claim's "
                                  "--keepalive does (0240)")
                    continue
                if m.group(1) == "comment" and "--expect-version" in span:
                    err(path, f"{where}: comment takes no --expect-version")
                lease_only = m.group(1) in LEASE_ONLY
                if "--" not in span and not lease_only:
                    if not UNHELD.search(line):
                        err(path, f"{where}: say it's for tickets nobody holds, "
                                  "or give the flags with --holder and --token")
                    continue
                if UNHELD.search(line) and "--token" not in span and not lease_only:
                    continue
                for flag in ("--holder", "--token"):
                    if not re.search(re.escape(flag) + r"\b", span):
                        err(path, f"{where}: {m.group(1)} on a held ticket needs {flag} (0431, 0221)")
                to = re.search(r"--to (\S+)", span)
                if to and (to.group(1) == "done" or to.group(1).startswith("<")) \
                        and "--actor-role" not in span:
                    err(path, f"{where}: a move that can be done needs --actor-role (0431)")
    return count


def main():
    cmds = runner_commands()
    skills_used = {}
    found = {}
    for role in sorted(os.listdir(PLAYBOOKS)):
        d = os.path.join(PLAYBOOKS, role)
        if not os.path.isdir(d) or role.startswith("_"):
            continue
        for f in sorted(os.listdir(d)):
            if f.endswith(".md") and not f.startswith("_"):
                key, fm = check_playbook(os.path.join(d, f), role, cmds, skills_used)
                found[key] = fm
    check_index(found)
    tasks = check_evals(found)
    links, paths = check_links()
    check_embeds()
    nskills = check_skills()
    nwrites = check_ticket_writes()
    if nwrites < MIN_TICKET_WRITES:
        err(os.path.join(EVALS, "check.py"), f"found {nwrites} ticket writes, "
            f"fewer than MIN_TICKET_WRITES ({MIN_TICKET_WRITES})")

    missing = [s for s in skills_used if not skill_kind(s)]
    for skill in sorted(missing):
        err(os.path.join(PLAYBOOKS, skills_used[skill][0].split()[0]),
            f"skill {skill!r} isn't in skills/bot/ or skills/harness/")

    passing = sum(1 for v in tasks.values() if "passing" in v)
    print(f"playbooks: {len(found)}  golden tasks: {sum(len(v) for v in tasks.values())} "
          f"(passing {passing})  skills: {nskills}  skills named: {len(skills_used)} "
          f"(missing {len(missing)})  links: {links}  repo paths: {paths}  "
          f"ticket writes: {nwrites}")
    for w in warnings:
        print("warn:", w)
    for e in errors:
        print("FAIL:", e)
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
