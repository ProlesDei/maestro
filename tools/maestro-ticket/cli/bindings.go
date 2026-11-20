package cli

import (
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/ProlesDei/maestro/tools/maestro-ticket/runs"
)

type jobModel struct {
	Estimate float64 `json:"estimate_percent,omitempty"`
	Role     string  `json:"role,omitempty"`
	Model    string  `json:"model"`
	Effort   string  `json:"effort"`
}
type boundProfile struct {
	Harness   string              `json:"harness"`
	ConfigDir string              `json:"config_dir,omitempty"`
	Identity  string              `json:"identity"`
	Plan      string              `json:"plan"`
	Jobs      map[string]jobModel `json:"jobs"`
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
		return nil, errors.New("profile: invalid bindings file")
	}
	return profiles, nil
}

var modelSlug = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

func (c *call) bindProfile(st profileStatus, config, job, model, effort, role string, estimate float64) error {
	if _, ok := findJob(job); !ok || !modelSlug.MatchString(model) || !slices.Contains([]string{"low", "medium", "high", "xhigh", "max"}, effort) {
		return usagef("profile bind: give an eligible --job, --model and --effort")
	}
	if estimate < 0 || estimate > 100 || math.IsNaN(estimate) || role != "" && !runs.BotPattern.MatchString(role) {
		return usagef("profile bind: invalid role or estimated allowance percentage")
	}
	if st.identity == "" {
		return errors.New("profile bind: native account did not report an identity")
	}
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
	p, ok := profiles[st.Alias]
	if ok && (p.Identity != st.identity || p.ConfigDir != config || p.Harness != st.Harness) {
		return errors.New("profile bind: alias belongs to another login or config directory")
	}
	for alias, other := range profiles {
		if alias != st.Alias && other.Identity == st.identity && other.Harness == st.Harness {
			return errors.New("profile bind: account already has an alias")
		}
	}
	if !ok {
		p = boundProfile{Harness: st.Harness, ConfigDir: config, Identity: st.identity, Jobs: map[string]jobModel{}}
	}
	if p.Jobs == nil {
		p.Jobs = make(map[string]jobModel)
	}
	p.Plan = st.Plan
	key := job
	if role != "" {
		key += "/" + role
	}
	p.Jobs[key] = jobModel{Model: model, Effort: effort, Estimate: estimate, Role: role}
	profiles[st.Alias] = p
	b, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return err
	}
	return writeWholeMode(path, b, 0600)
}

func (p boundProfile) binding(job, role string) (jobModel, bool) {
	if m, ok := p.Jobs[job+"/"+role]; ok {
		return m, m.Role == role
	}
	m, ok := p.Jobs[job]
	return m, ok && (m.Role == "" || m.Role == role)
}

func (c *call) jobProfile(alias, job string) (boundProfile, jobModel, error) {
	path, err := c.besideStore("profiles.json")
	if err != nil {
		return boundProfile{}, jobModel{}, err
	}
	profiles, err := loadBindings(path)
	if err != nil {
		return boundProfile{}, jobModel{}, err
	}
	p := profiles[alias]
	m, ok := p.binding(job, c.actor)
	if !ok || !runs.BotPattern.MatchString(alias) {
		return p, m, usagef("profile: alias %q has no binding for job %q", alias, job)
	}
	return p, m, nil
}
