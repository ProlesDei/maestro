package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/ticket"
)

type waitingForReset struct {
	at     time.Time
	reason string
}

func (e waitingForReset) Error() string { return e.reason }
func (e waitingForReset) Unwrap() error { return ticket.ErrPaused }

// A single local timer exists only while a known reset or reconnect is due.
// The deadline survives its process. Recovery on startup arms it again.
func (c *call) scheduleReconsider(dir string, at time.Time) error {
	store, _, err := c.storePath()
	if err != nil {
		return err
	}
	unlock, err := lockBoard(dir)
	if err != nil {
		return err
	}
	defer unlock()
	file := filepath.Join(dir, "reconsider.at")
	prev, err := readHold(file)
	if err != nil {
		return err
	}
	if prev.IsZero() || at.Before(prev) {
		if err := writeWholeMode(file, []byte(at.UTC().Format(time.RFC3339Nano)), 0600); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(filepath.Join(dir, "reconsider.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if ok, err := tryLock(f); err != nil || !ok {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "reconsider", "--store", store)
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, runs.RunIDEnv+"=") })
	cmd.SysProcAttr = runs.Detach()
	cmd.ExtraFiles = []*os.File{f}
	cmd.Dir = c.env.Dir
	return startSupervisor(cmd)
}

func runReconsider(ctx context.Context, c *call) error {
	if err := c.parse(); err != nil {
		return err
	}
	if err := c.noArgs(); err != nil {
		return err
	}
	loc, err := c.location()
	if err != nil {
		return err
	}
	if loc.boardID == "" {
		return usagef("reconsider requires a runtime board")
	}
	lock := os.NewFile(3, "reconsider.lock")
	if lock == nil {
		return errors.New("reconsider requires its inherited lock")
	}
	info, err := lock.Stat()
	expected, e := os.Stat(filepath.Join(loc.localDir, "reconsider.lock"))
	if err != nil || e != nil || !os.SameFile(info, expected) {
		return errors.New("invalid timer lock")
	}
	defer lock.Close()
	atFile := filepath.Join(loc.localDir, "reconsider.at")
	inFlight := filepath.Join(loc.localDir, "reconsider.active")
	backoff := time.Second
	for {
		unlock, err := lockBoard(loc.localDir)
		if err != nil {
			return err
		}
		at, err := readHold(atFile)
		if err != nil {
			unlock()
			return err
		}
		if at.IsZero() {
			lock.Close()
			unlock()
			return nil
		}
		delay := time.Until(at)
		if delay > 0 {
			unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(min(delay, time.Minute)):
			}
			continue
		}
		// Consume this request before running. A newer one can arrive while
		// recovery is active, including one it schedules for another reset.
		err = os.Rename(atFile, inFlight)
		unlock()
		if err != nil {
			return err
		}
		cc := *c
		cc.actor = "planner"
		cc.runCache = nil
		cc.shared = &callShared{}
		err = cc.reconsider(ctx, loc)
		cc.closeBoard()
		if err != nil {
			if e := c.scheduleReconsider(loc.localDir, time.Now().Add(backoff)); e != nil {
				return e
			}
			backoff = min(backoff*2, time.Minute)
		} else {
			backoff = time.Second
		}
		if err := os.Remove(inFlight); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
}
func (c *call) reconsider(ctx context.Context, loc storeLoc) error {
	b, err := c.board()
	if err != nil {
		return err
	}
	if _, err := b.Halted(ctx); err != nil {
		return err
	}
	ps, err := loadBindings(filepath.Join(loc.localDir, "profiles.json"))
	if err != nil {
		return err
	}
	meters, err := readUsage(filepath.Join(loc.localDir, "usage.json"))
	if err != nil {
		return err
	}
	for _, alias := range sortedAliases(ps) {
		p := ps[alias]
		if p.Harness != "codex" {
			continue
		}
		refresh := len(meters[alias]) == 0
		for _, w := range meters[alias] {
			if !time.Now().Before(w.Reset) {
				refresh = true
			}
		}
		if refresh {
			st, e := readProfile(ctx, alias, p.Harness, p.ConfigDir)
			if e == nil && st.identity == p.Identity && st.Plan == p.Plan && len(st.Windows) > 0 {
				if err := c.copyNativeUsage(st, ""); err != nil {
					return err
				}
			}
		}
	}
	dir := filepath.Join(loc.localDir, "runs")
	return c.withLook(ctx, func(ctx context.Context) error {
		report, err := c.reconcile(ctx, b, dir, loc.boardID)
		if err != nil {
			return err
		}
		return c.dispatchRuntimeLook(ctx, b, dir, &report, true)
	})
}

func (c *call) runtimeStoreDown(dir string) {
	// Atlas cannot route its own outage. Rate-limit the fixed fault wake locally.
	if account, e := user.Current(); e == nil && account.Username == "maestro" {
		notify := false
		if unlock, e := lockBoard(dir); e == nil {
			gate := filepath.Join(dir, "store-down.until")
			if until, e := readHold(gate); e == nil && !time.Now().Before(until) {
				notify = writeWholeMode(gate, []byte(time.Now().Add(runs.GateWindow).UTC().Format(time.RFC3339Nano)), 0600) == nil
			}
			unlock()
		}
		if notify {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = c.crossDoor(ctx, "maestro-cos", []string{"wake", "store-down"})
		}
	}
	if err := c.scheduleReconsider(dir, time.Now().Add(5*time.Second)); err != nil {
		fmt.Fprintln(c.env.Stderr, "reconnect:", err)
	}
}
