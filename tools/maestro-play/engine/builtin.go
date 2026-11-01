package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ProlesDei/maestro/tools/maestro-play/playbook"
)

// The built-in requests a run: step sends: worktree add, check, head and
// open pr. Each reply has status (ok, failed or refused), exit_code, output
// and the request's fields (playbook.Requests).

// SetupPath is where the project's setup lives, under the main checkout.
const SetupPath = ".maestro/project/project-setup.md"

// projectSetup is what the engine reads from project-setup.md.
type projectSetup struct {
	Path       string
	Name       string
	Summary    string
	MainBranch string
	Checks     []projectCheck
	// BadCheck is the first bullet under ## Checks that isn't a check, as
	// "line <n>: <text>", or "".
	BadCheck string
}

type projectCheck struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

var (
	checkRE  = regexp.MustCompile("^[-*]\\s+([^:`]+?):\\s*`([^`]+)`\\s*$")
	bulletRE = regexp.MustCompile(`^([-*+]|\d+[.)])\s`)
)

// parseSetup reads the ## Project, ## Main branch and ## Checks sections.
// A check is a bullet line: - <name>: `<command>`. Any other bullet there
// is BadCheck, so a typo can't pass as no checks; prose is skipped.
func parseSetup(text string) projectSetup {
	s := projectSetup{}
	section := ""
	for n, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if h, ok := strings.CutPrefix(t, "## "); ok {
			section = strings.ToLower(strings.TrimSpace(h))
			continue
		}
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		switch section {
		case "project":
			key, val, ok := strings.Cut(strings.Trim(strings.TrimLeft(t, "-* "), "* "), ":")
			if !ok {
				continue
			}
			val = strings.TrimSpace(strings.Trim(strings.TrimSpace(val), "*"))
			switch strings.ToLower(strings.Trim(key, "* ")) {
			case "name":
				s.Name = val
			case "summary":
				s.Summary = val
			}
		case "main branch":
			if s.MainBranch == "" {
				s.MainBranch = strings.Trim(strings.TrimLeft(t, "-* "), "`* ")
			}
		case "checks":
			if m := checkRE.FindStringSubmatch(t); m != nil {
				s.Checks = append(s.Checks, projectCheck{Name: strings.Trim(m[1], "* "), Command: m[2]})
			} else if s.BadCheck == "" && bulletRE.MatchString(t) {
				s.BadCheck = fmt.Sprintf("line %d: %s", n+1, t)
			}
		}
	}
	return s
}

// setup reads the main checkout's project-setup.md. A missing file gives
// the defaults: branch main and no checks.
func (e *Engine) setup() projectSetup {
	path := filepath.Join(e.cfg.Dir, SetupPath)
	b, _ := os.ReadFile(path)
	s := parseSetup(string(b))
	s.Path = path
	if s.MainBranch == "" {
		s.MainBranch = "main"
	}
	return s
}

// environment is .env, .project and .limits for a run.
func (e *Engine) environment() map[string]any {
	s := e.setup()
	checks := make([]any, len(s.Checks))
	for i, c := range s.Checks {
		checks[i] = map[string]any{"name": c.Name, "command": c.Command}
	}
	limits := defaultLimits()
	merge(limits, e.cfg.Limits)
	return map[string]any{
		"harness": harnessName(e.cfg.Harness), "family": familyOf(e.cfg.Harness),
		"project": map[string]any{"name": s.Name, "summary": s.Summary, "main_branch": s.MainBranch, "setup": SetupPath, "checks": checks},
		"limits":  limits,
	}
}

// ready says what is missing before a run may start, or "".
func (e *Engine) ready() string {
	if e.cfg.Harness != "claude" && e.cfg.Harness != "codex" {
		return fmt.Sprintf("harness %q: want claude or codex", e.cfg.Harness)
	}
	bin := e.harnessBin(e.cfg.Harness)
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Sprintf("%s not found (%v); install it or set its bin path", bin, err)
	}
	return ""
}

func (e *Engine) runEnv(st *State) []string {
	if st != nil && st.ID != "" {
		return []string{RunEnv + "=" + st.ID}
	}
	return nil
}

// stepEnv is runEnv for sessions and checks, with TMPDIR set to the run's
// temp folder, made on first use under the user's TMPDIR. cleanup removes
// it, so no session has to: claude and grok refuse an rm whose path comes
// from $(...), and codex any rm -f.
func (e *Engine) stepEnv(st *State) []string {
	env := e.runEnv(st)
	if st == nil {
		return env
	}
	if st.Tmp == "" {
		base := ""
		for _, kv := range e.cfg.Environ {
			if v, ok := strings.CutPrefix(kv, "TMPDIR="); ok {
				base = v
			}
		}
		dir, err := os.MkdirTemp(base, "tmp.")
		if err != nil {
			return env
		}
		st.Tmp = dir
	} else if err := os.MkdirAll(st.Tmp, 0o700); err != nil {
		return env
	}
	return append(env, "TMPDIR="+st.Tmp)
}

func newReply(status string, exit int, output string, fields ...any) map[string]any {
	r := map[string]any{"status": status, "exit_code": exit, "output": trimOutput(output)}
	for i := 0; i+1 < len(fields); i += 2 {
		r[fields[i].(string)] = fields[i+1]
	}
	return r
}

func refused(format string, a ...any) map[string]any {
	return newReply("refused", 2, fmt.Sprintf(format, a...))
}

// reqArgs splits a request's words into positionals and the flags it takes;
// any other flag, or one given twice, is an error.
func reqArgs(req *playbook.Request, words []string) ([]string, map[string]string, error) {
	var pos []string
	flags := map[string]string{}
	for i := 0; i < len(words); i++ {
		w := words[i]
		if !strings.HasPrefix(w, "-") {
			pos = append(pos, w)
			continue
		}
		name, val, hasVal := strings.Cut(w, "=")
		name = "--" + strings.TrimLeft(name, "-")
		if !playbook.Contains(req.Flags, name) {
			return nil, nil, fmt.Errorf("%s takes no %s", req.Name, name)
		}
		if _, dup := flags[name]; dup {
			return nil, nil, fmt.Errorf("%s: %s given twice", req.Name, name)
		}
		if !hasVal {
			if i+1 >= len(words) {
				return nil, nil, fmt.Errorf("%s: %s needs a value", req.Name, name)
			}
			i++
			val = words[i]
		}
		flags[name] = val
	}
	if len(pos) < req.MinArgs || len(pos) > req.MaxArgs {
		return nil, nil, fmt.Errorf("%s: want %s", req.Name, req.Usage)
	}
	for _, f := range req.Need {
		if flags[f] == "" {
			return nil, nil, fmt.Errorf("%s: want %s", req.Name, req.Usage)
		}
	}
	return pos, flags, nil
}

// request runs one built-in request for the run.
func (e *Engine) request(st *State, req *playbook.Request, words []string) (map[string]any, error) {
	pos, flags, err := reqArgs(req, words)
	if err != nil {
		return refused("%v", err), nil
	}
	switch req.Name {
	case "worktree add":
		return e.worktreeAdd(st, pos[0], flags["--from"])
	case "check":
		return e.check(st, pos[0])
	case "head":
		return e.head(st)
	case "open pr":
		return e.openPR(st, flags["--title"], flags["--body"])
	}
	return refused("%s: the engine sends it itself", req.Name), nil
}

// git runs git in dir.
func (e *Engine) git(st *State, dir string, args ...string) (execResult, error) {
	return e.exec(dir, e.cfg.GitBin, e.noPromptEnv(st), "", e.cfg.GitTimeout, args...)
}

func (e *Engine) gh(st *State, dir string, args ...string) (execResult, error) {
	return e.exec(dir, e.cfg.GhBin, e.noPromptEnv(st), "", e.cfg.GitTimeout, args...)
}

// noPromptEnv is runEnv for git and gh. A command in its own process group
// stops when it reads the terminal, so a password or host-key prompt would
// hang it until the git timeout; these make git, ssh and gh fail at once
// instead. The user's own ssh command (GIT_SSH_COMMAND, GIT_SSH or
// core.sshCommand) stays.
func (e *Engine) noPromptEnv(st *State) []string {
	env := append(e.runEnv(st), "GIT_TERMINAL_PROMPT=0", "GH_PROMPT_DISABLED=1")
	e.sshOnce.Do(func() {
		for _, kv := range e.cfg.Environ {
			k, v, _ := strings.Cut(kv, "=")
			if (k == "GIT_SSH_COMMAND" || k == "GIT_SSH") && v != "" {
				e.userSSH = true
				return
			}
		}
		r, err := e.exec(e.cfg.Dir, e.cfg.GitBin, env, "", e.cfg.GitTimeout, "config", "--get", "core.sshCommand")
		e.userSSH = err == nil && r.exit == 0 && strings.TrimSpace(r.stdout) != ""
	})
	if !e.userSSH {
		env = append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	return env
}

// workDir is where a run's commands run: its worktree, else the main
// checkout.
func (e *Engine) workDir(st *State) string {
	if st.Worktree != "" {
		return st.Worktree
	}
	return e.cfg.Dir
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// worktreeAdd gives the ticket its branch maestro/<ticket> and worktree
// .maestro/worktrees/<ticket>, or reuses them, so a resumed run lands in
// the same place.
func (e *Engine) worktreeAdd(st *State, ticket, from string) (map[string]any, error) {
	if !safeID.MatchString(ticket) {
		return refused("worktree add: ticket %q: want letters, digits, - and _", ticket), nil
	}
	if strings.HasPrefix(from, "-") {
		return refused("worktree add: --from %q", from), nil
	}
	branch := "maestro/" + ticket
	path := filepath.Join(e.cfg.Dir, ".maestro", "worktrees", ticket)
	ok := func(out string) map[string]any { return newReply("ok", 0, out, "path", path, "branch", branch) }
	if _, err := os.Stat(path); err == nil {
		r, err := e.git(st, path, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return nil, err
		}
		if got := strings.TrimSpace(r.stdout); r.exit != 0 || got != branch {
			return newReply("failed", 1, fmt.Sprintf("%s exists on %q, not %s: %s", path, got, branch, r.stderr)), nil
		}
		return ok("reused " + path + e.linkSkills(st, path)), nil
	}
	e.hideWorktrees(st)
	if _, err := e.git(st, e.cfg.Dir, "worktree", "prune"); err != nil {
		return nil, err
	}
	args := []string{"worktree", "add", "-b", branch, path, from}
	if r, err := e.git(st, e.cfg.Dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		return nil, err
	} else if r.exit == 0 {
		args = []string{"worktree", "add", path, branch}
	}
	r, err := e.git(st, e.cfg.Dir, args...)
	if err != nil {
		return nil, err
	}
	if r.exit != 0 {
		return newReply("failed", r.exit, r.stdout+r.stderr), nil
	}
	return ok(r.stdout + r.stderr + e.linkSkills(st, path)), nil
}

// linkSkills links each skill folder git ignores in the main checkout's
// .agents/skills into the worktree, and says what it couldn't link. Codex
// looks for skills only up to the worktree's root, so it would miss the
// hidden copies an install makes; Claude Code finds the main checkout's
// .claude/skills itself. A link git wouldn't ignore in the worktree is
// removed: it would leave git status dirty.
func (e *Engine) linkSkills(st *State, worktree string) string {
	src := filepath.Join(e.cfg.Dir, ".agents", "skills")
	entries, err := os.ReadDir(src)
	if err != nil {
		return ""
	}
	var missed []string
	for _, d := range entries {
		rel := filepath.Join(".agents", "skills", d.Name())
		dest := filepath.Join(worktree, rel)
		if _, err := os.Lstat(dest); err == nil {
			continue
		}
		if r, err := e.git(st, e.cfg.Dir, "check-ignore", "-q", "--", rel); err != nil || r.exit != 0 {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			missed = append(missed, d.Name())
			continue
		}
		if err := os.Symlink(filepath.Join(src, d.Name()), dest); err != nil {
			missed = append(missed, d.Name())
			continue
		}
		if r, err := e.git(st, worktree, "check-ignore", "-q", "--", rel); err != nil || r.exit != 0 {
			os.Remove(dest)
			missed = append(missed, d.Name())
		}
	}
	if len(missed) == 0 {
		return ""
	}
	return "\nskills not linked into the worktree: " + strings.Join(missed, " ")
}

// hideWorktrees adds .maestro/worktrees/ to the repo's info/exclude, so
// the main checkout's git status doesn't list the worktrees.
func (e *Engine) hideWorktrees(st *State) {
	r, err := e.git(st, e.cfg.Dir, "rev-parse", "--git-common-dir")
	if err != nil || r.exit != 0 {
		return
	}
	common := strings.TrimSpace(r.stdout)
	if !filepath.IsAbs(common) {
		common = filepath.Join(e.cfg.Dir, common)
	}
	path := filepath.Join(common, "info", "exclude")
	b, _ := os.ReadFile(path)
	const line = "/.maestro/worktrees/"
	if strings.Contains(string(b), line) {
		return
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
		f.WriteString("\n")
	}
	f.WriteString(line + "\n")
}

// check runs the project's checks, all of them or the one named, in order,
// in the run's worktree, each for at most CheckTimeout. Every check runs,
// so the reply lists every failure; the full output goes to output_path.
// A project may have no checks: check all then passes with none run. A
// bullet under ## Checks that isn't a check fails every check request.
func (e *Engine) check(st *State, name string) (map[string]any, error) {
	s := e.setup()
	if s.BadCheck != "" {
		return newReply("failed", 1, fmt.Sprintf("%s: %s: under ## Checks, want - <name>: `<command>`", s.Path, s.BadCheck),
			"summary", "bad ## Checks line", "checks", []any{}, "output_path", ""), nil
	}
	var run []projectCheck
	for _, c := range s.Checks {
		if name == "all" || c.Name == name {
			run = append(run, c)
		}
	}
	if len(run) == 0 {
		if name == "all" {
			return newReply("ok", 0, "no checks under ## Checks in "+s.Path, "summary", "no checks listed",
				"checks", []any{}, "output_path", ""), nil
		}
		return newReply("failed", 1, fmt.Sprintf("no check %s under ## Checks in %s", name, s.Path)), nil
	}
	dir := runDir(e.cfg.Common, st.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	n := 1
	for ; ; n++ {
		if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("check-%d.txt", n))); os.IsNotExist(err) {
			break
		}
	}
	outPath := filepath.Join(dir, fmt.Sprintf("check-%d.txt", n))
	// Checks on files the branch lacks would pass for a sha that fails.
	if msg, err := e.dirty(st, "check"); err != nil || msg != "" {
		if err == nil {
			err = os.WriteFile(outPath, []byte(msg+"\n"), 0o644)
		}
		if err != nil {
			return nil, err
		}
		return newReply("failed", 1, msg, "summary", "uncommitted changes", "checks", []any{}, "output_path", outPath), nil
	}
	var full, short strings.Builder
	var checks []any
	var failed []string
	exit := 0
	for _, c := range run {
		r, err := e.exec(e.workDir(st), "sh", e.stepEnv(st), "", e.cfg.CheckTimeout, "-c", c.Command)
		if err != nil {
			return nil, err
		}
		status := "ok"
		if r.exit != 0 {
			status = "failed"
			failed = append(failed, c.Name)
			if exit == 0 {
				exit = r.exit
			}
			fmt.Fprintf(&short, "%s: exit %d\n%s\n", c.Name, r.exit, trimOutput(r.stdout+r.stderr))
		}
		fmt.Fprintf(&full, "== %s: %s (exit %d)\n%s%s\n", c.Name, c.Command, r.exit, r.stdout, r.stderr)
		checks = append(checks, map[string]any{"name": c.Name, "command": c.Command, "exit_code": r.exit, "status": status})
	}
	if err := os.WriteFile(outPath, []byte(full.String()), 0o644); err != nil {
		return nil, err
	}
	if len(failed) == 0 {
		return newReply("ok", 0, full.String(), "summary", fmt.Sprintf("%d passed", len(run)), "checks", checks, "output_path", outPath), nil
	}
	summary := fmt.Sprintf("%d of %d failed: %s", len(failed), len(run), strings.Join(failed, ", "))
	return newReply("failed", exit, short.String(), "summary", summary, "checks", checks, "output_path", outPath), nil
}

// dirty says what the run's worktree holds that no commit does, or "". A
// run with no worktree has nothing to say.
func (e *Engine) dirty(st *State, req string) (string, error) {
	if st.Worktree == "" {
		return "", nil
	}
	r, err := e.git(st, st.Worktree, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if r.exit != 0 {
		return fmt.Sprintf("%s: git status: exit %d: %s", req, r.exit, r.stdout+r.stderr), nil
	}
	if out := strings.TrimRight(r.stdout, "\n"); out != "" {
		return fmt.Sprintf("%s: %s has uncommitted changes; commit or remove them:\n%s", req, st.Worktree, out), nil
	}
	return "", nil
}

// head is the sha the run's worktree is at. It fails while the worktree
// holds uncommitted changes, which the sha doesn't have.
func (e *Engine) head(st *State) (map[string]any, error) {
	if msg, err := e.dirty(st, "head"); err != nil || msg != "" {
		if err != nil {
			return nil, err
		}
		return newReply("failed", 1, msg), nil
	}
	r, err := e.git(st, e.workDir(st), "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	if r.exit != 0 {
		return newReply("failed", r.exit, r.stderr), nil
	}
	return newReply("ok", 0, "", "sha", strings.TrimSpace(r.stdout)), nil
}

// openPR pushes the run's branch to origin and opens a pull request into
// the main branch, or returns the one already open, so a resumed run or a
// fix after review updates the same PR. It pushes only a clean worktree
// with commits the main branch lacks.
func (e *Engine) openPR(st *State, title, body string) (map[string]any, error) {
	if st.Branch == "" || st.Worktree == "" {
		return newReply("failed", 1, "open pr: the run has no branch; run worktree add first"), nil
	}
	dir := st.Worktree
	if msg, err := e.dirty(st, "open pr"); err != nil || msg != "" {
		if err != nil {
			return nil, err
		}
		return newReply("failed", 1, msg), nil
	}
	base := e.setup().MainBranch
	r, err := e.git(st, dir, "rev-list", "--count", base+"..HEAD")
	if err != nil {
		return nil, err
	}
	if r.exit != 0 {
		return newReply("failed", r.exit, "open pr: git rev-list "+base+"..HEAD: "+r.stdout+r.stderr), nil
	}
	if strings.TrimSpace(r.stdout) == "0" {
		return newReply("failed", 1, fmt.Sprintf("open pr: %s has no commits beyond %s", st.Branch, base)), nil
	}
	r, err = e.git(st, dir, "push", "-u", "origin", st.Branch)
	if err != nil {
		return nil, err
	}
	if r.exit != 0 {
		return newReply("failed", r.exit, "git push: "+r.stdout+r.stderr), nil
	}
	pushed := r.stdout + r.stderr
	found, res, err := e.findPR(st, dir)
	if err != nil || res != nil {
		return res, err
	}
	if found != nil {
		return newReply("ok", 0, pushed+"PR already open: "+str(found["url"]), "url", str(found["url"]), "number", toInt(found["number"])), nil
	}
	if title == "" {
		title = firstNonEmpty(st.Ticket, "maestro-play run "+st.ID)
		if t := str(st.TicketD["title"]); t != "" {
			title += ": " + t
		}
	}
	if body == "" {
		body = fmt.Sprintf("Ticket %s.\n\nOpened by maestro-play run %s.", st.Ticket, st.ID)
	}
	r, err = e.gh(st, dir, "pr", "create", "--head", st.Branch, "--base", base, "--title", title, "--body", body)
	if err != nil {
		return nil, err
	}
	if r.exit != 0 {
		return newReply("failed", r.exit, "gh pr create: "+r.stdout+r.stderr), nil
	}
	found, res, err = e.findPR(st, dir)
	if err != nil || res != nil {
		return res, err
	}
	if found == nil {
		return newReply("failed", 1, "gh pr create succeeded but gh pr list finds no open PR for "+st.Branch), nil
	}
	return newReply("ok", 0, r.stdout, "url", str(found["url"]), "number", toInt(found["number"])), nil
}

// findPR is the open PR from the run's branch, or nil.
func (e *Engine) findPR(st *State, dir string) (map[string]any, map[string]any, error) {
	r, err := e.gh(st, dir, "pr", "list", "--head", st.Branch, "--state", "open", "--json", "number,url,isCrossRepository")
	if err != nil {
		return nil, nil, err
	}
	if r.exit != 0 {
		return nil, newReply("failed", r.exit, "gh pr list: "+r.stdout+r.stderr), nil
	}
	var list []any
	if decodeJSON([]byte(r.stdout), &list) != nil {
		return nil, newReply("failed", 1, "gh pr list: not JSON: "+r.stdout), nil
	}
	// --head matches a fork's branch of the same name too; skip those.
	for _, v := range list {
		if m, _ := v.(map[string]any); m != nil && m["isCrossRepository"] != true {
			return m, nil, nil
		}
	}
	return nil, nil, nil
}
