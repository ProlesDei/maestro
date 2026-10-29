package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Budgets is budgets.json. Limits keys are <metric>_<period>: metric usd or
// tokens, period total or a window such as 30m, 24h or 7d.
type Budgets struct {
	WarnAt         float64           `json:"warn_at"`
	Prices         map[string]Price  `json:"prices_per_million_tokens"`
	Global         Limits            `json:"global"`
	Workstreams    map[string]Limits `json:"workstreams"`
	TicketsDefault Limits            `json:"tickets_default"`
	Tickets        map[string]Limits `json:"tickets"`
	Roles          map[string]Limits `json:"roles"`
	Breaker        BreakerConfig     `json:"breaker"`
}

// Limits maps a limit key such as usd_24h to its maximum.
type Limits map[string]float64

// Price is dollars per million tokens. CachedInput defaults to Input.
type Price struct {
	Input       float64  `json:"input"`
	CachedInput *float64 `json:"cached_input"`
	Output      float64  `json:"output"`
}

// BreakerConfig sets when a role pool trips.
type BreakerConfig struct {
	Window         int     `json:"window"`      // newest records looked at
	MinSamples     int     `json:"min_samples"` // never trip on fewer
	MaxFailureRate float64 `json:"max_failure_rate"`
}

// DefaultBudgets has no limits, so everything passes.
func DefaultBudgets() Budgets {
	return Budgets{WarnAt: 0.8, Breaker: BreakerConfig{Window: 20, MinSamples: 5, MaxFailureRate: 0.5}}
}

// ParseBudgets reads budgets.json over the defaults. Unknown fields and bad
// limit keys are errors, so a typo can't silently switch a budget off.
func ParseBudgets(data []byte) (Budgets, error) {
	b := DefaultBudgets()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return b, fmt.Errorf("%w: budgets: %v", ErrInvalid, err)
	}
	all := []Limits{b.Global, b.TicketsDefault}
	for _, m := range []map[string]Limits{b.Workstreams, b.Tickets, b.Roles} {
		for _, l := range m {
			all = append(all, l)
		}
	}
	for _, l := range all {
		for key, v := range l {
			if _, _, err := parseLimitKey(key); err != nil {
				return b, err
			}
			// A negative limit would always read as under budget.
			if v < 0 {
				return b, fmt.Errorf("%w: limit %q: %v is negative", ErrInvalid, key, v)
			}
		}
	}
	for model, p := range b.Prices {
		if p.Input < 0 || p.Output < 0 || p.CachedInput != nil && *p.CachedInput < 0 {
			return b, fmt.Errorf("%w: price for %q is negative", ErrInvalid, model)
		}
	}
	if b.Breaker.Window < 1 {
		return b, fmt.Errorf("%w: budgets: breaker.window must be at least 1", ErrInvalid)
	}
	return b, nil
}

// ApplyPrices fills in cost from the price table when the harness reported
// tokens only.
func ApplyPrices(u *Usage, prices map[string]Price) {
	if u.CostUSD != nil || u.Model == "" {
		return
	}
	p, ok := prices[u.Model]
	if !ok {
		return
	}
	cached := p.Input
	if p.CachedInput != nil {
		cached = *p.CachedInput
	}
	cost := (float64(u.InputTokens)*p.Input + float64(u.CachedTokens)*cached +
		float64(u.OutputTokens)*p.Output) / 1e6
	u.CostUSD, u.CostSource = &cost, CostPrices
}

// ParseWindow reads 30m, 24h or 7d, or a bare number of seconds.
func ParseWindow(s string) (time.Duration, error) {
	unit := time.Second
	num := s
	if n := len(s); n > 0 {
		switch s[n-1] {
		case 'm':
			unit, num = time.Minute, s[:n-1]
		case 'h':
			unit, num = time.Hour, s[:n-1]
		case 'd':
			unit, num = 24*time.Hour, s[:n-1]
		}
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil || f <= 0 || math.IsInf(f, 0) {
		return 0, fmt.Errorf("%w: window %q: want a positive number with m, h or d", ErrInvalid, s)
	}
	return time.Duration(f * float64(unit)), nil
}

func parseLimitKey(key string) (metric string, window time.Duration, err error) {
	metric, period, _ := strings.Cut(key, "_")
	if metric != "usd" && metric != "tokens" {
		return "", 0, fmt.Errorf("%w: limit %q: want usd_<period> or tokens_<period>", ErrInvalid, key)
	}
	if period == "total" {
		return metric, 0, nil
	}
	window, err = ParseWindow(period)
	if err != nil {
		return "", 0, fmt.Errorf("%w: limit %q: period must be total or a window such as 24h", ErrInvalid, key)
	}
	return metric, window, nil
}

// Check statuses, worst last.
const (
	CheckOK   = "ok"
	CheckWarn = "warn"
	CheckOver = "over"
)

// Finding is one limit compared with spending.
type Finding struct {
	Scope     string  `json:"scope"`
	Limit     string  `json:"limit"`
	Used      float64 `json:"used"`
	Max       float64 `json:"max"`
	Remaining float64 `json:"remaining"`
	Status    string  `json:"status"`
}

// CheckResult is the outcome of Check.
type CheckResult struct {
	Status string `json:"status"`
	// USDRemaining is the smallest dollar budget left, to pass on as a hard
	// cap such as Claude Code's --max-budget-usd. Nil when no dollar limit applies.
	USDRemaining *float64  `json:"usd_remaining"`
	Findings     []Finding `json:"findings"`
}

// Scopes names what to check besides the global budget. Empty fields are
// skipped.
type Scopes struct {
	Ticket, Workstream, Role string
}

// Check compares spending with every limit that applies.
func Check(ctx context.Context, s Store, now time.Time, b Budgets, sc Scopes) (CheckResult, error) {
	type scope struct {
		name   string
		field  Field
		value  string
		limits Limits
	}
	scopes := []scope{{"global", "", "", b.Global}}
	if sc.Workstream != "" {
		scopes = append(scopes, scope{"workstream:" + sc.Workstream, FieldWorkstream, sc.Workstream, b.Workstreams[sc.Workstream]})
	}
	if sc.Ticket != "" {
		l, ok := b.Tickets[sc.Ticket]
		if !ok {
			l = b.TicketsDefault
		}
		scopes = append(scopes, scope{"ticket:" + sc.Ticket, FieldTicket, sc.Ticket, l})
	}
	if sc.Role != "" {
		scopes = append(scopes, scope{"role:" + sc.Role, FieldRole, sc.Role, b.Roles[sc.Role]})
	}

	res := CheckResult{Status: CheckOK, Findings: []Finding{}}
	rank := map[string]int{CheckOK: 0, CheckWarn: 1, CheckOver: 2}
	for _, sp := range scopes {
		keys := make([]string, 0, len(sp.limits))
		for k := range sp.limits {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, key := range keys {
			limit := sp.limits[key]
			metric, window, err := parseLimitKey(key)
			if err != nil {
				return res, err
			}
			f := Filter{Field: sp.field, Value: sp.value}
			if window > 0 {
				f.Since = now.Add(-window)
			}
			t, err := s.Spent(ctx, f)
			if err != nil {
				return res, err
			}
			used := t.USD
			if metric == "tokens" {
				used = float64(t.Tokens)
			}
			// A zero limit means no limit, as in the prototype.
			ratio := 0.0
			if limit != 0 {
				ratio = used / limit
			}
			st := CheckOK
			if ratio >= 1 {
				st = CheckOver
			} else if ratio >= b.WarnAt {
				st = CheckWarn
			}
			fd := Finding{Scope: sp.name, Limit: key, Used: round(used, 4), Max: limit,
				Remaining: round(max(limit-used, 0), 4), Status: st}
			res.Findings = append(res.Findings, fd)
			if rank[st] > rank[res.Status] {
				res.Status = st
			}
			// A zero limit has nothing left to pass on as a cap.
			if metric == "usd" && limit != 0 && (res.USDRemaining == nil || fd.Remaining < *res.USDRemaining) {
				r := fd.Remaining
				res.USDRemaining = &r
			}
		}
	}
	return res, nil
}

// BreakerResult is the outcome of Breaker.
type BreakerResult struct {
	Role           string  `json:"role"`
	Status         string  `json:"status"` // ok or tripped
	RecentSessions int     `json:"recent_sessions"`
	Failures       int     `json:"failures"`
	FailureRate    float64 `json:"failure_rate"`
	MaxFailureRate float64 `json:"max_failure_rate"`
}

// Breaker trips when more than the allowed share of a role's newest
// invocations failed. Unparsed output counts as a failure.
func Breaker(ctx context.Context, s Store, role string, cfg BreakerConfig) (BreakerResult, error) {
	sts, err := s.RecentStatuses(ctx, role, cfg.Window)
	if err != nil {
		return BreakerResult{}, err
	}
	fails := 0
	for _, st := range sts {
		if st == StatusError || st == StatusUnparsed {
			fails++
		}
	}
	rate := 0.0
	if len(sts) > 0 {
		rate = float64(fails) / float64(len(sts))
	}
	res := BreakerResult{Role: role, Status: "ok", RecentSessions: len(sts), Failures: fails,
		FailureRate: round(rate, 3), MaxFailureRate: cfg.MaxFailureRate}
	if len(sts) >= cfg.MinSamples && rate > cfg.MaxFailureRate {
		res.Status = "tripped"
	}
	return res, nil
}

func round(x float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(x*p) / p
}
