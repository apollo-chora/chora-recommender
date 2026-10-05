package agent

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// FewShotExample is one few-shot exchange embedded in the [EXAMPLES] block.
// Three exchanges per learner_persona — no specialisation axis (unlike
// Familiar's 3×3 matrix). The Recommender serves any subject; only the
// learner-axis varies.
type FewShotExample struct {
	UserPrompt     string `yaml:"user_prompt"`
	AssistantReply string `yaml:"assistant_reply"`
}

type fewShotsFile struct {
	LearnerPersona string           `yaml:"learner_persona"`
	Examples       []FewShotExample `yaml:"examples"`
}

//go:embed few_shots/*.yaml
var fewShotFS embed.FS

// FewShotsStore is the loaded matrix indexed by learner_persona.
type FewShotsStore struct {
	byPersona map[string][]FewShotExample
}

func (s *FewShotsStore) Lookup(persona string) ([]FewShotExample, bool) {
	if s == nil {
		return nil, false
	}
	ex, ok := s.byPersona[strings.ToLower(strings.TrimSpace(persona))]
	if !ok || len(ex) == 0 {
		return nil, false
	}
	out := make([]FewShotExample, len(ex))
	copy(out, ex)
	return out, true
}

var (
	defaultFewShotsOnce sync.Once
	defaultFewShots     *FewShotsStore
	defaultFewShotsErr  error
)

func LoadFewShots() (*FewShotsStore, error) {
	defaultFewShotsOnce.Do(func() {
		defaultFewShots, defaultFewShotsErr = loadFewShotsFromFS(fewShotFS)
	})
	return defaultFewShots, defaultFewShotsErr
}

func loadFewShotsFromFS(efs fs.FS) (*FewShotsStore, error) {
	entries, err := fs.ReadDir(efs, "few_shots")
	if err != nil {
		return nil, fmt.Errorf("read few_shots dir: %w", err)
	}
	store := &FewShotsStore{byPersona: make(map[string][]FewShotExample, len(entries))}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := "few_shots/" + e.Name()
		raw, err := fs.ReadFile(efs, path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var ff fewShotsFile
		if err := yaml.Unmarshal(raw, &ff); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if strings.TrimSpace(ff.LearnerPersona) == "" {
			return nil, fmt.Errorf("%s: missing learner_persona", path)
		}
		if len(ff.Examples) != 3 {
			return nil, fmt.Errorf("%s: want 3 examples; got %d", path, len(ff.Examples))
		}
		key := strings.ToLower(strings.TrimSpace(ff.LearnerPersona))
		if _, dup := store.byPersona[key]; dup {
			return nil, fmt.Errorf("duplicate few-shot fixture for persona %s", key)
		}
		store.byPersona[key] = ff.Examples
	}
	if len(store.byPersona) == 0 {
		return nil, fmt.Errorf("no few-shot fixtures found")
	}
	return store, nil
}
