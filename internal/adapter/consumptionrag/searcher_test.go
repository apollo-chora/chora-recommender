// searcher_test.go — TDD for the production gRPC Searcher adapter that wraps
// chora-consumption's Consumption.RecommendAtomsForLearner RPC (CHO-1662).
package consumptionrag

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	consumptionv1 "github.com/apollo-chora/chora-contracts/gen/go/chora/services/consumption/v1"

	"github.com/apollo-chora/chora-recommender/internal/tool"
)

// fakeClient is the narrowed RecommendClient seam the adapter depends on.
type fakeClient struct {
	gotReq *consumptionv1.RecommendAtomsForLearnerRequest
	resp   *consumptionv1.RecommendAtomsForLearnerResponse
	err    error
}

func (f *fakeClient) RecommendAtomsForLearner(
	_ context.Context,
	in *consumptionv1.RecommendAtomsForLearnerRequest,
	_ ...grpc.CallOption,
) (*consumptionv1.RecommendAtomsForLearnerResponse, error) {
	f.gotReq = in
	return f.resp, f.err
}

func TestSearcher_MapsProtoToAtomsAndForwardsParams(t *testing.T) {
	pub := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	fc := &fakeClient{resp: &consumptionv1.RecommendAtomsForLearnerResponse{
		Recommendations: []*consumptionv1.AtomRecommendation{
			{AtomId: "a1", Title: "Atom One", Topic: "agile", Difficulty: 3, PublishedAt: timestamppb.New(pub)},
			{AtomId: "a2", Title: "Atom Two", Topic: "scrum", Difficulty: 1, PublishedAt: timestamppb.New(pub)},
		},
	}}
	s := NewSearcher(fc)

	atoms, err := s.Search(context.Background(), tool.SearchParams{
		TenantID:    "t1",
		LearnerGCID: "g1",
		TopicHint:   "agile",
		Limit:       2,
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(atoms) != 2 {
		t.Fatalf("want 2 atoms; got %d", len(atoms))
	}
	if atoms[0].AtomID != "a1" || atoms[0].Title != "Atom One" {
		t.Errorf("atom[0] = %+v", atoms[0])
	}
	// Snippet carries the topic (UI hook); rank score descends with order.
	if atoms[0].Snippet != "agile" {
		t.Errorf("atom[0].Snippet = %q; want topic 'agile'", atoms[0].Snippet)
	}
	if atoms[0].RankScore <= atoms[1].RankScore {
		t.Errorf("rank must descend: %v vs %v", atoms[0].RankScore, atoms[1].RankScore)
	}

	// Params forwarded verbatim (tenant scoping + topic bias).
	if fc.gotReq.GetTenantId() != "t1" {
		t.Errorf("tenant = %q", fc.gotReq.GetTenantId())
	}
	if fc.gotReq.GetLearnerGcid() != "g1" {
		t.Errorf("gcid = %q", fc.gotReq.GetLearnerGcid())
	}
	if fc.gotReq.GetTopicHint() != "agile" {
		t.Errorf("topic = %q", fc.gotReq.GetTopicHint())
	}
	if fc.gotReq.GetLimit() != 2 {
		t.Errorf("limit = %d", fc.gotReq.GetLimit())
	}
}

func TestSearcher_PropagatesError(t *testing.T) {
	s := NewSearcher(&fakeClient{err: errors.New("rpc down")})
	_, err := s.Search(context.Background(), tool.SearchParams{TenantID: "t1", LearnerGCID: "g1"})
	if err == nil {
		t.Fatal("rpc error must propagate")
	}
}

func TestSearcher_EmptyResultIsEmptySlice(t *testing.T) {
	s := NewSearcher(&fakeClient{resp: &consumptionv1.RecommendAtomsForLearnerResponse{}})
	atoms, err := s.Search(context.Background(), tool.SearchParams{TenantID: "t1", LearnerGCID: "g1"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(atoms) != 0 {
		t.Errorf("want empty; got %d", len(atoms))
	}
}

// Compile-time guarantee: *Searcher satisfies the tool.Searcher port.
var _ tool.Searcher = (*Searcher)(nil)
