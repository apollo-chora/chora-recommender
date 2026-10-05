package agent

import (
	"strings"
	"testing"
	"testing/fstest"
)

// ComposeRecommenderInstruction emits the deterministic 6-block CREATE
// prompt for the Content Recommender (P1 single-agent ReAct). Unlike
// Familiar there is NO per-instance config — the only axis is
// learner_persona (curious-explorer / cert-focused / social-leader)
// per ADR-116 Amendment 2.

func TestCompose_emitsSixCreateBlocksInOrder(t *testing.T) {
	for _, persona := range []string{"curious-explorer", "cert-focused", "social-leader"} {
		got := ComposeRecommenderInstruction(persona, TaskContext{
			TenantID:    "01957c8c-0000-7000-8888-000088880000",
			LearnerGCID: "01957c8c-0000-7000-9999-00009999000f",
			TopicHint:   "agile-estimation",
		})
		blocks := []string{
			"[CONTEXT]",
			"[ROLE]",
			"[EXAMPLES]",
			"[AUDIENCE]",
			"[TASK]",
			"[EXPECTED OUTPUT]",
		}
		lastIdx := -1
		for _, b := range blocks {
			i := strings.Index(got, b)
			if i < 0 {
				t.Errorf("persona %s: CREATE block %q missing", persona, b)
				continue
			}
			if i <= lastIdx {
				t.Errorf("persona %s: %q at %d after %d (out of order)", persona, b, i, lastIdx)
			}
			lastIdx = i
		}
	}
}

func TestCompose_isDeterministic(t *testing.T) {
	ctx := TaskContext{TenantID: "t", LearnerGCID: "g", TopicHint: "kotlin"}
	for _, p := range []string{"curious-explorer", "cert-focused", "social-leader"} {
		a := ComposeRecommenderInstruction(p, ctx)
		b := ComposeRecommenderInstruction(p, ctx)
		if a != b {
			t.Errorf("persona %s: ComposeRecommenderInstruction not deterministic", p)
		}
	}
}

func TestCompose_distinguishesPersonas(t *testing.T) {
	ctx := TaskContext{TenantID: "t", LearnerGCID: "g", TopicHint: "graphs"}
	curious := ComposeRecommenderInstruction("curious-explorer", ctx)
	cert := ComposeRecommenderInstruction("cert-focused", ctx)
	social := ComposeRecommenderInstruction("social-leader", ctx)
	if curious == cert || cert == social || curious == social {
		t.Error("three persona values must produce three distinct prompts")
	}
}

func TestCompose_emptyPersonaDefaultsToCuriousExplorer(t *testing.T) {
	ctx := TaskContext{TenantID: "t", LearnerGCID: "g", TopicHint: "x"}
	got := ComposeRecommenderInstruction("", ctx)
	if !strings.Contains(got, "curious-explorer") {
		t.Errorf("empty persona must default to curious-explorer; prompt:\n%s", got)
	}
}

func TestCompose_examplesBlockContainsThreeFewShots(t *testing.T) {
	ctx := TaskContext{TenantID: "t", LearnerGCID: "g", TopicHint: "x"}
	got := ComposeRecommenderInstruction("curious-explorer", ctx)
	for _, label := range []string{"Example 1", "Example 2", "Example 3"} {
		if !strings.Contains(got, label) {
			t.Errorf("EXAMPLES block must enumerate %q", label)
		}
	}
}

func TestCompose_contextFieldsSurface(t *testing.T) {
	ctx := TaskContext{
		TenantID:    "tenant-xyz",
		LearnerGCID: "learner-abc",
		TopicHint:   "graph-algorithms",
	}
	got := ComposeRecommenderInstruction("cert-focused", ctx)
	for _, want := range []string{"tenant-xyz", "graph-algorithms", "cert-focused"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt must contain %q", want)
		}
	}
}

func TestFewShots_loadsThreePersonaFixtures(t *testing.T) {
	store, err := LoadFewShots()
	if err != nil {
		t.Fatalf("LoadFewShots: %v", err)
	}
	for _, p := range []string{"curious-explorer", "cert-focused", "social-leader"} {
		ex, ok := store.Lookup(p)
		if !ok {
			t.Errorf("persona %s: fixture missing", p)
			continue
		}
		if len(ex) != 3 {
			t.Errorf("persona %s: want 3 examples; got %d", p, len(ex))
		}
		for i, e := range ex {
			if strings.TrimSpace(e.UserPrompt) == "" {
				t.Errorf("persona %s example %d: UserPrompt empty", p, i)
			}
			if strings.TrimSpace(e.AssistantReply) == "" {
				t.Errorf("persona %s example %d: AssistantReply empty", p, i)
			}
		}
	}
}

func TestFewShots_unknownPersonaReturnsNotOK(t *testing.T) {
	store, _ := LoadFewShots()
	ex, ok := store.Lookup("nonexistent")
	if ok && len(ex) == 0 {
		t.Error("unknown persona must return (nil/empty, false)")
	}
}

func TestCompose_unknownPersonaFallsBackToCuriousFixture(t *testing.T) {
	ctx := TaskContext{TenantID: "t", LearnerGCID: "g", TopicHint: "x"}
	got := ComposeRecommenderInstruction("alchemist", ctx)
	// Unknown persona defaults to curious-explorer (the [AUDIENCE] block
	// + the few-shot fixture set both fall back).
	if !strings.Contains(got, "curious-explorer") {
		t.Errorf("unknown persona must default to curious-explorer; prompt:\n%s", got)
	}
	for _, label := range []string{"Example 1", "Example 2", "Example 3"} {
		if !strings.Contains(got, label) {
			t.Errorf("fallback path lost example %q", label)
		}
	}
}

func TestLoadFewShotsFromFS_errorPaths(t *testing.T) {
	// Empty dir → no-fixtures error.
	empty := fstest.MapFS{"few_shots/": &fstest.MapFile{Mode: 0o755 | 1<<31}}
	if _, err := loadFewShotsFromFS(empty); err == nil {
		t.Error("empty fixtures dir must error")
	}

	// Missing learner_persona key.
	noPersona := fstest.MapFS{
		"few_shots/x.yaml": &fstest.MapFile{Data: []byte("examples: []\n")},
	}
	if _, err := loadFewShotsFromFS(noPersona); err == nil {
		t.Error("missing learner_persona must error")
	}

	// Wrong example count (≠3).
	wrongCount := fstest.MapFS{
		"few_shots/x.yaml": &fstest.MapFile{Data: []byte("learner_persona: foo\nexamples:\n  - user_prompt: a\n    assistant_reply: b\n")},
	}
	if _, err := loadFewShotsFromFS(wrongCount); err == nil {
		t.Error("non-3 example count must error")
	}

	// Duplicate persona.
	dup := fstest.MapFS{
		"few_shots/a.yaml": &fstest.MapFile{Data: []byte("learner_persona: foo\nexamples:\n  - {user_prompt: a, assistant_reply: b}\n  - {user_prompt: a, assistant_reply: b}\n  - {user_prompt: a, assistant_reply: b}\n")},
		"few_shots/b.yaml": &fstest.MapFile{Data: []byte("learner_persona: foo\nexamples:\n  - {user_prompt: c, assistant_reply: d}\n  - {user_prompt: c, assistant_reply: d}\n  - {user_prompt: c, assistant_reply: d}\n")},
	}
	if _, err := loadFewShotsFromFS(dup); err == nil {
		t.Error("duplicate persona must error")
	}

	// Bad YAML.
	badYAML := fstest.MapFS{
		"few_shots/x.yaml": &fstest.MapFile{Data: []byte("learner_persona: [unclosed\n")},
	}
	if _, err := loadFewShotsFromFS(badYAML); err == nil {
		t.Error("malformed YAML must error")
	}
}

func TestCanonicalisePersonaCases(t *testing.T) {
	cases := []struct{ in, want string }{
		{"curious-explorer", "curious-explorer"},
		{"cert-focused", "cert-focused"},
		{"social-leader", "social-leader"},
		{"", "curious-explorer"},
		{"  curious-explorer  ", "curious-explorer"},
		{"CURIOUS-EXPLORER", "curious-explorer"},
		{"unknown-value", "curious-explorer"},
	}
	for _, tc := range cases {
		got := canonicalisePersona(tc.in)
		if got != tc.want {
			t.Errorf("canonicalisePersona(%q) = %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestMandatorySpanAttributes_coversLearnerInstancing(t *testing.T) {
	required := []string{
		"chora.tenant_id",
		"chora.learner_gcid",
		"chora.mana_tier",
		"chora.crew_kind",
		"gen_ai.request.model",
		"gen_ai.usage.output_tokens",
	}
	got := MandatorySpanAttributes()
	seen := make(map[string]struct{}, len(got))
	for _, k := range got {
		seen[k] = struct{}{}
	}
	for _, r := range required {
		if _, ok := seen[r]; !ok {
			t.Errorf("MandatorySpanAttributes missing %q", r)
		}
	}
}
