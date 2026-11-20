package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

type paceWindow struct {
	Used   float64   `json:"used_percent"`
	Reset  time.Time `json:"resets_at"`
	Read   time.Time `json:"read_at"`
	Source string    `json:"source"`
	// Burn is allowance points per hour. The first sample has no measured trend.
	Burn float64 `json:"burn_per_hour"`
}
type usageLedger map[string]map[string]paceWindow

func readUsage(path string) (usageLedger, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return usageLedger{}, nil
	}
	if err != nil {
		return nil, err
	}
	var state usageLedger
	if json.Unmarshal(b, &state) != nil || state == nil {
		return nil, errors.New("invalid subscription usage ledger")
	}
	return state, nil
}

func saveObservation(dir, alias string, windows map[string]paceWindow) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	unlock, err := lockBoard(dir)
	if err != nil {
		return err
	}
	defer unlock()
	path := filepath.Join(dir, "usage.json")
	state, err := readUsage(path)
	if err != nil {
		return err
	}
	if state[alias] == nil {
		state[alias] = map[string]paceWindow{}
	}
	for key, w := range windows {
		previous, ok := state[alias][key]
		if ok && !w.Read.After(previous.Read) {
			continue
		}
		if ok && previous.Reset.Equal(w.Reset) {
			if w.Used < previous.Used {
				return errors.New("allowance decreased within the same reset window")
			}
			w.Burn = (w.Used - previous.Used) / w.Read.Sub(previous.Read).Hours()
		}
		state[alias][key] = w
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return writeWholeMode(path, data, 0600)
}

func (c *call) recordUsage(alias, key, reset string, used float64) error {
	if !runs.BotPattern.MatchString(key) || math.IsNaN(used) || used < 0 || used > 100 {
		return usagef("profile sample: give a named --window and --used-percent from 0 to 100")
	}
	end, err := time.Parse(time.RFC3339, reset)
	now := time.Now().UTC()
	if err != nil || !end.After(now) {
		return usagef("profile sample: --reset must be a future RFC3339 timestamp")
	}
	dir, err := c.standingDir()
	if err != nil {
		return err
	}
	return saveObservation(dir, alias, map[string]paceWindow{key: {Used: used, Reset: end, Read: now, Source: "operator"}})
}

func (c *call) copyNativeUsage(st profileStatus) error {
	windows := map[string]paceWindow{}
	for name, w := range st.Windows {
		if w.UsedPercent != nil && w.Reset != nil && *w.UsedPercent >= 0 && *w.UsedPercent <= 100 {
			windows[name] = paceWindow{Used: *w.UsedPercent, Reset: time.Unix(*w.Reset, 0).UTC(), Read: st.ReadAt, Source: st.Harness}
		}
	}
	if len(windows) == 0 {
		return errors.New("profile sample: no numeric allowance from the native meter")
	}
	dir, err := c.standingDir()
	if err != nil {
		return err
	}
	return saveObservation(dir, st.Alias, windows)
}

// Admission runs under the board lock through creation of the run record.
// The record itself reserves the estimate before the supervisor can start.
func (c *call) createPacedRun(dir string, r runs.Record, prompt []byte) (runs.Record, error) {
	root := filepath.Dir(dir)
	loc, err := c.location()
	if err != nil {
		return r, err
	}
	if loc.boardID != "" && r.Job == "" {
		return r, usagef("launch: use job on a subscription-paced runtime board")
	}
	if loc.boardID == "" {
		return runs.Create(dir, r, prompt)
	}
	unlock, err := lockBoard(root)
	if err != nil {
		return r, err
	}
	defer unlock()
	profiles, err := loadBindings(filepath.Join(root, "profiles.json"))
	if err != nil {
		return r, err
	}
	usage, err := readUsage(filepath.Join(root, "usage.json"))
	if err != nil {
		return r, err
	}
	now := time.Now().UTC()
	list, err := runs.List(dir, now, true)
	if err != nil {
		return r, err
	}
	for _, old := range list {
		if old.State == runs.Unreadable {
			return r, errors.New("usage unknown: unreadable run record")
		}
		if r.ChainOf != "" && r.ResumeOf == "" && r.AgainOf == "" && old.ResumeOf == "" && old.AgainOf == "" && old.ChainOf == r.ChainOf && old.Job == r.Job && old.Ticket == r.Ticket && old.Launcher == r.Launcher && !neverRan(old) {
			return r, alreadyGoing{run: old.ID, ran: old.Ended()}
		}
	}
	best := -1.0
	var chosen string
	var reasons []string
	for _, alias := range sortedAliases(profiles) {
		p := profiles[alias]
		binding, ok := p.binding(r.Job, r.Launcher)
		if !ok || (r.Profile != "" && r.Profile != alias) {
			continue
		}
		if m, e := flagValue(r.Overrides, "--model"), flagValue(r.Overrides, "--effort"); (m != "" && binding.Model != m) || (e != "" && binding.Effort != e) {
			continue
		}
		account, err := readHold(filepath.Join(root, "account-"+alias+".hold"))
		if err != nil {
			return r, err
		}
		provider, err := readHold(filepath.Join(root, "provider-"+p.Harness+".hold"))
		if err != nil {
			return r, err
		}
		if now.Before(account) || now.Before(provider) {
			until := account
			if provider.After(until) {
				until = provider
			}
			reasons = append(reasons, alias+": paused until "+until.UTC().Format(time.RFC3339))
			continue
		}
		room, reason := projectedRoom(alias, binding, usage[alias], list, now)
		if reason != "" {
			reasons = append(reasons, alias+": "+reason)
			continue
		}
		if room > best {
			best, chosen = room, alias
		}
	}
	if chosen == "" {
		if len(reasons) == 0 {
			reasons = append(reasons, "no eligible account")
		}
		return r, fmt.Errorf("%w: %s: %s", ticket.ErrPaused, r.Job, strings.Join(reasons, ", "))
	}
	p := profiles[chosen]
	b, _ := p.binding(r.Job, r.Launcher)
	if !isNativeCommand(r.Command) {
		r.Command, err = c.nativeCommand(chosen, r.Job, b, p.Harness)
		if err != nil {
			return r, err
		}
	}
	r.BoardID = loc.boardID
	r.Profile, r.Harness, r.Estimate = chosen, p.Harness, b.Estimate
	return runs.Create(dir, r, prompt)
}

func sortedAliases(profiles map[string]boundProfile) []string {
	keys := make([]string, 0, len(profiles))
	for alias := range profiles {
		keys = append(keys, alias)
	}
	slices.Sort(keys)
	return keys
}

func projectedRoom(alias string, b jobModel, windows map[string]paceWindow, list []runs.Record, now time.Time) (float64, string) {
	if len(windows) == 0 || b.Estimate <= 0 {
		return 0, "missing usage reading or job estimate"
	}
	room := 100.0
	for _, w := range windows {
		if !now.Before(w.Reset) {
			return 0, "reset passed; refresh usage"
		}
		remaining := 100 - w.Used - w.Burn*w.Reset.Sub(w.Read).Hours() - b.Estimate
		for _, old := range list {
			if old.Profile == alias && !neverRan(old) && (old.EndedAt == nil || old.EndedAt.After(w.Read)) {
				remaining -= old.Estimate
			}
		}
		if remaining < 0 {
			return 0, "projected usage exceeds allowance until " + w.Reset.Format(time.RFC3339)
		}
		room = min(room, remaining)
	}
	return room, ""
}

func (c *call) usageStatus() error {
	dir, err := c.standingDir()
	if err != nil {
		return err
	}
	state, err := readUsage(filepath.Join(dir, "usage.json"))
	if err != nil {
		return err
	}
	return c.out(state, func(w io.Writer) { enc := json.NewEncoder(w); enc.SetIndent("", "  "); enc.Encode(state) })
}

// Profile limits and provider outages have different scopes.
func (c *call) holdSubscription(r runs.Record, until time.Time, provider bool) error {
	dir, err := c.standingDir()
	if err != nil {
		return err
	}
	key := "account-" + r.Profile
	if provider {
		key = "provider-" + r.Harness
	}
	if !runs.BotPattern.MatchString(key) {
		return errors.New("invalid subscription hold")
	}
	unlock, err := lockBoard(dir)
	if err != nil {
		return err
	}
	defer unlock()
	path := filepath.Join(dir, key+".hold")
	previous, err := readHold(path)
	if err != nil {
		return err
	}
	if previous.Before(until) {
		return writeWholeMode(path, []byte(until.UTC().Format(time.RFC3339Nano)), 0600)
	}
	return nil
}

func readHold(path string) (time.Time, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, string(b))
}
