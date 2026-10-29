# 0761 playbooks/README.md drops the playbook index

- **Context:** The old README kept an index table (role, trigger, claims, types) and `check.py` compared it to each playbook's frontmatter. Under 0700 those fields are the `intake:` header in each YAML file, which `maestro-play lint` reads.
- **Choice:** No index. The README holds the format and the shared ticket, tries and escalation rules; `ls playbooks/*/` and `maestro-play lint` list the playbooks.
- **Alternatives:** Keep the index and teach `check.py` to read `intake:` lines with a regex; have lint check the table.
- **Why:** The table restated every header, so it was a second copy to keep in step. One rule, one file.
