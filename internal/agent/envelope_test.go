package agent

import (
	"encoding/json"
	"testing"
)

// The kennel's dose lane parses this envelope out of the dispatch
// output_payload and projects it onto DoseRecommendationCompleted. The two
// sides are pinned to each other by these field names, so a rename here is a
// silent break there: the lane reads a missing key as an empty recommendation
// and publishes FAILED while the agent believes it answered.

func TestEnvelopeRendersTheKeysTheKennelLaneReads(t *testing.T) {
	env := Envelope{
		Atoms: []RecommendedAtom{
			{AtomID: "a1", Title: "Halves", Snippet: "...", RankScore: 0.4},
			{AtomID: "a2", Title: "Thirds", RankScore: 0.9},
		},
		Rationale:     "These follow on from yesterday.",
		ModelID:       "gemini-3.1-pro-preview",
		PromptVersion: "v1",
		PromptSource:  PromptSourceConfig,
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(env.Render()), &got); err != nil {
		t.Fatalf("render is not valid JSON: %v", err)
	}
	for _, key := range []string{"atoms", "rationale", "model_id", "prompt_version", "prompt_source"} {
		if _, ok := got[key]; !ok {
			t.Errorf("envelope is missing %q, which the kennel lane reads", key)
		}
	}
	if _, ok := got["status"]; ok {
		t.Error("envelope must NOT declare status: the kennel lane owns it, and a " +
			"second source of truth for one fact is how the two disagree")
	}
}

func TestArrayOrderIsPreservedBecauseItIsTheRecommendationOrder(t *testing.T) {
	// rank_score DESCENDS against array order on purpose. The kennel carries
	// array order and never re-sorts; a renderer that sorted by rank_score
	// would reorder the dose while looking like an improvement. rank_score is
	// legitimately ZERO for every atom on the candidate path.
	env := Envelope{Atoms: []RecommendedAtom{
		{AtomID: "first", RankScore: 0.1},
		{AtomID: "second", RankScore: 0.9},
	}}
	var got struct {
		Atoms []RecommendedAtom `json:"atoms"`
	}
	if err := json.Unmarshal([]byte(env.Render()), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Atoms) != 2 || got.Atoms[0].AtomID != "first" || got.Atoms[1].AtomID != "second" {
		t.Fatalf("array order not preserved: %+v", got.Atoms)
	}
}

func TestNilAtomsRenderAsAnEmptyArrayNeverNull(t *testing.T) {
	// The kennel iterates atoms; a JSON null would be indistinguishable from a
	// contract violation on a lane where an honest empty answer is legitimate.
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(Envelope{Rationale: "Nothing new today."}.Render()), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(got["atoms"]) != "[]" {
		t.Errorf("atoms rendered as %s, want []", got["atoms"])
	}
}

func TestFromToolAtomsMapsOneToOneAndKeepsOrder(t *testing.T) {
	// The tool ALREADY computes exactly this shape, which is why emitting rich
	// costs nothing. If the tool's Atom drifts, this fails rather than silently
	// dropping a field the kennel expects.
	env := EnvelopeFromToolAtoms(
		[]ToolAtom{{AtomID: "x", Title: "T", Snippet: "S", RankScore: 0.5}},
		"why", "model-1", "v2", PromptSourceConfig)
	if len(env.Atoms) != 1 {
		t.Fatalf("want 1 atom, got %d", len(env.Atoms))
	}
	a := env.Atoms[0]
	if a.AtomID != "x" || a.Title != "T" || a.Snippet != "S" || a.RankScore != 0.5 {
		t.Errorf("field dropped in the 1:1 map: %+v", a)
	}
	if env.Rationale != "why" || env.ModelID != "model-1" || env.PromptVersion != "v2" {
		t.Errorf("attribution dropped: %+v", env)
	}
}

func TestBlankAtomIdsAreDroppedRatherThanCarriedAsEmptyPicks(t *testing.T) {
	env := EnvelopeFromToolAtoms([]ToolAtom{
		{AtomID: "  "}, {AtomID: "keep"}, {AtomID: ""},
	}, "", "m", "v", PromptSourceConfig)
	if len(env.Atoms) != 1 || env.Atoms[0].AtomID != "keep" {
		t.Fatalf("blank ids not dropped: %+v", env.Atoms)
	}
}
