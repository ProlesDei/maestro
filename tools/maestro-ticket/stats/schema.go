package stats

import (
	"fmt"
	"strings"
)

// Shapes of the few TEXT columns, as GLOB checks. Go checks each value
// against the same shape before it writes, so a value off its shape goes
// in as NULL and never fails the row; the checks are the backstop.
var (
	runGlob  = "R-" + strings.Repeat("[a-z2-7]", 8)
	hex8     = strings.Repeat("[0-9a-f]", 8)
	hex4     = strings.Repeat("[0-9a-f]", 4)
	uuidGlob = hex8 + "-" + hex4 + "-" + hex4 + "-" + hex4 + "-" + hex4 + hex8
)

func isRun(col string) string { return fmt.Sprintf("CHECK (%s GLOB '%s')", col, runGlob) }

func isHex(col string, lo, hi int) string {
	return fmt.Sprintf("CHECK (length(%[1]s) BETWEEN %[2]d AND %[3]d AND %[1]s NOT GLOB '*[^0-9a-f]*')", col, lo, hi)
}

// migrations[i] moves stats.db from version i to i+1, in one transaction.
// Never edit a step that has shipped; add a new one. Every table is STRICT,
// so a text value can't land in an INTEGER column. An enum column holds a
// codes id, never a name.
var migrations = []string{
	`CREATE TABLE codes (
		id INTEGER PRIMARY KEY,
		kind TEXT NOT NULL,
		name TEXT NOT NULL,
		UNIQUE (kind, name)
	) STRICT;
	CREATE TABLE meta (
		key INTEGER PRIMARY KEY,
		value INTEGER NOT NULL
	) STRICT;
	CREATE TABLE run_facts (
		run TEXT PRIMARY KEY ` + isRun("run") + `,
		session TEXT CHECK (session GLOB '` + uuidGlob + `'),
		ticket INTEGER,
		role INTEGER, job INTEGER,
		model INTEGER, effort INTEGER, lean INTEGER,
		why INTEGER, tier INTEGER, tries INTEGER,
		again_of TEXT ` + isRun("again_of") + `,
		prompt_bytes INTEGER, drops INTEGER,
		resume_of TEXT ` + isRun("resume_of") + `,
		nudge INTEGER, after_death INTEGER,
		started_at INTEGER, ended_at INTEGER, wall_ms INTEGER,
		wait_ms INTEGER, wait_capped INTEGER, stop INTEGER, escaped INTEGER,
		state INTEGER, exit INTEGER, outcome INTEGER, reason INTEGER,
		subtype INTEGER, stop_reason INTEGER, is_error INTEGER,
		claude_ms INTEGER, api_ms INTEGER, turns INTEGER,
		head INTEGER, verdict INTEGER, v_tier INTEGER, mode INTEGER,
		reply_bytes INTEGER,
		stream INTEGER, ctx_peak INTEGER, compactions INTEGER, retries INTEGER,
		subagents INTEGER, tool_calls INTEGER, tool_fails INTEGER,
		open_code INTEGER, open_ms INTEGER,
		tail TEXT CHECK (tail NOT GLOB '*[^0-9! ]*'),
		rang INTEGER, ring_err INTEGER,
		v_tool TEXT ` + isHex("v_tool", 40, 40) + `,
		v_maestro TEXT ` + isHex("v_maestro", 7, 40) + `,
		v_claude TEXT CHECK (length(v_claude) <= 20 AND v_claude GLOB '[0-9]*.[0-9]*.[0-9]*' AND v_claude NOT GLOB '*[^0-9.]*'),
		os INTEGER, arch INTEGER, disk_free_pct INTEGER, inode_free_pct INTEGER
	) STRICT;
	CREATE INDEX run_facts_ended ON run_facts (ended_at);
	CREATE INDEX run_facts_ticket ON run_facts (ticket);
	CREATE INDEX run_facts_session ON run_facts (session);
	CREATE TABLE run_models (
		run TEXT NOT NULL ` + isRun("run") + `,
		model INTEGER NOT NULL,
		input_cum INTEGER, output_cum INTEGER, cache_read_cum INTEGER, cache_write_cum INTEGER,
		cost_cum REAL, cost_src INTEGER,
		PRIMARY KEY (run, model)
	) STRICT;
	CREATE TABLE run_steps (
		run TEXT NOT NULL ` + isRun("run") + `,
		code INTEGER NOT NULL,
		calls INTEGER, fails INTEGER, ms_sum INTEGER, ms_max INTEGER,
		PRIMARY KEY (run, code)
	) STRICT;
	CREATE TABLE findings (
		run TEXT NOT NULL ` + isRun("run") + `,
		fid TEXT CHECK (length(fid) <= 4 AND fid GLOB 'F[0-9]*' AND substr(fid, 2) NOT GLOB '*[^0-9]*'),
		kind INTEGER, pass INTEGER CHECK (pass BETWEEN 1 AND 10),
		sev INTEGER, action INTEGER, prior INTEGER, n INTEGER
	) STRICT;
	CREATE INDEX findings_run ON findings (run);
	CREATE TABLE daily (
		day INTEGER NOT NULL,
		job INTEGER NOT NULL, model INTEGER NOT NULL, outcome INTEGER NOT NULL,
		n INTEGER NOT NULL, cost_usd REAL, tokens INTEGER, ms_p50 INTEGER, ms_p90 INTEGER,
		PRIMARY KEY (day, job, model, outcome)
	) STRICT;
	CREATE TABLE pruned_cum (
		session TEXT NOT NULL CHECK (session GLOB '` + uuidGlob + `'),
		model INTEGER NOT NULL,
		ended_at INTEGER NOT NULL,
		cost_cum REAL, tok_cum INTEGER,
		PRIMARY KEY (session, model)
	) STRICT;`,
}

// Version is the schema this binary writes.
func Version() int { return len(migrations) }

// EnumCols are the columns that hold codes ids, by table: each is INTEGER,
// and a test holds every table to it. In daily, 0 stands for none.
var EnumCols = map[string][]string{
	"run_facts": {"role", "job", "model", "effort", "why", "tier", "nudge", "stop", "state", "outcome", "reason",
		"subtype", "stop_reason", "head", "verdict", "mode", "stream", "open_code", "ring_err", "os", "arch"},
	"run_models": {"model", "cost_src"},
	"run_steps":  {"code"},
	"findings":   {"kind", "sev", "action", "prior"},
	"daily":      {"job", "model", "outcome"},
	"pruned_cum": {"model"},
}

// meta keys.
const (
	metaLastPrune    = 1
	metaCodesVersion = 2
)
