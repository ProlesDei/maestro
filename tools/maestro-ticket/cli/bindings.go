package cli

import (
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

// Every sub in the pool runs every job. Claude runs the model the job
// picks, Opus or Sonnet at high; Codex runs Astra where Claude would run
// Opus and Sol where it would run Sonnet, at high (owner's ruling, Nov 20
// 2026).
const (
	codexOpus   = "gpt-6.2-astra"
	codexSonnet = "gpt-6.2-sol"
	// defaultReserve is the share of each window kept for the owner.
	defaultReserve = 4.0
	// defaultEstimate is a job's guessed cost in window points until
	// readings teach a better one.
	defaultEstimate = 2.0
)

type jobModel struct {
	Estimate float64
	Model    string
	Effort   string
}

type boundProfile struct {
	Reserve   float64 `json:"reserve_percent"`
	Harness   string  `json:"harness"`
	ConfigDir string  `json:"config_dir,omitempty"`
	Identity  string  `json:"identity"`
	Plan      string  `json:"plan"`
}

func loadBindings(path string) (map[string]boundProfile, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return make(map[string]boundProfile), nil
	}
	if err != nil {
		return nil, err
	}
	var profiles map[string]boundProfile
	if json.Unmarshal(b, &profiles) != nil || profiles == nil {
		return nil, errors.New("profile: invalid pool file")
	}
	return profiles, nil
}

// claudeTier is the Claude model a run's model stands for: sonnet for
// Sonnet or Sol, else opus.
func claudeTier(model string) string {
	if model == modelSonnet || model == codexSonnet {
		return modelSonnet
	}
	return modelOpus
}

// poolModel is the model and effort harness runs a job on that Claude
// would run on model.
func poolModel(harness, model string) jobModel {
	tier := claudeTier(model)
	if harness == "codex" {
		if tier == modelSonnet {
			return jobModel{Model: codexSonnet, Effort: effortHigh}
		}
		return jobModel{Model: codexOpus, Effort: effortHigh}
	}
	return jobModel{Model: tier, Effort: effortHigh}
}

// poolAllows reports whether harness may run model at effort.
func poolAllows(harness, model, effort string) bool {
	return effort == effortHigh && poolModel(harness, model).Model == model
}

func (c *call) addProfile(st profileStatus, config string, reserve float64) error {
	if reserve < 0 || reserve > 100 || math.IsNaN(reserve) {
		return usagef("profile add: reserve must be 0 to 100 percent")
	}
	if st.identity == "" {
		return errors.New("profile add: native account did not report an identity")
	}
	return c.editPool(func(profiles map[string]boundProfile) error {
		if p, ok := profiles[st.Alias]; ok && (p.Identity != st.identity || p.ConfigDir != config || p.Harness != st.Harness) {
			return errors.New("profile add: alias belongs to another login or config directory")
		}
		for alias, other := range profiles {
			if alias != st.Alias && other.Identity == st.identity && other.Harness == st.Harness {
				return errors.New("profile add: account already has an alias")
			}
		}
		profiles[st.Alias] = boundProfile{Reserve: reserve, Harness: st.Harness, ConfigDir: config, Identity: st.identity, Plan: st.Plan}
		return nil
	})
}

func (c *call) removeProfile(alias string) error {
	return c.editPool(func(profiles map[string]boundProfile) error {
		if _, ok := profiles[alias]; !ok {
			return usagef("profile remove: unknown alias")
		}
		delete(profiles, alias)
		return nil
	})
}

func (c *call) editPool(edit func(map[string]boundProfile) error) error {
	path, err := c.besideStore("profiles.json")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	unlock, err := lockBoard(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer unlock()
	profiles, err := loadBindings(path)
	if err != nil {
		return err
	}
	if err := edit(profiles); err != nil {
		return err
	}
	b, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return err
	}
	return writeWholeMode(path, b, 0600)
}

// poolProfile is alias's sub, for a job pinned to it.
func (c *call) poolProfile(alias string) (boundProfile, error) {
	path, err := c.besideStore("profiles.json")
	if err != nil {
		return boundProfile{}, err
	}
	profiles, err := loadBindings(path)
	if err != nil {
		return boundProfile{}, err
	}
	p, ok := profiles[alias]
	if !ok || !runs.BotPattern.MatchString(alias) {
		return p, usagef("profile: no sub %q in the pool", alias)
	}
	return p, nil
}
