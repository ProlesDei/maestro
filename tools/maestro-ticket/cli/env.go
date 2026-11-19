package cli

import (
	"fmt"
	"regexp"
	"strings"
)

// envBullet is a bullet under ## Env in project-setup.md: the name a run's
// session gets, a colon, "from" and the name in the launcher's
// environment that holds the value, such as GH_TOKEN: from
// GITHUB_BLITZLINK_TOKEN.
var envBullet = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*:\s*from\s+([A-Za-z_][A-Za-z0-9_]*)$`)

// shownName is a name a skip line may print: upper case, as environment
// names are, and short. A pasted token such as ghp_… is a valid name too,
// but has lower case or is long, so it prints as "a name".
var shownName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

func shown(name string) string {
	if shownName.MatchString(name) {
		return name
	}
	return "a name"
}

// reservedEnv says why ## Env may not set name, or "" when it may: names
// maestro-ticket sets itself, and names that change what a session runs,
// loads or reads (PATH, HOME, LD_*, GIT_*, CLAUDE_*).
func reservedEnv(name string) string {
	u := strings.ToUpper(name)
	switch {
	case strings.HasPrefix(u, "MAESTRO_"):
		return "maestro-ticket sets the MAESTRO_ names itself"
	case u == "PATH" || u == "HOME":
		return "a run's session keeps the launcher's " + u
	}
	for _, p := range []string{"LD_", "GIT_", "CLAUDE_"} {
		if strings.HasPrefix(u, p) {
			return "a run's session keeps the launcher's " + p + " names"
		}
	}
	return ""
}

// setupEnv reads ## Env: the NAME=value words each run's session gets,
// and a line for each bullet it skips. Only names live in the file; a
// value goes from the launcher's environment to the session's, never onto
// a command line, into the run's record or into output, so a skip prints
// only a line number and names that look like names (see shown). A
// reserved name (see reservedEnv) is skipped, as is a source that isn't
// set, so a session never gets an empty secret in its place.
func setupEnv(setup string, getenv func(string) string) (kv, skipped []string) {
	on := false
	seen := map[string]bool{}
	for i, l := range strings.Split(lineBreaks.Replace(setup), "\n") {
		l = strings.TrimSpace(l)
		if h, ok := strings.CutPrefix(l, "## "); ok {
			on = strings.EqualFold(strings.TrimSpace(h), "env")
			continue
		}
		if !on || !(strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "* ")) {
			continue
		}
		l = strings.ReplaceAll(strings.TrimSpace(l[2:]), "`", "")
		m := envBullet.FindStringSubmatch(l)
		switch {
		case m == nil:
			// The line's number, and its name only when the whole word
			// before a colon is one: a value written in by mistake, as
			// in GH_TOKEN=ghp_… or a bare pasted token, never prints.
			at := fmt.Sprintf("line %d", i+1)
			if name, _, ok := strings.Cut(l, ":"); ok && shownName.MatchString(strings.TrimSpace(name)) {
				at += " (" + strings.TrimSpace(name) + ")"
			}
			skipped = append(skipped, at+": want <NAME>: from <NAME in the launcher's environment>")
		case reservedEnv(m[1]) != "":
			skipped = append(skipped, fmt.Sprintf("%s: %s", shown(m[1]), reservedEnv(m[1])))
		case seen[m[1]]:
			skipped = append(skipped, fmt.Sprintf("%s: listed twice; the first stands", shown(m[1])))
		case getenv(m[2]) == "":
			skipped = append(skipped, fmt.Sprintf("%s: %s is not set in the launcher's environment", shown(m[1]), shown(m[2])))
		default:
			seen[m[1]] = true
			kv = append(kv, m[1]+"="+getenv(m[2]))
		}
	}
	return kv, skipped
}

// runEnv is the NAME=value words ## Env in the main checkout's
// project-setup.md gives a run's session. It reads the main checkout, as
// cache does, so a PR's tree can't name what its own runs get. Outside a
// git checkout, or with no ## Env, it gives none.
func (c *call) runEnv() []string {
	setup, err := c.mainSetup()
	if err != nil || setup == "" {
		return nil
	}
	kv, skipped := setupEnv(setup, c.env.Getenv)
	for _, s := range skipped {
		fmt.Fprintln(c.env.Stderr, "maestro-ticket: env: skipped: "+s)
	}
	return kv
}
