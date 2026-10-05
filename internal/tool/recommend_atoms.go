// Package tool holds the stateless tool functions called by the Recommender
// ADK agent. Each is a pure function calling an upstream Chora service.
//
// CHO-1662 — REAL RAG: the recommender's content source is the
// chora-consumption Consumption.RecommendAtomsForLearner gRPC RPC, which reads
// the local `atom_index` projection (hydrated from chora.creation.atom.created.v1).
// The crew runs in the ai-kernel namespace and CANNOT read chora_consumption
// directly (cross-DB FORBIDDEN), so it dials chora-consumption:9090 for REAL
// published atoms. Stub-mode (CONSUMPTION_GRPC_ENDPOINT=stub://…) still returns
// deterministic data so end-to-end smoke runs without a live backend.
//
// Hexagonal split:
//   - Atom / SearchParams / Searcher (port) live here in the domain-ish tool pkg.
//   - The production gRPC adapter lives in internal/adapter (consumptionrag).
//   - RecommendWithSearcher is the pure tool logic (testable with a fake
//     Searcher + a fake session-state reader). It reads tenant_id + learner gcid
//     from the ADK session state (RLS-bearing; NEVER from the model args).
package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

// Atom is the projection returned to the agent for ranking.
type Atom struct {
	AtomID    string  `json:"atom_id"`
	Title     string  `json:"title"`
	Snippet   string  `json:"snippet"`
	RankScore float64 `json:"rank_score"`
}

// RecommendAtomsRequest is the agent-provided input.
//
// Only learner_gcid is REQUIRED. persona / topic_hint / limit are OPTIONAL
// (omitempty → not in the generated tool schema's `required` list): the
// daily-dose /ai path frequently has no topic to recommend around, and the
// required-topic_hint schema is exactly what stranded the recommender — the
// model declared "Missing topic_hint to retrieve relevant atoms" and returned
// no picks. With them optional the model can call the tool with just the GCID;
// the handler defaults topic_hint→"general", persona→"default", limit→5.
//
// NOTE: tenant_id is DELIBERATELY NOT a request field — it is RLS-bearing and
// MUST come from the trusted session state (the tenant-propagation plugin), not
// from anything the model can influence.
type RecommendAtomsRequest struct {
	LearnerGCID string `json:"learner_gcid"`
	Persona     string `json:"persona,omitempty"`
	TopicHint   string `json:"topic_hint,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

// RecommendAtomsResponse is the tool's output.
type RecommendAtomsResponse struct {
	Atoms []Atom `json:"atoms"`
}

// SearchParams is the resolved, trusted input the Searcher port receives.
// TenantID + LearnerGCID come from the session state, not the model.
type SearchParams struct {
	TenantID    string
	LearnerGCID string
	TopicHint   string
	Limit       int
}

// Searcher is the port the tool depends on to retrieve real atoms. The
// production adapter (internal/adapter/consumptionrag) wraps the
// chora-consumption Consumption.RecommendAtomsForLearner gRPC client; tests
// inject a fake.
type Searcher interface {
	Search(ctx context.Context, p SearchParams) ([]Atom, error)
}

// StateReader is the minimal readonly session-state seam (matches the ADK
// ReadonlyState contract: Get(key) → (value, error)). The tool reads the
// RLS-bearing tenant_id + learner gcid the consumption Recommend client wrote
// at async_create_session. Mirrors agent.StateReader / composer.go.
type StateReader interface {
	Get(key string) (any, error)
}

const (
	defaultLimit = 5
	maxLimit     = 10
)

// RecommendWithSearcher is the pure tool logic: resolve tenant_id + learner
// gcid from session state, clamp the limit, and return recommendations.
//
// CHO-1662 session-state-injection RAG (PRIMARY path): chora-consumption owns
// atom_index and already initiates this call, so it pre-fetches the candidate
// atoms in-process and injects them into the session state as a JSON string
// (state["atom_candidates"]). When present, the tool returns them directly — no
// searcher dial, no mesh hop. The sidecar-less crew CANNOT dial back to
// consumption's STRICT-mTLS :9090, which is exactly what this sidesteps (mirrors
// the familiar bug-3 mesh-sidestep).
//
// FALLBACK path: when no candidates are injected (sandbox smoke / external
// callers), it falls back to the Searcher port. Refuses when the session state
// carries no tenant_id (RLS-bearing identity) regardless of path. NEVER
// fabricates atoms — an empty result yields an empty list.
func RecommendWithSearcher(ctx context.Context, state StateReader, searcher Searcher, req RecommendAtomsRequest) (RecommendAtomsResponse, error) {
	tracer := otel.Tracer("recommender_adk_go/tool/recommend_atoms")
	ctx, span := tracer.Start(ctx, "tool.recommend_atoms_for_learner")
	defer span.End()

	tenantID := stateString(state, "tenant_id")
	if tenantID == "" {
		return RecommendAtomsResponse{}, errors.New(
			"recommend: tenant_id absent from session state — refusing (RLS needs tenant; " +
				"the consumption Recommend client must stamp tenant_id at async_create_session)")
	}
	gcid := stateString(state, "user_gcid")
	if gcid == "" {
		gcid = stateString(state, "learner_gcid")
	}
	if gcid == "" {
		gcid = strings.TrimSpace(req.LearnerGCID) // last-resort model fallback
	}

	limit := req.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	span.SetAttributes(
		attribute.String("openinference.span.kind", "TOOL"),
		attribute.String("tool.name", "recommend_atoms_for_learner"),
		attribute.String("chora.tenant_id", tenantID),
		attribute.String("chora.learner_gcid", gcid),
		attribute.String("tool.input.topic_hint", req.TopicHint),
		attribute.Int("tool.input.limit", limit),
	)

	// PRIMARY path — pre-injected atom candidates from the trusted session state
	// (consumption pre-fetched them from atom_index). A non-empty, well-formed
	// list short-circuits the searcher entirely (no mesh dial).
	if atoms, ok := candidatesFromState(state, limit); ok {
		span.SetAttributes(
			attribute.String("chora.recommend.source", "session_state_candidates"),
			attribute.Int("tool.output.atom_count", len(atoms)),
		)
		return RecommendAtomsResponse{Atoms: atoms}, nil
	}

	// FALLBACK path — no injected candidates; dial the Searcher (stub in sandbox).
	if searcher == nil {
		return RecommendAtomsResponse{}, errors.New("recommend: nil searcher")
	}
	span.SetAttributes(attribute.String("chora.recommend.source", "searcher"))

	atoms, err := searcher.Search(ctx, SearchParams{
		TenantID:    tenantID,
		LearnerGCID: gcid,
		TopicHint:   strings.TrimSpace(req.TopicHint),
		Limit:       limit,
	})
	if err != nil {
		span.RecordError(err)
		return RecommendAtomsResponse{}, fmt.Errorf("recommend: search: %w", err)
	}
	span.SetAttributes(attribute.Int("tool.output.atom_count", len(atoms)))
	return RecommendAtomsResponse{Atoms: atoms}, nil
}

// RecommendAtomsForLearner is the legacy entry point that resolves a Searcher
// from CONSUMPTION_GRPC_ENDPOINT and delegates to RecommendWithSearcher. It is
// retained for the stub smoke path + callers that pass a plain context.Context
// (no session state) — in that case tenant/gcid come from the request. The ADK
// handler (cmd/recommender) calls RecommendWithSearcher directly with the tool
// session state + the boot-injected real Searcher.
func RecommendAtomsForLearner(ctx context.Context, req RecommendAtomsRequest) (RecommendAtomsResponse, error) {
	endpoint := os.Getenv("CONSUMPTION_GRPC_ENDPOINT")
	if endpoint == "" {
		return RecommendAtomsResponse{}, errors.New(
			"CONSUMPTION_GRPC_ENDPOINT not set; refusing inline default " +
				"per feedback_no_inline_config")
	}

	limit := req.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	if strings.HasPrefix(endpoint, "stub://") {
		atoms := make([]Atom, 0, limit)
		for i := 0; i < limit; i++ {
			atoms = append(atoms, Atom{
				AtomID: fmt.Sprintf("atom-stub-%03d", i),
				Title:  fmt.Sprintf("%s — stub atom %d", safe(req.TopicHint, "general"), i+1),
				Snippet: fmt.Sprintf("[persona=%s] For learner %s, atom %d on %s (stub).",
					safe(req.Persona, "default"), safePrefix(req.LearnerGCID, 8), i+1, safe(req.TopicHint, "general")),
				RankScore: 0.95 - 0.07*float64(i),
			})
		}
		return RecommendAtomsResponse{Atoms: atoms}, nil
	}

	// Non-stub endpoint but no Searcher wired through this legacy path: the
	// real path runs through RecommendWithSearcher from the ADK handler with
	// the boot-injected gRPC adapter. Fail loud rather than silently stubbing.
	return RecommendAtomsResponse{}, errors.New(
		"recommend: non-stub CONSUMPTION_GRPC_ENDPOINT requires the boot-injected " +
			"gRPC Searcher — call RecommendWithSearcher from the ADK handler")
}

// StubSearcher is the deterministic in-process Searcher used for sandbox smoke
// (CONSUMPTION_GRPC_ENDPOINT=stub://…). It biases the snippet by topic so the
// e2e pipe is exercised without a live chora-consumption. NEVER use in prod.
type StubSearcher struct{}

// Search returns `limit` deterministic atom-stub-* projections.
func (StubSearcher) Search(_ context.Context, p SearchParams) ([]Atom, error) {
	n := p.Limit
	if n <= 0 {
		n = defaultLimit
	}
	atoms := make([]Atom, 0, n)
	for i := 0; i < n; i++ {
		atoms = append(atoms, Atom{
			AtomID:    fmt.Sprintf("atom-stub-%03d", i),
			Title:     fmt.Sprintf("%s — stub atom %d", safe(p.TopicHint, "general"), i+1),
			Snippet:   safe(p.TopicHint, "general"),
			RankScore: 0.95 - 0.07*float64(i),
		})
	}
	return atoms, nil
}

// candidatesFromState reads the pre-injected atom candidates the consumption
// Recommend client wrote into session.State["atom_candidates"] as a JSON string
// (CHO-1662). Returns (atoms, true) only when the JSON is well-formed AND yields
// at least one atom — clamped to limit. An absent key, empty array, or malformed
// JSON returns (nil, false) so the caller falls back to the searcher (NEVER
// fabricates picks). The shape is the tool's own Atom (atom_id/title/snippet);
// RankScore is left zero (consumption already ranked the candidates by order).
func candidatesFromState(state StateReader, limit int) ([]Atom, bool) {
	raw := stateString(state, "atom_candidates")
	if strings.TrimSpace(raw) == "" {
		return nil, false
	}
	var atoms []Atom
	if err := json.Unmarshal([]byte(raw), &atoms); err != nil {
		return nil, false
	}
	if len(atoms) == 0 {
		return nil, false
	}
	if limit > 0 && len(atoms) > limit {
		atoms = atoms[:limit]
	}
	return atoms, true
}

// stateString reads a string value from session state; absent / wrong-typed
// keys return "".
func stateString(state StateReader, key string) string {
	if state == nil {
		return ""
	}
	raw, err := state.Get(key)
	if err != nil {
		return ""
	}
	s, _ := raw.(string)
	return s
}

func safe(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func safePrefix(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}
