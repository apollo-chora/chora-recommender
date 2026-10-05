// recommend_atoms_state_candidates_test.go — CHO-1662 session-state-injection.
//
// The recommender crew is sidecar-less and consumption :9090 is STRICT-mTLS, so
// the tool CANNOT dial consumption for candidates. Instead chora-consumption
// pre-fetches the atom_index candidates in-process and injects them into the ADK
// session state as state["atom_candidates"] (a JSON string). The tool reads them
// and returns them directly — no searcher dial. When absent it falls back to the
// searcher (stub). tenant_id-absent is still refused (RLS-bearing).
package tool

import (
	"context"
	"encoding/json"
	"testing"
)

func candidatesJSON(t *testing.T, atoms []Atom) string {
	t.Helper()
	b, err := json.Marshal(atoms)
	if err != nil {
		t.Fatalf("marshal candidates: %v", err)
	}
	return string(b)
}

func TestRecommend_StateCandidates_ReturnedWithoutSearcherDial(t *testing.T) {
	// Searcher would error if dialed — proves the candidates path bypasses it.
	fake := &fakeSearcher{err: errMustNotDial()}
	state := stubState{
		"tenant_id": "tenant-xyz",
		"user_gcid": "gcid-abc",
		"atom_candidates": candidatesJSON(t, []Atom{
			{AtomID: "real-1", Title: "SOLID: the S Principle", Snippet: "solid"},
			{AtomID: "real-2", Title: "Big-O of Binary Search", Snippet: "algorithms"},
			{AtomID: "real-3", Title: "Version Control Basics", Snippet: "git"},
		}),
	}

	resp, err := RecommendWithSearcher(context.Background(), state, fake, RecommendAtomsRequest{
		LearnerGCID: "gcid-abc", Limit: 5,
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(resp.Atoms) != 3 {
		t.Fatalf("want 3 candidates from state; got %d", len(resp.Atoms))
	}
	if resp.Atoms[0].AtomID != "real-1" || resp.Atoms[2].AtomID != "real-3" {
		t.Errorf("atoms = %+v; want the injected state candidates in order", resp.Atoms)
	}
	if fake.gotTenant != "" {
		t.Errorf("searcher WAS dialed (gotTenant=%q); the state candidates path must bypass it", fake.gotTenant)
	}
}

func TestRecommend_StateCandidates_ClampedToLimit(t *testing.T) {
	fake := &fakeSearcher{err: errMustNotDial()}
	state := stubState{
		"tenant_id": "t1",
		"user_gcid": "g1",
		"atom_candidates": candidatesJSON(t, []Atom{
			{AtomID: "a"}, {AtomID: "b"}, {AtomID: "c"}, {AtomID: "d"},
		}),
	}
	resp, err := RecommendWithSearcher(context.Background(), state, fake, RecommendAtomsRequest{Limit: 2})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(resp.Atoms) != 2 {
		t.Errorf("want clamp to limit 2; got %d", len(resp.Atoms))
	}
	if resp.Atoms[0].AtomID != "a" || resp.Atoms[1].AtomID != "b" {
		t.Errorf("atoms = %+v; want first 2 candidates", resp.Atoms)
	}
}

func TestRecommend_StateCandidates_FallsBackToSearcherWhenAbsent(t *testing.T) {
	fake := &fakeSearcher{ret: []Atom{{AtomID: "from-searcher"}}}
	state := stubState{"tenant_id": "t1", "user_gcid": "g1"} // no atom_candidates

	resp, err := RecommendWithSearcher(context.Background(), state, fake, RecommendAtomsRequest{Limit: 1})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(resp.Atoms) != 1 || resp.Atoms[0].AtomID != "from-searcher" {
		t.Errorf("atoms = %+v; want searcher fallback when state candidates absent", resp.Atoms)
	}
	if fake.gotTenant != "t1" {
		t.Errorf("searcher not dialed on fallback; gotTenant=%q", fake.gotTenant)
	}
}

func TestRecommend_StateCandidates_EmptyJSONFallsBackToSearcher(t *testing.T) {
	fake := &fakeSearcher{ret: []Atom{{AtomID: "from-searcher"}}}
	// Empty array → no real candidates → fall back to searcher rather than
	// returning zero picks.
	state := stubState{"tenant_id": "t1", "user_gcid": "g1", "atom_candidates": "[]"}

	resp, err := RecommendWithSearcher(context.Background(), state, fake, RecommendAtomsRequest{Limit: 1})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(resp.Atoms) != 1 || resp.Atoms[0].AtomID != "from-searcher" {
		t.Errorf("atoms = %+v; want searcher fallback on empty candidates JSON", resp.Atoms)
	}
}

func TestRecommend_StateCandidates_MalformedJSONFallsBackToSearcher(t *testing.T) {
	fake := &fakeSearcher{ret: []Atom{{AtomID: "from-searcher"}}}
	state := stubState{"tenant_id": "t1", "user_gcid": "g1", "atom_candidates": "{not-json"}

	resp, err := RecommendWithSearcher(context.Background(), state, fake, RecommendAtomsRequest{Limit: 1})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(resp.Atoms) != 1 || resp.Atoms[0].AtomID != "from-searcher" {
		t.Errorf("atoms = %+v; want searcher fallback on malformed candidates JSON", resp.Atoms)
	}
}

func TestRecommend_StateCandidates_MissingTenantStillRefused(t *testing.T) {
	fake := &fakeSearcher{err: errMustNotDial()}
	state := stubState{
		"user_gcid":       "g1", // no tenant_id
		"atom_candidates": candidatesJSON(t, []Atom{{AtomID: "a"}}),
	}
	_, err := RecommendWithSearcher(context.Background(), state, fake, RecommendAtomsRequest{})
	if err == nil {
		t.Fatal("missing tenant_id must refuse even when candidates present (RLS-bearing identity)")
	}
}

// errMustNotDial is a sentinel the fake returns so a dialed searcher fails the
// test loudly (the candidates path must NOT call it).
func errMustNotDial() error { return errDial }

var errDial = errString("searcher must NOT be dialed when state candidates present")

type errString string

func (e errString) Error() string { return string(e) }
