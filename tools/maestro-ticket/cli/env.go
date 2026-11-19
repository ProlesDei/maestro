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

// setupEnv reads ## Env: the NAME=value words each run's session gets,
// and a line for each bullet it skips. Only names live in the file; a
// value goes from the launcher's environment to the session's, never onto
// a command line, into the run's record or into output, so a skip names
// only names. A name code sets (MAESTRO_*) is skipped, as is a source
// that isn't set, so a session never gets an empty secret in its place.
func setupEnv(setup string, getenv func(string) string) (kv, skipped []string) {
	on := false
	seen := map[string]bool{}
	for _, l := range strings.Split(lineBreaks.Replace(setup), "\n") {
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
			// Only what comes before the colon: a value written in by
			// mistake stays out of the output.
			name, _, _ := strings.Cut(l, ":")
			skipped = append(skipped, fmt.Sprintf("%q: want <NAME>: from <NAME in the launcher's environment>", strings.TrimSpace(name)))
		case strings.HasPrefix(strings.ToUpper(m[1]), "MAESTRO_"):
			skipped = append(skipped, fmt.Sprintf("%s: maestro-ticket sets the MAESTRO_ names itself", m[1]))
		case seen[m[1]]:
			skipped = append(skipped, fmt.Sprintf("%s: listed twice; the first stands", m[1]))
		case getenv(m[2]) == "":
			skipped = append(skipped, fmt.Sprintf("%s: %s is not set in the launcher's environment", m[1], m[2]))
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
