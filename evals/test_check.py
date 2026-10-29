#!/usr/bin/env python3
"""Tests for evals/check.py. Run: python3 evals/test_check.py"""

import os
import shutil
import sys
import tempfile
import unittest

sys.dont_write_bytecode = True  # no __pycache__ in evals/
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import check  # noqa: E402

DIAGRAM = "flowchart TD\n    A --> B\n"
GOLDEN = """---
playbook: {pb}
fixture: tiny-cli
status: {status}
---

# Task

## Setup
## Input
## Expected
## Must not
## Checks
"""


class CheckTest(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.root)
        embeds = "".join(
            f"<!-- Source: {src}. Edit that file, then copy it here. -->\n"
            f"```mermaid\n{DIAGRAM}```\n" for src in check.EMBEDS)
        self.write("README.md", "# maestro\n\n" + embeds)
        for src in check.EMBEDS:
            self.write(src, DIAGRAM)
        self.write("bot-profiles/planner/persona.md", "---\nname: planner\n---\n\nYou plan.\n")
        self.write("bot-profiles/team-rules.md", "Start with `play`; `maestro-play` runs it.\n")
        self.write("playbooks/planner/groom-board.yaml", "name: groom-board\n")
        self.write("playbooks/prompts/groom.md", "Run `maestro-ticket list --json`.\n")
        self.write("evals/planner/groom-board.md",
                   GOLDEN.format(pb="planner/groom-board", status="draft"))
        self.write("evals/fixtures/README.md", "# Fixtures\n")
        self.write("skills/README.md", "# Skills\n")
        self.write("skills/use.yaml", "prompts:\n  all: []\n")
        for tool in ("maestro-ticket", "maestro-play"):
            os.makedirs(self.p("tools", tool))

    def p(self, *parts):
        return os.path.join(self.root, *parts)

    def write(self, rel, text):
        os.makedirs(os.path.dirname(self.p(rel)), exist_ok=True)
        with open(self.p(rel), "w") as f:
            f.write(text)

    def run_check(self):
        c = check.Check(self.root)
        summary = c.run()
        return c.errors, summary

    def assertFails(self, needle):
        errors, _ = self.run_check()
        self.assertTrue(any(needle in e for e in errors),
                        f"no error containing {needle!r} in {errors}")

    def test_clean_tree_passes(self):
        errors, summary = self.run_check()
        self.assertEqual(errors, [])
        self.assertIn("playbooks: 1  golden tasks: 1 (passing 0)  skills: 0", summary)

    # Playbooks and golden tasks

    def test_markdown_playbook_fails(self):
        self.write("playbooks/planner/old.md", "# Old\n")
        self.assertFails("playbooks/planner/old.md: Markdown playbook")

    def test_template_and_prompts_are_not_playbooks(self):
        self.write("playbooks/_template/playbook.yaml", "name: example-job\n")
        self.write("playbooks/prompts/_contract.md", "You are one step.\n")
        errors, summary = self.run_check()
        self.assertEqual(errors, [])
        self.assertIn("playbooks: 1 ", summary)

    def test_no_playbooks_fails(self):
        os.remove(self.p("playbooks/planner/groom-board.yaml"))
        self.assertFails("no playbooks/<role>/<job>.yaml found")

    def test_role_without_bot_profile_fails(self):
        self.write("playbooks/nobody/job.yaml", "name: job\n")
        self.write("evals/nobody/job.md", GOLDEN.format(pb="nobody/job", status="draft"))
        self.assertFails("no bot profile for role nobody")

    def test_playbook_without_golden_task_fails(self):
        self.write("playbooks/planner/plan-project.yaml", "name: plan-project\n")
        self.assertFails("evals/planner/plan-project.md: playbook has no golden task")

    def test_golden_task_for_missing_playbook_fails(self):
        self.write("evals/planner/gone.md", GOLDEN.format(pb="planner/gone", status="draft"))
        self.assertFails("has no playbooks/planner/gone.yaml")

    def test_golden_task_cases(self):
        self.write("evals/planner/groom-board--stale.md",
                   GOLDEN.format(pb="planner/groom-board", status="passing"))
        errors, summary = self.run_check()
        self.assertEqual(errors, [])
        self.assertIn("golden tasks: 2 (passing 1)", summary)

    def test_golden_task_name_must_match(self):
        self.write("evals/planner/groom.md",
                   GOLDEN.format(pb="planner/groom-board", status="draft"))
        self.assertFails("file name must be <playbook>.md")

    def test_golden_task_bad_status_and_section(self):
        self.write("evals/planner/groom-board.md",
                   GOLDEN.format(pb="planner/groom-board", status="green")
                   .replace("## Must not\n", ""))
        self.assertFails("status must be one of")
        self.assertFails("missing section ## Must not")

    # Skills

    def test_flat_skill_counts(self):
        self.write("skills/tdd/SKILL.md", "---\nname: tdd\n---\n")
        errors, summary = self.run_check()
        self.assertEqual(errors, [])
        self.assertIn("skills: 1 ", summary)

    def test_nested_skill_kinds_fail(self):
        self.write("skills/kinds/maestro-frob/SKILL.md", "---\nname: maestro-frob\n---\n")
        self.assertFails("skills/kinds: not a skill: no SKILL.md")

    def test_stray_file_in_skills_fails(self):
        self.write("skills/notes.txt", "x\n")
        self.assertFails("only README.md, use.yaml and skill folders")

    def test_persona_naming_old_skill_fails(self):
        self.write("bot-profiles/planner/persona.md", "Claim it (`maestro-frob`).\n")
        self.assertFails("'maestro-frob' is a skill name")

    def test_playbook_naming_skill_path_fails(self):
        self.write("playbooks/planner/groom-board.yaml",
                   "name: groom-board\n# Read .agents/skills/tdd/SKILL.md\n")
        self.assertFails("names a skill path '.agents/skills/tdd/SKILL.md'")

    def test_prompt_calling_user_skill_fails(self):
        self.write("skills/tdd/SKILL.md", "---\nname: tdd\n---\n")
        self.write("playbooks/prompts/build.md", "Use /tdd for this.\n")
        self.assertFails("calls skill 'tdd'")

    def test_prompt_calling_skill_codex_way_fails(self):
        self.write("skills/tdd/SKILL.md", "---\nname: tdd\n---\n")
        self.write("playbooks/prompts/build.md", "Use $tdd for this.\n")
        self.assertFails("calls skill 'tdd'")

    def test_bad_skill_folder_name_fails(self):
        self.write("skills/Deep_Review/SKILL.md", "---\nname: Deep_Review\n---\n")
        self.assertFails("skills/Deep_Review: skill folder names are lowercase")

    def test_skill_frontmatter_name_must_match_folder(self):
        self.write("skills/tdd/SKILL.md", "---\nname: test-first\n---\n")
        self.assertFails("frontmatter name must be the folder name 'tdd'")

    def test_unquoted_colon_in_skill_frontmatter_fails(self):
        self.write("skills/prune/SKILL.md",
                   "---\nname: prune\ndescription: Clean up dead weight: files and code.\n"
                   "disable-model-invocation: true\n---\n")
        self.assertFails("skills/prune/SKILL.md: line 3: quote the description value")

    def test_quoted_skill_frontmatter_passes(self):
        self.write("skills/prune/SKILL.md",
                   "---\nname: prune\ndescription: \"Clean up dead weight: files and code.\"\n"
                   "argument-hint: \"[path]\"\n---\n")
        errors, _ = self.run_check()
        self.assertEqual(errors, [])

    def test_manual_only_skill_needs_codex_policy(self):
        self.write("skills/work/SKILL.md",
                   "---\nname: work\ndisable-model-invocation: true\n---\n")
        self.assertFails("skills/work/SKILL.md: manual-only skill needs agents/openai.yaml")
        self.write("skills/work/agents/openai.yaml", "policy:\n  allow_implicit_invocation: true\n")
        self.assertFails("skills/work/SKILL.md: manual-only skill needs agents/openai.yaml")
        self.write("skills/work/agents/openai.yaml", "policy:\n  allow_implicit_invocation: false\n")
        errors, _ = self.run_check()
        self.assertEqual(errors, [])

    def test_frontmatter_that_wont_parse_fails(self):
        cases = {
            "argument-hint: `[ticket id]`\n": "line 3: quote the argument-hint value",
            "argument-hint: '[path]' or '--dry-run'\n": "line 3: quote the whole argument-hint value",
            "description: x\ndescription: y\n": "line 4: duplicate key 'description'",
            "disable-model-invocation: false\n  note: x: y\n": "line 4: indented line under a key",
            "argument-hint: [id]\n": "line 3: quote the argument-hint value",
        }
        for extra, needle in cases.items():
            with self.subTest(extra=extra):
                self.write("skills/tdd/SKILL.md", "---\nname: tdd\n" + extra + "---\n")
                self.assertFails("skills/tdd/SKILL.md: " + needle)

    def test_nested_and_block_frontmatter_passes(self):
        self.write("skills/tdd/SKILL.md",
                   "---\nname: tdd\ndescription: >\n  Test first: then fix.\n"
                   "metadata:\n  owner: paul\n  tags: [a, b]\n"
                   "argument-hint: 'it''s [id]'\n---\n")
        errors, _ = self.run_check()
        self.assertEqual(errors, [])

    def test_manual_only_flag_is_read_like_yaml(self):
        self.write("skills/work/SKILL.md",
                   "---\nname: work\ndisable-model-invocation: True\n---\n")
        self.assertFails("skills/work/SKILL.md: manual-only skill needs agents/openai.yaml")

    def test_codex_policy_must_sit_under_policy(self):
        self.write("skills/work/SKILL.md",
                   "---\nname: work\ndisable-model-invocation: true\n---\n")
        self.write("skills/work/agents/openai.yaml",
                   "policy:\n  allow_implicit_invocation: true\nother:\n  allow_implicit_invocation: false\n")
        self.assertFails("skills/work/SKILL.md: manual-only skill needs agents/openai.yaml")

    def test_skills_may_name_each_other(self):
        # skills/ is the user's: only Maestro's own files are kept skill-free.
        self.write("skills/tdd/SKILL.md", "---\nname: tdd\n---\nThen run /review.\n")
        self.write("skills/review/SKILL.md",
                   "---\nname: review\n---\nSee skills/tdd/SKILL.md and `maestro-pr-risk`.\n")
        errors, _ = self.run_check()
        self.assertEqual(errors, [])

    def test_skill_name_as_plain_word_is_fine(self):
        self.write("skills/review/SKILL.md", "---\nname: review\n---\n")
        self.write("playbooks/prompts/review.md",
                   "# Review\nReview the diff; see playbooks/prompts/review.md.\n")
        errors, _ = self.run_check()
        self.assertEqual(errors, [])

    def test_use_yaml_path_is_allowed(self):
        self.write("bot-profiles/team-rules.md", "`skills/` and `skills/use.yaml` are the user's.\n")
        errors, _ = self.run_check()
        self.assertEqual(errors, [])

    # Links, paths and embeds

    def test_missing_repo_path_fails(self):
        self.write("skills/README.md", "See `playbooks/prompts/nope.md`.\n")
        self.assertFails("missing repo path: playbooks/prompts/nope.md")

    def test_repo_path_in_yaml_is_checked(self):
        self.write("playbooks/planner/groom-board.yaml",
                   "name: groom-board\n# `playbooks/prompts/missing.md`\n")
        self.assertFails("groom-board.yaml: missing repo path")

    def test_broken_link_fails(self):
        self.write("evals/README.md", "[x](nope.md) and `[y](skip.md)`\n")
        errors, _ = self.run_check()
        self.assertEqual([e for e in errors if "broken link" in e],
                         ["evals/README.md: broken link: nope.md"])

    def test_embed_drift_fails(self):
        self.write("docs/team.mermaid", "flowchart LR\n")
        self.assertFails("embed differs from docs/team.mermaid")

    def test_decisions_and_tools_are_skipped(self):
        self.write("docs/decisions/0001-old.md", "`skills/gone/runner` and [x](gone.md)\n")
        self.write("tools/maestro-play/README.md", "[x](gone.md)\n")
        errors, _ = self.run_check()
        self.assertEqual(errors, [])


if __name__ == "__main__":
    unittest.main()
