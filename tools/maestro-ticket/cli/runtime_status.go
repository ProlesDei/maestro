package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// Status reads only local usage and run files and verifies the board. A
// failed Atlas connection still leaves a usable local report.
func runRuntimeStatus(ctx context.Context, c *call) error {
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
		return usagef("status: select a T3 runtime board")
	}
	profiles, err := runtimeProfiles(loc.localDir)
	if err != nil {
		return err
	}
	boardStatus := "available"
	s, err := c.open(ctx)
	if err != nil {
		boardStatus = "unavailable"
	} else {
		s.Close()
	}
	return json.NewEncoder(c.env.Stdout).Encode(map[string]any{"as_of": time.Now().UTC(), "board": loc.boardID, "store": boardStatus, "profiles": profiles})
}

type runtimeProfile struct {
	Alias, Harness, Login string
	Reserve               float64 `json:"reserve_percent"`
	Windows               map[string]paceWindow
	HoldUntil             time.Time `json:"hold_until,omitzero"`
	Running               []string
}

func runtimeProfiles(root string) ([]runtimeProfile, error) {
	ps, err := loadBindings(filepath.Join(root, "profiles.json"))
	if err != nil {
		return nil, err
	}
	usage, err := readUsage(filepath.Join(root, "usage.json"))
	if err != nil {
		return nil, err
	}
	all, err := runs.List(filepath.Join(root, "runs"), time.Now(), true)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	profiles := []runtimeProfile{}
	for _, alias := range sortedAliases(ps) {
		p := runtimeProfile{Alias: alias, Reserve: ps[alias].Reserve, Harness: ps[alias].Harness, Login: "verified when added; live login unchecked", Windows: usage[alias], Running: []string{}}
		if _, err := os.Stat(filepath.Join(root, "account-"+alias+".login")); err == nil {
			p.Login = "verification required"
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		for _, scope := range []string{"account-" + alias, "provider-" + p.Harness} {
			hold, err := readHold(filepath.Join(root, scope+".hold"))
			if err != nil {
				return nil, err
			}
			if hold.After(now) && hold.After(p.HoldUntil) {
				p.HoldUntil = hold
			}
		}
		for _, r := range all {
			if !r.Ended() && r.Profile == alias {
				p.Running = append(p.Running, r.ID)
			}
		}
		profiles = append(profiles, p)
	}
	return profiles, nil
}
