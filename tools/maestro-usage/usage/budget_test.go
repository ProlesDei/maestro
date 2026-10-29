package usage_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-usage/sqlitestore"
	"github.com/ProlesDei/maestro/tools/maestro-usage/usage"
)

// The prototype's BUDGETS_EXAMPLE, with prices.
const exampleBudgets = `{
  "warn_at": 0.8,
  "prices_per_million_tokens": {"gpt-5.5": {"input": 2, "cached_input": 0.5, "output": 10}},
  "global": {"usd_24h": 50, "tokens_24h": 50000000},
  "workstreams": {"auth": {"usd_total": 20}},
  "tickets_default": {"usd_total": 2},
  "tickets": {"T-12": {"usd_total": 5}},
  "roles": {"junior-engineer": {"usd_24h": 10, "tokens_1h": 1000}},
  "breaker": {"window": 20, "min_samples": 5, "max_failure_rate": 0.5}
}`

func TestParseBudgets(t *testing.T) {
	b, err := usage.ParseBudgets([]byte(exampleBudgets))
	if err != nil {
		t.Fatal(err)
	}
	if b.Tickets["T-12"]["usd_total"] != 5 || *b.Prices["gpt-5.5"].CachedInput != 0.5 {
		t.Fatalf("parsed %+v", b)
	}
	// Partial breaker config keeps the other defaults.
	b, err = usage.ParseBudgets([]byte(`{"breaker": {"window": 10}}`))
	if err != nil {
		t.Fatal(err)
	}
	if b.Breaker != (usage.BreakerConfig{Window: 10, MinSamples: 5, MaxFailureRate: 0.5}) || b.WarnAt != 0.8 {
		t.Fatalf("defaults lost: %+v", b)
	}
	for _, bad := range []string{
		`{"global": {"usd_24x": 1}}`,
		`{"global": {"dollars_total": 1}}`,
		`{"roles": {"qa": {"usd": 1}}}`,
		`{"tickets": {"T-1": {"tokens_-1h": 1}}}`,
		`{"globl": {"usd_total": 1}}`,
		`{"breaker": {"window": 0}}`,
		`{"global": {"usd_24h": -1}}`,
		`{"prices_per_million_tokens": {"m": {"input": 1, "output": -1}}}`,
		`{"prices_per_million_tokens": {"m": {"input": 1, "cached_input": -1, "output": 1}}}`,
		`not json`,
	} {
		if _, err := usage.ParseBudgets([]byte(bad)); !errors.Is(err, usage.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestParseWindow(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"30m", 30 * time.Minute}, {"24h", 24 * time.Hour}, {"7d", 7 * 24 * time.Hour},
		{"1.5h", 90 * time.Minute}, {"90", 90 * time.Second},
	}
	for _, tc := range tests {
		got, err := usage.ParseWindow(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ParseWindow(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "h", "0h", "-1d", "1w", "total"} {
		if _, err := usage.ParseWindow(bad); err == nil {
			t.Errorf("ParseWindow(%q) accepted", bad)
		}
	}
}

func TestApplyPrices(t *testing.T) {
	prices := map[string]usage.Price{
		"m":         {Input: 2, CachedInput: ptr(0.5), Output: 10},
		"no-cached": {Input: 2, Output: 10},
	}
	tests := []struct {
		name string
		in   usage.Usage
		want *float64
	}{
		{"priced", usage.Usage{Model: "m", InputTokens: 1e6, CachedTokens: 2e6, OutputTokens: 1e5}, ptr(2 + 1 + 1.0)},
		{"cached defaults to input", usage.Usage{Model: "no-cached", CachedTokens: 1e6}, ptr(2.0)},
		{"unknown model", usage.Usage{Model: "other", InputTokens: 1e6}, nil},
		{"no model", usage.Usage{InputTokens: 1e6}, nil},
		{"harness cost wins", usage.Usage{Model: "m", InputTokens: 1e6, CostUSD: ptr(0.1), CostSource: usage.CostHarness}, ptr(0.1)},
	}
	for _, tc := range tests {
		u := tc.in
		usage.ApplyPrices(&u, prices)
		if (u.CostUSD == nil) != (tc.want == nil) || u.CostUSD != nil && *u.CostUSD != *tc.want {
			t.Errorf("%s: cost = %v, want %v", tc.name, u.CostUSD, tc.want)
		}
		if tc.name == "priced" && u.CostSource != usage.CostPrices {
			t.Errorf("source = %q", u.CostSource)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func openStore(t *testing.T) *sqlitestore.Store {
	t.Helper()
	s, err := sqlitestore.Open(context.Background(), filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

var now = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

var keys int

func save(t *testing.T, s usage.Store, at time.Time, c usage.Context, usd float64, tokens int64, status string) {
	t.Helper()
	keys++
	r := usage.Record{Key: fmt.Sprintf("k%d", keys), RecordedAt: at, Harness: "claude", Context: c,
		Usage: usage.Usage{InputTokens: tokens, CostUSD: &usd, Status: status}}
	if err := s.Save(context.Background(), r); err != nil {
		t.Fatal(err)
	}
}

func TestCheckScopes(t *testing.T) {
	b, err := usage.ParseBudgets([]byte(exampleBudgets))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	jse := usage.Context{Ticket: "T-12", Workstream: "auth", Role: "junior-engineer"}
	tests := []struct {
		name   string
		seed   func(s usage.Store)
		scopes usage.Scopes
		status string
		usdRem *float64
		over   []string // scope/limit pairs not ok
	}{
		{"empty store", func(usage.Store) {}, usage.Scopes{Ticket: "T-1"}, usage.CheckOK, ptr(2.0), nil},
		{"ticket default warns at 80%", func(s usage.Store) {
			save(t, s, now, usage.Context{Ticket: "T-1"}, 1.6, 0, "success")
		}, usage.Scopes{Ticket: "T-1"}, usage.CheckWarn, ptr(0.4), []string{"ticket:T-1/usd_total"}},
		{"ticket override raises the limit", func(s usage.Store) {
			save(t, s, now, jse, 3, 0, "success")
		}, usage.Scopes{Ticket: "T-12"}, usage.CheckOK, ptr(2.0), nil},
		{"workstream over", func(s usage.Store) {
			save(t, s, now.Add(-30*24*time.Hour), jse, 20, 0, "success")
		}, usage.Scopes{Workstream: "auth"}, usage.CheckOver, ptr(0.0), []string{"workstream:auth/usd_total"}},
		{"old spend is outside the 24h window", func(s usage.Store) {
			save(t, s, now.Add(-25*time.Hour), jse, 49, 0, "success")
			save(t, s, now.Add(-time.Hour), jse, 1, 0, "success")
		}, usage.Scopes{}, usage.CheckOK, ptr(49.0), nil},
		{"global over in 24h", func(s usage.Store) {
			save(t, s, now.Add(-23*time.Hour), usage.Context{}, 50, 0, "success")
		}, usage.Scopes{}, usage.CheckOver, ptr(0.0), []string{"global/usd_24h"}},
		{"role tokens over while dollars are fine", func(s usage.Store) {
			save(t, s, now.Add(-10*time.Minute), jse, 0, 1200, "success")
			save(t, s, now.Add(-2*time.Hour), jse, 0, 5000, "success")
		}, usage.Scopes{Role: "junior-engineer"}, usage.CheckOver, ptr(10.0), []string{"role:junior-engineer/tokens_1h"}},
		{"other roles don't count", func(s usage.Store) {
			save(t, s, now, usage.Context{Role: "qa-lead"}, 0, 5000, "success")
		}, usage.Scopes{Role: "junior-engineer"}, usage.CheckOK, ptr(10.0), nil},
		{"worst status wins", func(s usage.Store) {
			save(t, s, now, jse, 4.5, 0, "success")
		}, usage.Scopes{Ticket: "T-12", Workstream: "auth", Role: "junior-engineer"}, usage.CheckWarn, ptr(0.5),
			[]string{"ticket:T-12/usd_total"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := openStore(t)
			tc.seed(s)
			res, err := usage.Check(ctx, s, now, b, tc.scopes)
			if err != nil {
				t.Fatal(err)
			}
			if res.Status != tc.status {
				t.Errorf("status = %s, want %s: %+v", res.Status, tc.status, res.Findings)
			}
			if (res.USDRemaining == nil) != (tc.usdRem == nil) || res.USDRemaining != nil && *res.USDRemaining != *tc.usdRem {
				t.Errorf("usd_remaining = %v, want %v", deref(res.USDRemaining), deref(tc.usdRem))
			}
			var bad []string
			for _, f := range res.Findings {
				if f.Status != usage.CheckOK {
					bad = append(bad, f.Scope+"/"+f.Limit)
				}
			}
			if fmt.Sprint(bad) != fmt.Sprint(tc.over) {
				t.Errorf("not ok = %v, want %v", bad, tc.over)
			}
		})
	}
}

func TestCheckNoLimits(t *testing.T) {
	s := openStore(t)
	save(t, s, now, usage.Context{Ticket: "T-1"}, 1000, 1e9, "success")
	res, err := usage.Check(context.Background(), s, now, usage.DefaultBudgets(), usage.Scopes{Ticket: "T-1", Role: "x"})
	if err != nil || res.Status != usage.CheckOK || res.USDRemaining != nil || len(res.Findings) != 0 {
		t.Fatalf("res = %+v, err %v", res, err)
	}
	// A zero limit means no limit, as in the prototype.
	b := usage.DefaultBudgets()
	b.Global = usage.Limits{"usd_total": 0}
	// It sets no cap either: usd_remaining 0 would stop every session.
	if res, _ := usage.Check(context.Background(), s, now, b, usage.Scopes{}); res.Status != usage.CheckOK || res.USDRemaining != nil {
		t.Fatalf("zero limit: %+v", res)
	}
}

func TestBreaker(t *testing.T) {
	cfg := usage.BreakerConfig{Window: 4, MinSamples: 3, MaxFailureRate: 0.5}
	tests := []struct {
		name     string
		statuses []string // oldest first
		tripped  bool
		failures int
	}{
		{"no history", nil, false, 0},
		{"too few samples", []string{"error", "error"}, false, 2},
		{"over the rate", []string{"success", "error", "unparsed"}, true, 2},
		{"at the rate is ok", []string{"success", "success", "error", "error"}, false, 2},
		{"only the newest window counts", []string{"error", "error", "error", "success", "success", "success", "error"}, false, 1},
		{"recent spike", []string{"success", "success", "success", "error", "error", "error"}, true, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := openStore(t)
			for i, st := range tc.statuses {
				save(t, s, now.Add(time.Duration(i)*time.Minute), usage.Context{Role: "junior-engineer"}, 0, 0, st)
			}
			save(t, s, now, usage.Context{Role: "senior-engineer"}, 0, 0, "error")
			res, err := usage.Breaker(context.Background(), s, "junior-engineer", cfg)
			if err != nil {
				t.Fatal(err)
			}
			if (res.Status == "tripped") != tc.tripped || res.Failures != tc.failures {
				t.Errorf("got %+v, want tripped=%v failures=%d", res, tc.tripped, tc.failures)
			}
		})
	}
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}
