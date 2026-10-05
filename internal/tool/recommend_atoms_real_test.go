// recommend_atoms_real_test.go — TDD for the REAL-RAG path of the recommender
// tool (CHO-1662): the tool resolves its content source from CONSUMPTION_GRPC_
// ENDPOINT, reads tenant_id from the ADK session state (NOT the model args),
// and maps real chora-consumption RecommendAtomsForLearner results into
// recommendations — replacing the atom-stub-* source.
package tool

import (
	"context"
	"errors"
	"testing"
)

// fakeSearcher is an in-test AtomSearcher capturing the resolved params.
type fakeSearcher struct {
	gotTenant string
	gotGCID   string
	gotTopic  string
	gotLimit  int
	ret       []Atom
	err       error
}

func (f *fakeSearcher) Search(_ context.Context, p SearchParams) ([]Atom, error) {
	f.gotTenant = p.TenantID
	f.gotGCID = p.LearnerGCID
	f.gotTopic = p.TopicHint
	f.gotLimit = p.Limit
	return f.ret, f.err
}

// stubState implements the StateReader seam used to read session state.
type stubState map[string]any

func (s stubState) Get(key string) (any, error) {
	v, ok := s[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return v, nil
}

func TestRecommend_RealSearcher_MapsResultsAndReadsTenantFromState(t *testing.T) {
	fake := &fakeSearcher{ret: []Atom{
		{AtomID: "real-1", Title: "Real Atom One", Snippet: "agile", RankScore: 0.9},
		{AtomID: "real-2", Title: "Real Atom Two", Snippet: "scrum", RankScore: 0.8},
	}}
	state := stubState{"tenant_id": "tenant-xyz", "user_gcid": "gcid-abc"}

	resp, err := RecommendWithSearcher(context.Background(), state, fake, RecommendAtomsRequest{
		LearnerGCID: "ignored-model-gcid", // model-supplied; state wins for tenant
		TopicHint:   "agile",
		Limit:       2,
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(resp.Atoms) != 2 {
		t.Fatalf("want 2 atoms; got %d", len(resp.Atoms))
	}
	if resp.Atoms[0].AtomID != "real-1" {
		t.Errorf("atom[0] = %q; want real-1 (no stub)", resp.Atoms[0].AtomID)
	}
	// Tenant MUST come from session state (RLS-bearing), not the model.
	if fake.gotTenant != "tenant-xyz" {
		t.Errorf("tenant = %q; want tenant-xyz (from session state)", fake.gotTenant)
	}
	if fake.gotGCID != "gcid-abc" {
		t.Errorf("gcid = %q; want gcid-abc (from session state)", fake.gotGCID)
	}
	if fake.gotTopic != "agile" {
		t.Errorf("topic = %q; want agile", fake.gotTopic)
	}
	if fake.gotLimit != 2 {
		t.Errorf("limit = %d; want 2", fake.gotLimit)
	}
}

func TestRecommend_RealSearcher_LearnerGCIDAliasFallback(t *testing.T) {
	fake := &fakeSearcher{ret: []Atom{{AtomID: "a"}}}
	// canonical user_gcid absent; learner_gcid alias present.
	state := stubState{"tenant_id": "t1", "learner_gcid": "alias-gcid"}
	_, err := RecommendWithSearcher(context.Background(), state, fake, RecommendAtomsRequest{Limit: 1})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if fake.gotGCID != "alias-gcid" {
		t.Errorf("gcid = %q; want alias-gcid (learner_gcid alias)", fake.gotGCID)
	}
}

func TestRecommend_RealSearcher_MissingTenantRefuses(t *testing.T) {
	fake := &fakeSearcher{ret: []Atom{{AtomID: "a"}}}
	state := stubState{"user_gcid": "g1"} // no tenant_id
	_, err := RecommendWithSearcher(context.Background(), state, fake, RecommendAtomsRequest{})
	if err == nil {
		t.Fatal("missing tenant_id in session state must refuse (RLS needs tenant)")
	}
}

func TestRecommend_RealSearcher_PropagatesSearchError(t *testing.T) {
	fake := &fakeSearcher{err: errors.New("boom")}
	state := stubState{"tenant_id": "t1", "user_gcid": "g1"}
	_, err := RecommendWithSearcher(context.Background(), state, fake, RecommendAtomsRequest{})
	if err == nil {
		t.Fatal("search error must propagate")
	}
}

func TestRecommend_RealSearcher_ClampsLimit(t *testing.T) {
	fake := &fakeSearcher{ret: []Atom{{AtomID: "a"}}}
	state := stubState{"tenant_id": "t1", "user_gcid": "g1"}
	// limit 0 → default 5; over-large → clamp 10.
	_, _ = RecommendWithSearcher(context.Background(), state, fake, RecommendAtomsRequest{Limit: 0})
	if fake.gotLimit != 5 {
		t.Errorf("limit 0 → %d; want default 5", fake.gotLimit)
	}
	_, _ = RecommendWithSearcher(context.Background(), state, fake, RecommendAtomsRequest{Limit: 99})
	if fake.gotLimit != 10 {
		t.Errorf("limit 99 → %d; want clamp 10", fake.gotLimit)
	}
}

func TestRecommend_RealSearcher_NilSearcherRefuses(t *testing.T) {
	state := stubState{"tenant_id": "t1", "user_gcid": "g1"}
	_, err := RecommendWithSearcher(context.Background(), state, nil, RecommendAtomsRequest{})
	if err == nil {
		t.Fatal("nil searcher must refuse")
	}
}

func TestStubSearcher_ReturnsDeterministicTopicBiasedStubs(t *testing.T) {
	atoms, err := StubSearcher{}.Search(context.Background(), SearchParams{TopicHint: "agile", Limit: 3})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(atoms) != 3 {
		t.Fatalf("want 3; got %d", len(atoms))
	}
	if atoms[0].Snippet != "agile" {
		t.Errorf("snippet = %q; want topic 'agile'", atoms[0].Snippet)
	}
	if atoms[0].RankScore <= atoms[2].RankScore {
		t.Error("rank must descend")
	}
	// Default limit when unset.
	def, _ := StubSearcher{}.Search(context.Background(), SearchParams{})
	if len(def) != 5 {
		t.Errorf("default limit = %d; want 5", len(def))
	}
}
