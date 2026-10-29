#!/usr/bin/env python3
"""Check what maestro-play lint can't: golden tasks (evals/README.md) and
that every playbook in playbooks/<role>/<job>.yaml has one; that no
Markdown playbook is left; that skills/ is the user's flat slot
(skills/README.md), each SKILL.md with frontmatter a harness can parse
and, if manual-only, Codex's policy file (decisions 0785, 0786); that no
persona, playbook or prompt names a skill; that links and backticked
repo paths resolve; and that README's diagram embeds match
docs/*.mermaid. Playbook steps, templates, runner requests,
ticket writes and skills/use.yaml are maestro-play lint's (decision 0760).
Standard library only.

Run from anywhere: python3 evals/check.py
Exit 0 when clean, 1 when any check fails.
"""

import os
import re
import sys

EVAL_SECTIONS = ["Setup", "Input", "Expected", "Must not", "Checks"]
STATUSES = {"draft", "passing", "failing"}
SKILL_NAME = re.compile(r"[a-z0-9]+(?:-[a-z0-9]+)*")
# Files in skills/ that aren't skills.
SKILLS_FILES = {"README.md", "use.yaml"}
# The only skills/ paths Maestro's own files may name.
SKILLS_PATHS = {"skills/", "skills/README.md", "skills/use.yaml"}
# The ways a harness or a prompt names a skill: a path into a skill folder,
# or /name and $name in a prompt (docs/runner.md).
SKILL_PATH = re.compile(r"(?<![\w.-])(?:\.claude/|\.agents/)?skills/[\w./-]*")
EMBEDS = ("docs/team.mermaid", "docs/bot-anatomy.mermaid")


class Check:
    def __init__(self, root):
        self.root = root
        self.errors = []

    def path(self, *parts):
        return os.path.join(self.root, *parts)

    def err(self, path, msg):
        self.errors.append(f"{os.path.relpath(path, self.root)}: {msg}")

    def read(self, path):
        with open(path, encoding="utf-8") as f:
            return f.read()

    # Playbooks

    def role_dirs(self):
        d = self.path("playbooks")
        if not os.path.isdir(d):
            return []
        return [r for r in sorted(os.listdir(d))
                if os.path.isdir(os.path.join(d, r))
                and not r.startswith(("_", ".")) and r != "prompts"]

    def playbooks(self):
        """`<role>/<job>` for every playbooks/<role>/<job>.yaml."""
        found = []
        for role in self.role_dirs():
            d = self.path("playbooks", role)
            if not os.path.exists(self.path("bot-profiles", role, "persona.md")):
                self.err(d, f"no bot profile for role {role}")
            for f in sorted(os.listdir(d)):
                if f.startswith("_"):
                    continue
                if f.endswith(".yaml"):
                    found.append(f"{role}/{f[:-5]}")
                elif f.endswith(".md"):
                    self.err(os.path.join(d, f),
                             "Markdown playbook; playbooks are YAML now (0700)")
        if not found:
            self.err(self.path("playbooks"), "no playbooks/<role>/<job>.yaml found")
        return found

    # Golden tasks

    def golden_tasks(self, found):
        evals = self.path("evals")
        tasks = {}
        for role in sorted(os.listdir(evals)):
            d = os.path.join(evals, role)
            if not os.path.isdir(d) or role.startswith(("_", ".")) or role == "fixtures":
                continue
            for f in sorted(os.listdir(d)):
                if not f.endswith(".md") or f.startswith("_"):
                    continue
                path = os.path.join(d, f)
                fm, body = frontmatter(self.read(path))
                if fm is None:
                    self.err(path, "no frontmatter")
                    continue
                pb = fm.get("playbook", "")
                if pb not in found:
                    self.err(path, f"playbook {pb!r} has no playbooks/{pb}.yaml")
                if pb.split("/")[0] != role:
                    self.err(path, "lives in the wrong role folder")
                name = pb.split("/")[-1]
                if f[:-3] != name and not f.startswith(name + "--"):
                    self.err(path, "file name must be <playbook>.md or <playbook>--<case>.md")
                if fm.get("status") not in STATUSES:
                    self.err(path, f"status must be one of {sorted(STATUSES)}")
                if not fm.get("fixture"):
                    self.err(path, "frontmatter missing fixture")
                secs = sections(body)
                for s in EVAL_SECTIONS:
                    if s not in secs:
                        self.err(path, f"missing section ## {s}")
                tasks.setdefault(pb, []).append(fm.get("status"))
        for key in found:
            if key not in tasks:
                self.err(self.path("evals", key + ".md"), "playbook has no golden task")
        return tasks

    # Skills

    def skills(self):
        """skills/ holds README.md, use.yaml and flat skill folders."""
        d = self.path("skills")
        names = []
        if not os.path.isdir(d):
            return names
        for name in sorted(os.listdir(d)):
            p = os.path.join(d, name)
            if name in SKILLS_FILES or name.startswith("."):
                continue
            if not os.path.isdir(p):
                self.err(p, "only README.md, use.yaml and skill folders go in skills/")
            elif not os.path.exists(os.path.join(p, "SKILL.md")):
                self.err(p, "not a skill: no SKILL.md (skills are flat, skills/<name>/SKILL.md)")
            elif not SKILL_NAME.fullmatch(name):
                self.err(p, "skill folder names are lowercase words joined by hyphens")
            else:
                self.skill_frontmatter(os.path.join(p, "SKILL.md"), name)
                names.append(name)
        return names

    def skill_frontmatter(self, path, name):
        """Harnesses read a skill's name and flags from YAML frontmatter.
        If the block doesn't parse, the harness drops all of it,
        disable-model-invocation included, and says nothing (0785)."""
        text = self.read(path)
        m = re.match(r"---\n(.*?)\n---\n", text, re.S)
        if not m:
            self.err(path, "no frontmatter")
            return
        fields, _ = frontmatter(text)
        if fields.get("name") != name:
            self.err(path, f"frontmatter name must be the folder name {name!r}")
        for n, why in yaml_flat_problems(m.group(1)):
            self.err(path, f"line {n + 1}: {why}; the frontmatter won't parse")
        # Codex ignores disable-model-invocation; a manual-only skill also
        # needs agents/openai.yaml (decisions 0143, 0786).
        if fields.get("disable-model-invocation", "").lower() == "true":
            oa = os.path.join(os.path.dirname(path), "agents", "openai.yaml")
            policy = re.search(r"^policy:[ \t]*\n((?:[ \t]+.*\n?)*)",
                               self.read(oa) if os.path.exists(oa) else "", re.M)
            if not (policy and re.search(r"^[ \t]+allow_implicit_invocation:[ \t]*false[ \t]*$",
                                         policy.group(1), re.M)):
                self.err(path, "manual-only skill needs agents/openai.yaml with "
                               "policy.allow_implicit_invocation: false (decision 0143)")

    def maestro_files(self):
        """Personas, team rules, playbooks and prompt templates."""
        for top in ("bot-profiles", "playbooks"):
            for dirpath, dirs, files in os.walk(self.path(top)):
                dirs.sort()
                for f in sorted(files):
                    if f.endswith((".md", ".yaml")):
                        yield os.path.join(dirpath, f)

    def skill_names(self, skills):
        """Maestro never depends on a skill name (0700 B3): no file it
        ships names a skill folder or calls a skill the harness's way."""
        tools = set()
        if os.path.isdir(self.path("tools")):
            tools = set(os.listdir(self.path("tools")))
        calls = None
        if skills:
            alt = "|".join(map(re.escape, skills))
            calls = re.compile(r"(?<![\w/.$-])[/$](" + alt + r")(?![\w-])")
        for path in self.maestro_files():
            for n, line in enumerate(self.read(path).splitlines(), 1):
                for m in SKILL_PATH.finditer(line):
                    if m.group(0).rstrip(".,") not in SKILLS_PATHS:
                        self.err(path, f"line {n}: names a skill path {m.group(0)!r}; "
                                       "list skills in skills/use.yaml instead")
                for m in re.finditer(r"(?<![\w/.-])maestro-[a-z][a-z-]*", line):
                    if m.group(0) not in tools:
                        self.err(path, f"line {n}: {m.group(0)!r} is a skill name, "
                                       "not a tool in tools/")
                if calls:
                    for m in calls.finditer(line):
                        self.err(path, f"line {n}: calls skill {m.group(1)!r}; "
                                       "list it in skills/use.yaml instead")

    # Links, paths and embeds

    def text_files(self):
        """Every Markdown and YAML file outside .git, tools/ and the
        decision log (history), and the design handoff brief (kept as
        written, 0409)."""
        out = []
        for dirpath, dirs, files in os.walk(self.root):
            rel = os.path.relpath(dirpath, self.root)
            dirs[:] = sorted(d for d in dirs if d != ".git" and
                             os.path.normpath(os.path.join(rel, d)) not in ("tools", "docs/decisions"))
            for f in sorted(files):
                if f.endswith((".md", ".yaml")) and f != "HANDOFF.md":
                    out.append(os.path.join(dirpath, f))
        return out

    def links(self):
        links = paths = 0
        for path in self.text_files():
            dirpath = os.path.dirname(path)
            text = self.read(path)
            for m in REPO_PATH.finditer(text):
                target = m.group(1).rstrip("/.,")
                if re.search(r"[<*{]|\.\.\.|…", target):
                    continue
                paths += 1
                if not os.path.exists(self.path(target)):
                    self.err(path, f"missing repo path: {target}")
            if not path.endswith(".md"):
                continue
            text = re.sub(r"`[^`\n]*`", "", text)  # links inside code spans aren't links
            for target in re.findall(r"\]\(([^)\s]+)\)", text):
                if re.match(r"[a-z]+:", target) or target.startswith("#"):
                    continue
                links += 1
                rel = target.split("#", 1)[0]
                if not os.path.exists(os.path.normpath(os.path.join(dirpath, rel))):
                    self.err(path, f"broken link: {target}")
        return links, paths

    def embeds(self):
        path = self.path("README.md")
        readme = self.read(path)
        for src in EMBEDS:
            marker = f"<!-- Source: {src}. Edit that file, then copy it here. -->"
            m = re.search(re.escape(marker) + r"\n```mermaid\n(.*?)```", readme, re.S)
            if not m or m.group(1) != self.read(self.path(src)):
                self.err(path, f"embed differs from {src}")

    def run(self):
        found = self.playbooks()
        tasks = self.golden_tasks(found)
        skills = self.skills()
        self.skill_names(skills)
        links, paths = self.links()
        self.embeds()
        passing = sum(1 for v in tasks.values() if "passing" in v)
        return (f"playbooks: {len(found)}  golden tasks: {sum(len(v) for v in tasks.values())} "
                f"(passing {passing})  skills: {len(skills)}  links: {links}  "
                f"repo paths: {paths}")


REPO_PATH = re.compile(r"`((?:bot-profiles|skills|docs|playbooks|tools|evals)/[^`\s]*"
                       r"|README\.md|AGENTS\.md|CLAUDE\.md)`")


YAML_INDICATORS = tuple("`@[]{}*&!%,?")


def yaml_flat_problems(block):
    """(line number, why) for the common ways a one-line-per-key YAML
    block fails to parse: a plain value with ': ', ' #' or a reserved
    first character, text after a closing quote, a duplicate key, or an
    indented line that isn't under a nested key or a | or > block."""
    seen = set()
    nested = False  # the last top-level key opened a map, list or block
    for n, line in enumerate(block.splitlines(), 1):
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        indented = line[0] in " \t"
        if indented and nested:
            continue
        if indented:
            yield n, "indented line under a key that already has a value"
            continue
        k, sep, v = line.partition(":")
        v = v.strip()
        if not sep:
            yield n, f"{line.strip()!r} is not a key: value line"
            continue
        if k in seen:
            yield n, f"duplicate key {k!r}"
        seen.add(k)
        nested = v == "" or v[:1] in "|>"
        if nested:
            continue
        if v[0] in "'\"":
            if not re.fullmatch(r"'(?:[^']|'')*'|\"(?:[^\"\\]|\\.)*\"", v):
                yield n, f"quote the whole {k} value, with nothing after the closing quote"
        elif v.startswith(YAML_INDICATORS) or ": " in v or v.endswith(":") or " #" in v:
            yield n, f"quote the {k} value"


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


def main():
    c = Check(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    print(c.run())
    for e in c.errors:
        print("FAIL:", e)
    return 1 if c.errors else 0


if __name__ == "__main__":
    sys.exit(main())
