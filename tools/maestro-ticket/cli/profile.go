package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

type allowanceWindow struct {
	UsedPercent *float64 `json:"usedPercent"`
	Minutes     *int     `json:"windowDurationMins"`
	Reset       *int64   `json:"resetsAt"`
}

type profileStatus struct {
	identity string
	Alias    string                     `json:"alias"`
	Harness  string                     `json:"harness"`
	Plan     string                     `json:"plan"`
	Billing  string                     `json:"billing"`
	ReadAt   time.Time                  `json:"read_at"`
	Windows  map[string]allowanceWindow `json:"windows"`
}

// Each probe gets the chosen native config. Drop inherited provider overrides
// so a status read cannot silently inspect a different endpoint or account.
func profileCommand(ctx context.Context, harness, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, harness, args...)
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		switch key {
		case "HOME", "PATH", "USER", "LANG", "TMPDIR", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS", "SYSTEMROOT", "SystemRoot", "USERPROFILE", "APPDATA", "LOCALAPPDATA":
			cmd.Env = append(cmd.Env, item)
		}
	}
	if dir != "" {
		key := "CLAUDE_CONFIG_DIR"
		if harness == "codex" {
			key = "CODEX_HOME"
		}
		cmd.Env = append(cmd.Env, key+"="+dir)
	}
	return cmd
}

func runProfile(ctx context.Context, c *call) error {
	harness := c.fs.String("harness", "", "native harness: claude or codex")
	dir := c.fs.String("config-dir", "", "absolute native profile directory (default: standard login)")
	reserve := c.fs.Float64("reserve-percent", defaultReserve, "allowance points kept for the owner in each window")
	window := c.fs.String("window", "", "manually observed window name")
	reset := c.fs.String("reset", "", "observed reset timestamp")
	used := c.fs.Float64("used-percent", -1, "observed percentage used")
	if err := c.parse(); err != nil {
		return err
	}
	if len(c.args) == 1 && c.args[0] == "usage" {
		return c.usageStatus()
	}
	if len(c.args) == 2 && c.args[0] == "sample" {
		alias := c.args[1]
		path, err := c.besideStore("profiles.json")
		if err != nil {
			return err
		}
		ps, err := loadBindings(path)
		if err != nil {
			return err
		}
		p, ok := ps[alias]
		if !ok {
			return usagef("profile sample: unknown alias")
		}
		st, err := readProfile(ctx, alias, p.Harness, p.ConfigDir)
		if err != nil {
			return err
		}
		if st.identity != p.Identity {
			return errors.New("profile sample: login changed")
		}
		if st.Plan != p.Plan {
			return errors.New("profile sample: subscription changed")
		}
		marker := filepath.Join(filepath.Dir(path), "account-"+alias+".login")
		if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if *window != "" || *reset != "" || *used != -1 {
			err = c.recordUsage(alias, *window, *reset, *used)
		} else {
			err = c.copyNativeUsage(st, "")
		}
		if err == nil {
			if loc, e := c.location(); e == nil && loc.boardID != "" && codeRole(filepath.Join(loc.localDir, "notify"), "planner") {
				c.refill(ctx, filepath.Join(loc.localDir, "runs"))
			}
		}
		return err
	}
	if len(c.args) == 2 && c.args[0] == "remove" {
		if err := c.removeProfile(c.args[1]); err != nil {
			return err
		}
		return c.out(map[string]string{"removed": c.args[1]}, func(w io.Writer) { fmt.Fprintf(w, "%s: removed from the pool\n", c.args[1]) })
	}
	if len(c.args) >= 1 && c.args[0] == "bind" {
		return usagef("profile bind is gone: profile add <alias> --harness claude|codex puts a sub in the pool for every job")
	}
	add := len(c.args) == 2 && c.args[0] == "add"
	if add {
		c.args = c.args[1:]
	}
	if len(c.args) != 1 || !runs.BotPattern.MatchString(c.args[0]) || (*harness != "claude" && *harness != "codex") {
		return usagef("profile: give an account alias and --harness claude or codex")
	}
	if *dir != "" {
		if !filepath.IsAbs(*dir) {
			return usagef("profile: --config-dir must be absolute")
		}
		info, err := os.Stat(*dir)
		if err != nil || !info.IsDir() {
			return usagef("profile: config directory is unavailable")
		}
	}
	st, err := readProfile(ctx, c.args[0], *harness, *dir)
	if err != nil {
		return err
	}
	if add {
		if err = c.addProfile(st, *dir, *reserve); err != nil {
			return err
		}
		if len(st.Windows) > 0 {
			if err := c.copyNativeUsage(st, ""); err != nil {
				fmt.Fprintln(c.env.Stderr, "usage:", err)
			}
		}
	}
	return c.out(st, func(w io.Writer) {
		fmt.Fprintf(w, "%s: %s %s subscription", st.Alias, st.Harness, st.Plan)
		if st.Windows == nil {
			fmt.Fprintln(w, ", usage unavailable")
			return
		}
		fmt.Fprintln(w)
		for _, name := range []string{"primary", "secondary"} {
			window, ok := st.Windows[name]
			if !ok {
				continue
			}
			fmt.Fprintf(w, "%s:", name)
			if window.UsedPercent != nil {
				fmt.Fprintf(w, " %g%% used", *window.UsedPercent)
			}
			if window.Minutes != nil {
				fmt.Fprintf(w, " over %d minutes", *window.Minutes)
			}
			if window.Reset != nil {
				fmt.Fprintf(w, ", resets %s", time.Unix(*window.Reset, 0).UTC().Format(time.RFC3339))
			}
			fmt.Fprintln(w)
		}
	})
}

func readProfile(ctx context.Context, alias, harness, dir string) (profileStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	st := profileStatus{Alias: alias, Harness: harness, Billing: "subscription"}
	if harness == "claude" {
		data, err := profileCommand(ctx, "claude", dir, "auth", "status", "--json").Output()
		var auth struct {
			LoggedIn                                         bool
			Email, AuthMethod, APIProvider, SubscriptionType string
		}
		if err != nil || json.Unmarshal(data, &auth) != nil || !auth.LoggedIn || auth.AuthMethod != "claude.ai" || auth.APIProvider != "firstParty" || auth.SubscriptionType == "" {
			return st, errors.New("profile: Claude subscription login could not be verified")
		}
		st.Plan = auth.SubscriptionType
		st.identity = auth.Email
	} else if err := codexStatus(ctx, dir, &st); err != nil {
		return st, err
	}
	st.ReadAt = time.Now().UTC()
	if st.identity != "" {
		sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(st.identity))))
		st.identity = hex.EncodeToString(sum[:])
	}
	return st, nil
}

func codexStatus(ctx context.Context, dir string, st *profileStatus) error {
	cmd := profileCommand(ctx, "codex", dir, "app-server")
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return errors.New("profile: could not start codex app-server")
	}
	defer func() { in.Close(); cmd.Process.Kill(); cmd.Wait() }()
	enc, dec := json.NewEncoder(in), json.NewDecoder(out)
	call := func(id int, method string, params any, result any) error {
		if err := enc.Encode(map[string]any{"id": id, "method": method, "params": params}); err != nil {
			return err
		}
		for {
			var msg struct {
				ID     int
				Result json.RawMessage
			}
			if err := dec.Decode(&msg); err != nil {
				return err
			}
			if msg.ID == id {
				return json.Unmarshal(msg.Result, result)
			}
		}
	}
	var init json.RawMessage
	if err := call(1, "initialize", map[string]any{"clientInfo": map[string]string{"name": "maestro", "version": "1"}}, &init); err != nil {
		return errors.New("profile: codex app-server initialization failed")
	}
	var auth struct {
		Account struct{ Email, Type, PlanType string }
	}
	if err := call(2, "account/read", struct{}{}, &auth); err != nil || auth.Account.Type != "chatgpt" || auth.Account.PlanType == "" {
		return errors.New("profile: ChatGPT login could not be verified")
	}
	st.Plan = auth.Account.PlanType
	st.identity = auth.Account.Email
	var meter struct {
		RateLimits struct{ Primary, Secondary *allowanceWindow }
	}
	if err := call(3, "account/rateLimits/read", struct{}{}, &meter); err != nil {
		return nil
	}
	for name, window := range map[string]*allowanceWindow{"primary": meter.RateLimits.Primary, "secondary": meter.RateLimits.Secondary} {
		if window != nil {
			if st.Windows == nil {
				st.Windows = make(map[string]allowanceWindow)
			}
			st.Windows[name] = *window
		}
	}
	return nil
}
