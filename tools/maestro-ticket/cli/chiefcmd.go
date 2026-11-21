package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/chief"
	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// faultFile names the chief thread that hears of a store outage. The
// newest registration writes it; an outage can't read the registry.
const faultFile = "chief-fault-thread"

// chiefVerbs are the chief of staff's own commands, each one JSON request
// on stdin.
var chiefVerbs = []string{"register", "assign", "draft", "publish", "read", "instruction", "result", "direction", "propose", "claim", "question", "answer", "rule", "ack", "prepare"}

// runChief is the chief of staff's side of the board: goal conversations,
// drafts, decisions and owner rules. The session is the T3 thread that runs it.
func runChief(ctx context.Context, c *call) error {
	if err := c.parse(); err != nil {
		return err
	}
	if len(c.args) != 1 || !slices.Contains(chiefVerbs, c.args[0]) {
		return usagef("chief: give one verb: %s", strings.Join(chiefVerbs, "|"))
	}
	verb := c.args[0]
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s, cfg, err := c.chiefService(ctx)
	if err != nil {
		return err
	}
	defer s.Close(ctx)
	c.recoverAfterBoot()
	if verb == "prepare" {
		return chief.DescribeError(s.Prepare(ctx))
	}
	dec := json.NewDecoder(io.LimitReader(c.env.Stdin, 1<<20))
	dec.DisallowUnknownFields()
	var req chief.Request
	if err := dec.Decode(&req); err != nil {
		return usagef("chief: invalid request")
	}
	if dec.Decode(new(any)) != io.EOF {
		return usagef("chief: expected one request")
	}
	thread := c.env.Getenv("T3CODE_THREAD_ID")
	var out any
	if verb == "answer" {
		out, err = s.Capture(ctx, cfg.T3Base, thread, req)
	} else {
		out, err = s.Apply(ctx, thread, verb, req)
	}
	if err != nil {
		return chief.DescribeError(err)
	}
	if verb == "register" {
		if loc, e := c.location(); e == nil {
			if e := writeWholeMode(filepath.Join(loc.localDir, faultFile), []byte(thread+"\n"), 0600); e != nil {
				return e
			}
		}
	}
	return json.NewEncoder(c.env.Stdout).Encode(out)
}

// chiefConfig is the chief's view of this repo's board: the same
// connection and pin, T3's data folder from MAESTRO_T3_BASE, and the
// thread that hears of outages.
func (c *call) chiefConfig() (chief.Config, string, error) {
	loc, err := c.location()
	if err != nil {
		return chief.Config{}, "", err
	}
	cfg := chief.Config{URI: loc.uri, Board: loc.boardID, T3Base: c.env.Getenv("MAESTRO_T3_BASE")}
	if b, err := os.ReadFile(filepath.Join(loc.localDir, faultFile)); err == nil {
		cfg.FaultThread = strings.TrimSpace(string(b))
	}
	return cfg, loc.localDir, nil
}

func (c *call) chiefService(ctx context.Context) (*chief.Service, chief.Config, error) {
	cfg, _, err := c.chiefConfig()
	if err != nil {
		return nil, cfg, err
	}
	if cfg.URI == "" || cfg.Board == "" {
		return nil, cfg, usagef("chief: set up the board first: maestro-ticket store init --uri-file -")
	}
	s, err := chief.Open(ctx, cfg)
	return s, cfg, err
}

// hook is bot's runs.Hook, the chief's waking its T3 thread through this
// call's board. The wake drops the ring's body: no worker text reaches
// the chief's prompt.
func (c *call) hook(bot string) runs.Hook {
	h := runs.Serving(bot)
	if h.Chief {
		h.Wake = func(ctx context.Context, _ runs.Notification) error { return c.wakeChief(ctx) }
	}
	return h
}

// wakeChief wakes each registered chief thread with pending inbox items.
// No worker text enters the T3 prompt.
func (c *call) wakeChief(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s, cfg, err := c.chiefService(ctx)
	if err != nil {
		return err
	}
	defer s.Close(ctx)
	return s.Wake(ctx, cfg.T3Base)
}

// wakeStoreDown tells the newest registered chief thread the store is out.
func (c *call) wakeStoreDown(ctx context.Context) error {
	cfg, _, err := c.chiefConfig()
	if err != nil {
		return err
	}
	return chief.SendWake(ctx, cfg.T3Base, cfg.FaultThread)
}

func runWake(ctx context.Context, c *call) error {
	if err := c.parse(); err != nil {
		return err
	}
	switch {
	case len(c.args) == 0:
		return c.wakeChief(ctx)
	case len(c.args) == 1 && c.args[0] == "store-down":
		return c.wakeStoreDown(ctx)
	}
	return usagef("wake takes only the optional word store-down")
}

// recoverAfterBoot runs recovery once per machine boot, from the first
// chief or cos command after it, so no service needs installing. It
// starts recover in the background and never waits on it.
func (c *call) recoverAfterBoot() {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return
	}
	loc, err := c.location()
	if err != nil || loc.boardID == "" {
		return
	}
	dir := loc.localDir
	unlock, err := lockBoard(dir)
	if err != nil {
		return
	}
	defer unlock()
	mark := filepath.Join(dir, "recovered-boot")
	id := strings.TrimSpace(string(boot))
	if b, err := os.ReadFile(mark); err == nil && strings.TrimSpace(string(b)) == id {
		return
	}
	store, _, err := c.storePath()
	if err != nil {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "recover", "--store", store, "--actor", "planner")
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, runs.RunIDEnv+"=") })
	cmd.SysProcAttr = runs.Detach()
	cmd.Dir = c.env.Dir
	if startSupervisor(cmd) != nil {
		return
	}
	writeWholeMode(mark, []byte(id+"\n"), 0600)
}
