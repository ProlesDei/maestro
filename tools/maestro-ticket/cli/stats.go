package cli

import (
	"context"
	"path/filepath"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/stats"
)

// runStats turns the local run stats on or off for this store, as
// install.sh apply does from install.yaml's stats: key, and records the
// maestro-sha apply pinned. It prints nothing on success. Off stops every
// write and leaves stats.db as it is.
func runStats(ctx context.Context, c *call) error {
	sha := c.fs.String("maestro-sha", "", "the Maestro commit install.yaml pins, recorded with each run")
	if err := c.parse(); err != nil {
		return err
	}
	if len(c.args) != 1 || (c.args[0] != "on" && c.args[0] != "off") {
		return usagef("stats: want on or off")
	}
	if *sha != "" && !stats.ValidSha(*sha) {
		return usagef("stats: --maestro-sha %q: want a commit sha of 7 to 40 lowercase hex", *sha)
	}
	marker, err := c.besideStore(stats.OffFile)
	if err != nil {
		return err
	}
	dir := filepath.Dir(marker)
	if *sha != "" {
		if err := stats.SetSha(dir, *sha); err != nil {
			return err
		}
	}
	return stats.SetOff(dir, c.args[0] == "off")
}
