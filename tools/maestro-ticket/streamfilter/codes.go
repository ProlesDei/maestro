package streamfilter

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// Other is the code of a tool off every list. Codes leaves it out: the
// caller's code list adds it to every kind.
const Other = "other"

// tools are Claude Code's built-in tools, each its own code. Skill is
// coded by the skill it names, Bash and Read by what they run or read.
var tools = []string{
	"Agent", "AskUserQuestion", "Bash", "BashOutput", "Edit", "EnterPlanMode", "ExitPlanMode",
	"Glob", "Grep", "KillBash", "KillShell", "LS", "MultiEdit", "NotebookEdit", "NotebookRead",
	"Read", "SlashCommand", "Task", "TaskOutput", "TodoRead", "TodoWrite", "ToolSearch",
	"WebFetch", "WebSearch", "Write",
}

// skillFiles are Maestro's shipped skills, each with the stems of its
// references/*.md and scripts/*.sh: a read or run of one of these is
// coded by name, and of any other file by its tool alone. A test holds
// the list to skills/.
var skillFiles = map[string]struct{ refs, scripts []string }{
	"maestro-chief-of-staff":     {},
	"maestro-code-review":        {refs: []string{"passes"}, scripts: []string{"review-prep"}},
	"maestro-create-edit-skill":  {},
	"maestro-create-ticket":      {},
	"maestro-designer":           {refs: []string{"design"}, scripts: []string{"design-pr"}},
	"maestro-figure-it-out":      {},
	"maestro-planner":            {refs: []string{"finish", "judge", "plan", "rule"}},
	"maestro-prove-complete":     {},
	"maestro-prune":              {refs: []string{"code"}},
	"maestro-qa-lead":            {refs: []string{"browser", "live-run", "test-a-pr"}, scripts: []string{"qa-prep", "screens"}},
	"maestro-recall":             {},
	"maestro-refactor-code":      {},
	"maestro-sharpen":            {refs: []string{"example"}},
	"maestro-software-engineer":  {},
	"maestro-swarm":              {},
	"maestro-tdd":                {},
	"maestro-technical-writing":  {refs: []string{"example"}},
	"maestro-unslop":             {refs: []string{"tells"}},
	"maestro-working-principles": {refs: []string{"build"}},
}

// Skills are the shipped skills skillFiles names, sorted.
func Skills() []string {
	out := make([]string, 0, len(skillFiles))
	for s := range skillFiles {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// skillStems are the reference and script stems skillFiles keeps for a
// shipped skill.
func skillStems(skill string) (refs, scripts []string) {
	f := skillFiles[skill]
	return slices.Clone(f.refs), slices.Clone(f.scripts)
}

// gitSubs are the git and gh subcommands coded by name.
var gitSubs = []string{"checks", "commit", "merge", "pr", "push", "worktree"}

// bashWords are the first words of a Bash command coded by name.
var bashWords = []string{"cargo", "docker", "go", "make", "npm", "playwright-cli", "pnpm", "pytest", "yarn"}

// Codes is every step code Scan can give with n, but Other.
func Codes(n Names) []string {
	out := append([]string{"mcp", "S:other"}, tools...)
	for _, s := range Skills() {
		f := skillFiles[s]
		out = append(out, "S:"+s, "R:"+s)
		for _, r := range f.refs {
			out = append(out, "R:"+s+"/"+r)
		}
		for _, x := range f.scripts {
			out = append(out, "X:"+s+"/"+x)
		}
	}
	for _, s := range n.Sub {
		out = append(out, "mt:"+s)
	}
	for _, s := range gitSubs {
		out = append(out, "git:"+s, "gh:"+s)
	}
	for _, w := range bashWords {
		out = append(out, "Bash:"+w)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

var (
	// A read of a shipped skill's SKILL.md or of one of its references.
	readPath = regexp.MustCompile(`(?:^|/)(maestro-[a-z-]+)/(?:SKILL\.md|references/([a-z0-9-]+)\.md)$`)
	// A run of a shipped skill's script, alone or through bash or sh.
	scriptRun = regexp.MustCompile(`^(?:(?:bash|sh)\s+)?(?:\S*/)?(maestro-[a-z-]+)/scripts/([a-z0-9-]+)\.sh(?:\s|$)`)
	// A leading cd that a command runs the rest of itself after.
	leadingCd = regexp.MustCompile(`^cd\s+(?:"[^"]*"|'[^']*'|\S+)\s*&&\s*`)
	envWord   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
)

// code is a tool call's step code.
func (n Names) code(c block) string {
	switch {
	case c.Name == "Bash":
		return n.bash(c.Input.Command)
	case c.Name == "Read":
		return readCode(c.Input.FilePath)
	case c.Name == "Skill":
		if _, ok := skillFiles[c.Input.Skill]; ok {
			return "S:" + c.Input.Skill
		}
		return "S:other"
	case slices.Contains(tools, c.Name):
		return c.Name
	case strings.HasPrefix(c.Name, "mcp__"):
		return "mcp"
	}
	return Other
}

func readCode(p string) string {
	m := readPath.FindStringSubmatch(p)
	if m == nil {
		return "Read"
	}
	f, ok := skillFiles[m[1]]
	switch {
	case !ok:
		return "Read"
	case m[2] == "":
		return "R:" + m[1]
	case slices.Contains(f.refs, m[2]):
		return "R:" + m[1] + "/" + m[2]
	}
	return "Read"
}

// bash codes a Bash command by what it runs first: a shipped script, a
// maestro-ticket subcommand, a git or gh subcommand on the list, or a
// first word on the list; else Bash.
func (n Names) bash(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	for {
		loc := leadingCd.FindStringIndex(cmd)
		if loc == nil {
			break
		}
		cmd = cmd[loc[1]:]
	}
	if m := scriptRun.FindStringSubmatch(cmd); m != nil {
		if f, ok := skillFiles[m[1]]; ok && slices.Contains(f.scripts, m[2]) {
			return "X:" + m[1] + "/" + m[2]
		}
		return "Bash"
	}
	words := strings.Fields(cmd)
	for len(words) > 0 && envWord.MatchString(words[0]) {
		words = words[1:]
	}
	if len(words) == 0 {
		return "Bash"
	}
	first, rest := path.Base(words[0]), words[1:]
	switch first {
	case "maestro-ticket":
		if sub := firstSub(rest, nil); slices.Contains(n.Sub, sub) {
			return "mt:" + sub
		}
		return "Bash"
	case "git", "gh":
		// git -C <dir> and -c <k=v> take a word.
		if sub := firstSub(rest, []string{"-C", "-c"}); slices.Contains(gitSubs, sub) {
			return first + ":" + sub
		}
		return "Bash"
	}
	if slices.Contains(bashWords, first) {
		return "Bash:" + first
	}
	return "Bash"
}

// firstSub is the first word that isn't a flag, skipping the word after
// each flag in takes.
func firstSub(words, takes []string) string {
	for i := 0; i < len(words); i++ {
		w := words[i]
		if !strings.HasPrefix(w, "-") {
			return w
		}
		if slices.Contains(takes, w) {
			i++
		}
	}
	return ""
}
