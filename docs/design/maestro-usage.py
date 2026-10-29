#!/usr/bin/env python3
"""maestro-usage: track harness usage, check budgets and trip circuit breakers.

Every check here is a plain script reading a local SQLite database, so it costs
no tokens. Bots call it as a gate and act on the exit code.

Commands
  run      Run a harness command, pass its output through, and record its usage.
  record   Record usage from harness output piped on stdin (or entered manually).
  check    Check budgets. Exit 0 = ok, 1 = warning, 2 = over budget.
  breaker  Check a role pool's recent failure rate. Exit 0 = ok, 2 = tripped.
  report   Summarise usage, grouped by ticket, workstream, role, model or harness.

Examples
  maestro-usage run --ticket T-12 --workstream auth --role junior-engineer \\
      -- claude -p "..." --output-format json
  maestro-usage run --ticket T-12 --role senior-engineer --model gpt-5.5 \\
      -- codex exec --json "..."
  maestro-usage check --ticket T-12 --workstream auth --role junior-engineer
  maestro-usage breaker --role junior-engineer
  maestro-usage report --by workstream --since 24h

Storage
  $(git rev-parse --git-common-dir)/maestro/usage.db, shared by every worktree.
  Override with MAESTRO_USAGE_DB=/path/to/usage.db.

Config
  .maestro/project/budgets.json at the repo root, or MAESTRO_BUDGETS=/path.
  See BUDGETS_EXAMPLE below for the format.
"""

import argparse
import hashlib
import json
import os
import sqlite3
import subprocess
import sys
import time
import uuid
from pathlib import Path

BUDGETS_EXAMPLE = {
    "warn_at": 0.8,
    "prices_per_million_tokens": {
        "example-model": {"input": 0.0, "cached_input": 0.0, "output": 0.0}
    },
    "global": {"usd_24h": 50, "tokens_24h": 50000000},
    "workstreams": {"auth": {"usd_total": 20}},
    "tickets_default": {"usd_total": 2},
    "tickets": {"T-12": {"usd_total": 5}},
    "roles": {"junior-engineer": {"usd_24h": 10}},
    "breaker": {"window": 20, "min_samples": 5, "max_failure_rate": 0.5},
}

SCHEMA = """
CREATE TABLE IF NOT EXISTS usage (
    id                 INTEGER PRIMARY KEY,
    recorded_at        REAL    NOT NULL,
    record_key         TEXT    NOT NULL UNIQUE,
    harness            TEXT    NOT NULL,
    session_id         TEXT,
    ticket             TEXT,
    workstream         TEXT,
    role               TEXT,
    bot                TEXT,
    model              TEXT,
    input_tokens       INTEGER NOT NULL DEFAULT 0,
    cached_tokens      INTEGER NOT NULL DEFAULT 0,
    output_tokens      INTEGER NOT NULL DEFAULT 0,
    cost_usd           REAL,
    cost_source        TEXT,
    status             TEXT    NOT NULL,
    duration_ms        INTEGER
);
CREATE INDEX IF NOT EXISTS usage_ticket     ON usage(ticket, recorded_at);
CREATE INDEX IF NOT EXISTS usage_workstream ON usage(workstream, recorded_at);
CREATE INDEX IF NOT EXISTS usage_role       ON usage(role, recorded_at);
CREATE INDEX IF NOT EXISTS usage_time       ON usage(recorded_at);
"""

EXIT_OK, EXIT_WARN, EXIT_OVER = 0, 1, 2


# ───────────────────────── storage ─────────────────────────

def repo_root():
    try:
        out = subprocess.run(["git", "rev-parse", "--show-toplevel"],
                             capture_output=True, text=True, check=True)
        return Path(out.stdout.strip())
    except (subprocess.CalledProcessError, FileNotFoundError):
        return Path.cwd()


def default_db_path():
    if os.environ.get("MAESTRO_USAGE_DB"):
        return Path(os.environ["MAESTRO_USAGE_DB"])
    try:
        out = subprocess.run(["git", "rev-parse", "--git-common-dir"],
                             capture_output=True, text=True, check=True)
        common = Path(out.stdout.strip())
        if not common.is_absolute():
            common = Path.cwd() / common
        return common / "maestro" / "usage.db"
    except (subprocess.CalledProcessError, FileNotFoundError):
        return Path.cwd() / ".maestro" / "local" / "usage.db"


def connect(path=None):
    path = Path(path or default_db_path())
    path.parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(path, timeout=30, isolation_level=None)
    conn.execute("PRAGMA journal_mode=WAL")
    conn.execute("PRAGMA synchronous=NORMAL")
    conn.execute("PRAGMA busy_timeout=30000")
    conn.executescript(SCHEMA)
    return conn


def load_budgets(path=None):
    path = path or os.environ.get("MAESTRO_BUDGETS")
    path = Path(path) if path else repo_root() / ".maestro" / "project" / "budgets.json"
    if not path.exists():
        return {}
    with open(path) as f:
        return json.load(f)


# ───────────────────────── parsing ─────────────────────────

def empty_usage():
    return {"session_id": None, "model": None, "input_tokens": 0,
            "cached_tokens": 0, "output_tokens": 0, "cost_usd": None,
            "cost_source": None, "status": "unparsed", "duration_ms": None}


def parse_claude_result(obj):
    """Claude Code `--output-format json`, or the final `result` event of stream-json."""
    u = empty_usage()
    u["session_id"] = obj.get("session_id")
    u["duration_ms"] = obj.get("duration_ms")
    u["status"] = "error" if obj.get("is_error") else "success"

    model_usage = obj.get("modelUsage") or {}
    if model_usage:
        # Prefer modelUsage: unlike `usage`, it includes subagent tokens.
        for stats in model_usage.values():
            u["input_tokens"] += stats.get("inputTokens", 0) + stats.get("cacheCreationInputTokens", 0)
            u["cached_tokens"] += stats.get("cacheReadInputTokens", 0)
            u["output_tokens"] += stats.get("outputTokens", 0)
        if len(model_usage) == 1:
            u["model"] = next(iter(model_usage))
        else:
            # Name the model that cost the most; helper models are usually cheap.
            u["model"] = max(model_usage, key=lambda m: model_usage[m].get("costUSD", 0))
    else:
        usage = obj.get("usage") or {}
        u["input_tokens"] = usage.get("input_tokens", 0) + usage.get("cache_creation_input_tokens", 0)
        u["cached_tokens"] = usage.get("cache_read_input_tokens", 0)
        u["output_tokens"] = usage.get("output_tokens", 0)

    if obj.get("total_cost_usd") is not None:
        u["cost_usd"] = float(obj["total_cost_usd"])
        u["cost_source"] = "harness-estimate"
    return u


def parse_codex_jsonl(lines):
    """Codex `exec --json`: sum usage across every turn.completed event."""
    u = empty_usage()
    saw_turn = False
    for obj in lines:
        kind = obj.get("type")
        if kind == "thread.started":
            u["session_id"] = obj.get("thread_id")
        elif kind == "turn.completed":
            saw_turn = True
            usage = obj.get("usage") or {}
            cached = usage.get("cached_input_tokens", 0)
            # Codex counts cached tokens inside input_tokens; store them separately.
            u["input_tokens"] += max(usage.get("input_tokens", 0) - cached, 0)
            u["cached_tokens"] += cached
            u["output_tokens"] += usage.get("output_tokens", 0)
        elif kind in ("turn.failed", "error"):
            u["status"] = "error"
    if saw_turn and u["status"] != "error":
        u["status"] = "success"
    return u


def parse_output(text):
    """Detect the harness format from its output. Returns (harness, usage)."""
    text = text.strip()
    if not text:
        return "unknown", empty_usage()

    try:
        obj = json.loads(text)
        if isinstance(obj, dict) and obj.get("type") == "result":
            return "claude", parse_claude_result(obj)
    except json.JSONDecodeError:
        pass

    lines = []
    for line in text.splitlines():
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            lines.append(json.loads(line))
        except json.JSONDecodeError:
            continue

    types = {o.get("type") for o in lines}
    if types & {"thread.started", "turn.completed", "turn.failed"}:
        return "codex", parse_codex_jsonl(lines)
    results = [o for o in lines if o.get("type") == "result"]
    if results:
        return "claude", parse_claude_result(results[-1])

    # TODO: add Grok Build's headless output format once confirmed.
    return "unknown", empty_usage()


def apply_prices(u, budgets):
    """Fill in cost from the price table when the harness reports tokens only."""
    if u["cost_usd"] is not None or not u["model"]:
        return u
    price = (budgets.get("prices_per_million_tokens") or {}).get(u["model"])
    if not price:
        return u
    u["cost_usd"] = (u["input_tokens"] * price.get("input", 0)
                     + u["cached_tokens"] * price.get("cached_input", price.get("input", 0))
                     + u["output_tokens"] * price.get("output", 0)) / 1_000_000
    u["cost_source"] = "price-table"
    return u


def save(conn, harness, u, ctx, raw_output=None):
    # One row per harness invocation. A resumed session reports each step's usage
    # under the same session ID, so keying by session alone would undercount.
    # Default key: a hash of the exact output, so re-recording identical output
    # (a retried pipe) replaces the row instead of double counting. Pass
    # --record-key to make a retried bot turn idempotent explicitly.
    if ctx.get("record_key"):
        key = ctx["record_key"]
    elif raw_output:
        digest = hashlib.sha256(raw_output.encode()).hexdigest()[:16]
        key = f"{harness}:{u['session_id'] or 'nosession'}:{digest}"
    else:
        key = f"{harness}:{uuid.uuid4()}"
    conn.execute("BEGIN IMMEDIATE")
    try:
        conn.execute("""
            INSERT INTO usage (recorded_at, record_key, harness, session_id, ticket,
                workstream, role, bot, model, input_tokens, cached_tokens,
                output_tokens, cost_usd, cost_source, status, duration_ms)
            VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
            ON CONFLICT(record_key) DO UPDATE SET
                recorded_at=excluded.recorded_at, model=excluded.model,
                input_tokens=excluded.input_tokens, cached_tokens=excluded.cached_tokens,
                output_tokens=excluded.output_tokens, cost_usd=excluded.cost_usd,
                cost_source=excluded.cost_source, status=excluded.status,
                duration_ms=excluded.duration_ms
        """, (time.time(), key, harness, u["session_id"], ctx.get("ticket"),
              ctx.get("workstream"), ctx.get("role"), ctx.get("bot"),
              ctx.get("model") or u["model"], u["input_tokens"], u["cached_tokens"],
              u["output_tokens"], u["cost_usd"], u["cost_source"], u["status"],
              u["duration_ms"]))
        conn.execute("COMMIT")
    except Exception:
        conn.execute("ROLLBACK")
        raise
    return key


# ───────────────────────── budgets ─────────────────────────

def parse_window(s):
    if not s:
        return None
    units = {"m": 60, "h": 3600, "d": 86400}
    return float(s[:-1]) * units[s[-1]] if s[-1] in units else float(s)


def spent(conn, column=None, value=None, window_s=None):
    sql = "SELECT COALESCE(SUM(cost_usd),0), COALESCE(SUM(input_tokens+cached_tokens+output_tokens),0) FROM usage WHERE 1=1"
    args = []
    if column:
        sql += f" AND {column} = ?"
        args.append(value)
    if window_s:
        sql += " AND recorded_at >= ?"
        args.append(time.time() - window_s)
    usd, tokens = conn.execute(sql, args).fetchone()
    return usd, tokens


def evaluate(conn, scope_name, column, value, limits, warn_at):
    """Compare one scope's spending with its limits. Returns a list of findings."""
    findings = []
    for key, limit in (limits or {}).items():
        metric, _, period = key.partition("_")
        window = None if period == "total" else parse_window(period)
        usd, tokens = spent(conn, column, value, window)
        used = usd if metric == "usd" else tokens
        ratio = used / limit if limit else 0
        status = "over" if ratio >= 1 else "warn" if ratio >= warn_at else "ok"
        findings.append({"scope": scope_name, "limit": key, "used": round(used, 4),
                         "max": limit, "remaining": round(max(limit - used, 0), 4),
                         "status": status})
    return findings


def cmd_check(args, conn, budgets):
    warn_at = budgets.get("warn_at", 0.8)
    findings = evaluate(conn, "global", None, None, budgets.get("global"), warn_at)
    if args.workstream:
        findings += evaluate(conn, f"workstream:{args.workstream}", "workstream",
                             args.workstream, (budgets.get("workstreams") or {}).get(args.workstream), warn_at)
    if args.ticket:
        limits = (budgets.get("tickets") or {}).get(args.ticket, budgets.get("tickets_default"))
        findings += evaluate(conn, f"ticket:{args.ticket}", "ticket", args.ticket, limits, warn_at)
    if args.role:
        findings += evaluate(conn, f"role:{args.role}", "role", args.role,
                             (budgets.get("roles") or {}).get(args.role), warn_at)

    worst = "over" if any(f["status"] == "over" for f in findings) else \
            "warn" if any(f["status"] == "warn" for f in findings) else "ok"
    # Smallest remaining dollar budget, so a bot can pass it on as a hard cap
    # (for example Claude Code's --max-budget-usd).
    usd_left = [f["remaining"] for f in findings if f["limit"].startswith("usd_")]
    print(json.dumps({"status": worst,
                      "usd_remaining": min(usd_left) if usd_left else None,
                      "findings": [f for f in findings if f["status"] != "ok"] if not args.verbose else findings}))
    return {"ok": EXIT_OK, "warn": EXIT_WARN, "over": EXIT_OVER}[worst]


# ───────────────────────── breaker ─────────────────────────

def cmd_breaker(args, conn, budgets):
    cfg = {"window": 20, "min_samples": 5, "max_failure_rate": 0.5}
    cfg.update(budgets.get("breaker") or {})
    rows = conn.execute(
        "SELECT status FROM usage WHERE role = ? ORDER BY recorded_at DESC LIMIT ?",
        (args.role, cfg["window"])).fetchall()
    total = len(rows)
    failures = sum(1 for (s,) in rows if s in ("error", "unparsed"))
    rate = failures / total if total else 0
    tripped = total >= cfg["min_samples"] and rate > cfg["max_failure_rate"]
    print(json.dumps({"role": args.role, "status": "tripped" if tripped else "ok",
                      "recent_sessions": total, "failures": failures,
                      "failure_rate": round(rate, 3), "max_failure_rate": cfg["max_failure_rate"]}))
    return EXIT_OVER if tripped else EXIT_OK


# ───────────────────────── report ─────────────────────────

def cmd_report(args, conn, budgets):
    group = {"ticket": "ticket", "workstream": "workstream", "role": "role",
             "model": "model", "harness": "harness"}[args.by]
    sql = f"""SELECT COALESCE({group}, '(none)'), COUNT(*),
                     COALESCE(SUM(cost_usd),0),
                     SUM(input_tokens), SUM(cached_tokens), SUM(output_tokens),
                     SUM(CASE WHEN status != 'success' THEN 1 ELSE 0 END),
                     SUM(CASE WHEN cost_usd IS NULL THEN 1 ELSE 0 END)
              FROM usage WHERE 1=1"""
    params = []
    if args.since:
        sql += " AND recorded_at >= ?"
        params.append(time.time() - parse_window(args.since))
    sql += f" GROUP BY {group} ORDER BY 3 DESC"
    rows = conn.execute(sql, params).fetchall()
    if args.json:
        keys = [args.by, "sessions", "usd", "input", "cached", "output", "failures", "unpriced"]
        print(json.dumps([dict(zip(keys, r)) for r in rows]))
        return EXIT_OK
    print(f"{args.by:<24} {'sessions':>8} {'usd':>10} {'input':>12} {'cached':>12} {'output':>10} {'failed':>7}")
    for name, n, usd, inp, cached, out, failed, unpriced in rows:
        flag = " *" if unpriced else ""
        print(f"{str(name)[:24]:<24} {n:>8} {usd:>10.4f} {inp:>12} {cached:>12} {out:>10} {failed:>7}{flag}")
    if any(r[7] for r in rows):
        print("* some sessions have no cost: add their model to prices_per_million_tokens")
    return EXIT_OK


# ───────────────────────── run / record ─────────────────────────

def context(args):
    return {"ticket": args.ticket, "workstream": args.workstream, "role": args.role,
            "bot": args.bot, "model": args.model, "record_key": args.record_key}


def cmd_run(args, conn, budgets):
    if not args.command:
        print("maestro-usage run: give the harness command after --", file=sys.stderr)
        return 64
    proc = subprocess.run(args.command, stdout=subprocess.PIPE, text=True)
    # Pass the harness output through untouched, so the bot sees exactly what it would have.
    sys.stdout.write(proc.stdout)
    sys.stdout.flush()
    harness, u = parse_output(proc.stdout)
    if proc.returncode != 0 and u["status"] == "success":
        u["status"] = "error"
    u["model"] = args.model or u["model"]
    apply_prices(u, budgets)
    # Every run is a new invocation, so it always gets its own row.
    save(conn, harness, u, context(args))
    return proc.returncode


def cmd_record(args, conn, budgets):
    if args.manual:
        # For usage the harness doesn't report, such as the bot runtime's own tokens.
        u = empty_usage()
        u.update({"input_tokens": args.input_tokens, "cached_tokens": args.cached_tokens,
                  "output_tokens": args.output_tokens, "status": args.status,
                  "session_id": args.session_id, "model": args.model})
        if args.cost_usd is not None:
            u["cost_usd"], u["cost_source"] = args.cost_usd, "manual"
        harness = args.harness or "manual"
        raw = None
    else:
        raw = sys.stdin.read()
        harness, u = parse_output(raw)
    u["model"] = args.model or u["model"]
    apply_prices(u, budgets)
    key = save(conn, harness, u, context(args), raw_output=raw)
    print(json.dumps({"recorded": key, "harness": harness, "status": u["status"],
                      "cost_usd": u["cost_usd"]}))
    return EXIT_OK


# ───────────────────────── cli ─────────────────────────

def main(argv=None):
    p = argparse.ArgumentParser(prog="maestro-usage", description=__doc__,
                                formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--db", help="path to usage.db (default: shared git dir)")
    p.add_argument("--budgets", help="path to budgets.json")
    sub = p.add_subparsers(dest="cmd", required=True)

    def add_context(sp):
        sp.add_argument("--ticket")
        sp.add_argument("--workstream")
        sp.add_argument("--role")
        sp.add_argument("--bot")
        sp.add_argument("--model", help="model name, needed for Codex pricing")
        sp.add_argument("--record-key", help="idempotency key; defaults to harness:session")

    sp = sub.add_parser("run", help="run a harness command and record its usage")
    add_context(sp)
    sp.add_argument("command", nargs=argparse.REMAINDER)

    sp = sub.add_parser("record", help="record usage from stdin, or manually")
    add_context(sp)
    sp.add_argument("--manual", action="store_true")
    sp.add_argument("--harness")
    sp.add_argument("--session-id")
    sp.add_argument("--input-tokens", type=int, default=0)
    sp.add_argument("--cached-tokens", type=int, default=0)
    sp.add_argument("--output-tokens", type=int, default=0)
    sp.add_argument("--cost-usd", type=float)
    sp.add_argument("--status", default="success", choices=["success", "error"])

    sp = sub.add_parser("check", help="check budgets; exit 0 ok, 1 warn, 2 over")
    sp.add_argument("--ticket")
    sp.add_argument("--workstream")
    sp.add_argument("--role")
    sp.add_argument("--verbose", action="store_true", help="show every limit, not just problems")

    sp = sub.add_parser("breaker", help="check a role pool's failure rate; exit 2 if tripped")
    sp.add_argument("--role", required=True)

    sp = sub.add_parser("report", help="summarise usage")
    sp.add_argument("--by", default="workstream",
                    choices=["ticket", "workstream", "role", "model", "harness"])
    sp.add_argument("--since", help="window such as 30m, 24h or 7d")
    sp.add_argument("--json", action="store_true")

    args = p.parse_args(argv)
    if getattr(args, "command", None) and args.command[:1] == ["--"]:
        args.command = args.command[1:]

    conn = connect(args.db)
    budgets = load_budgets(args.budgets)
    handler = {"run": cmd_run, "record": cmd_record, "check": cmd_check,
               "breaker": cmd_breaker, "report": cmd_report}[args.cmd]
    return handler(args, conn, budgets)


if __name__ == "__main__":
    sys.exit(main())
