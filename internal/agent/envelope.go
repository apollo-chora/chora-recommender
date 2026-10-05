package agent

import (
	"encoding/json"
	"strings"
)

// The `recommend` completion envelope (ADR-254 D4, gate G-b).
//
// This is the agent-side half of the dose lane contract and the analogue of
// companion_chat's capp.Envelope. It is what the kennel's dose lane parses out
// of the dispatch output_payload; the lane then PROJECTS it onto
// DoseRecommendationCompleted, whose caller-facing fields are only
// recommended_atom_ids[] and rationale.
//
// EMIT RICH HERE, PROJECT THIN AT THE KENNEL. The per-atom shape below is
// exactly what internal/tool.Atom already computes, so carrying it costs
// nothing and it makes the ORDER of recommended_atom_ids DERIVED rather than
// asserted. Widening the caller-facing proto to carry title/score is a separate,
// one-sided change later; keeping the agent thin would have made it two-sided.
//
// ⚠ THERE IS DELIBERATELY NO `status` FIELD. The kennel lane owns status,
// error_code and workflow_id, exactly as it does for companion_turn. An
// agent-declared status would be a second source of truth for one fact, and the
// interesting case is always the one where the two disagree.

// RecommendedAtom is one pick.
//
// ⚠ ARRAY ORDER IS THE RECOMMENDATION ORDER AND IT IS CANONICAL. RankScore is
// DISCLOSURE ONLY: no consumer re-sorts on it. On the candidate path it is
// legitimately ZERO for every atom, because consumption already ranked the
// candidates by order and candidatesFromState leaves the score unset. A
// consumer that sorted by RankScore would therefore flatten a correctly ordered
// dose into an arbitrary one, while looking like an improvement.
type RecommendedAtom struct {
	AtomID    string  `json:"atom_id"`
	Title     string  `json:"title,omitempty"`
	Snippet   string  `json:"snippet,omitempty"`
	RankScore float64 `json:"rank_score"`
}

// Envelope is the completion output_payload for every `recommend` dispatch.
type Envelope struct {
	Atoms         []RecommendedAtom `json:"atoms"`
	Rationale     string            `json:"rationale"`
	RefusalReason string            `json:"refusal_reason,omitempty"`
	ModelID       string            `json:"model_id"`
	PromptVersion string            `json:"prompt_version"`
	PromptSource  string            `json:"prompt_source"`
}

// Prompt sources for the recommender. Unlike companion_chat there is no
// per-instance registry override, so the prompt is the agentconfig-declared one.
const (
	PromptSourceConfig = "agentconfig"
)

// ToolAtom is the shape internal/tool.Atom already returns. Declared here
// rather than imported so this package stays free of a tool dependency; the
// 1:1 map is pinned by a test that fails if either side drifts.
type ToolAtom struct {
	AtomID    string  `json:"atom_id"`
	Title     string  `json:"title"`
	Snippet   string  `json:"snippet"`
	RankScore float64 `json:"rank_score"`
}

// EnvelopeFromToolAtoms builds the completion envelope from the tool's picks,
// PRESERVING ORDER. Blank ids are dropped: an empty pick cannot be resolved by
// consumption and would occupy a dose slot that could have carried a real atom.
func EnvelopeFromToolAtoms(atoms []ToolAtom, rationale, modelID, promptVersion,
	promptSource string) Envelope {
	out := make([]RecommendedAtom, 0, len(atoms))
	for _, a := range atoms {
		id := strings.TrimSpace(a.AtomID)
		if id == "" {
			continue
		}
		out = append(out, RecommendedAtom{
			AtomID: id, Title: a.Title, Snippet: a.Snippet, RankScore: a.RankScore,
		})
	}
	return Envelope{
		Atoms:         out,
		Rationale:     rationale,
		ModelID:       modelID,
		PromptVersion: promptVersion,
		PromptSource:  promptSource,
	}
}

// Render marshals the envelope. A nil slice renders as [] so the kennel never
// sees null where it iterates a list: on this lane an honest empty answer is
// legitimate, so null and empty must not be confusable.
func (e Envelope) Render() string {
	if e.Atoms == nil {
		e.Atoms = []RecommendedAtom{}
	}
	b, _ := json.Marshal(e)
	return string(b)
}
