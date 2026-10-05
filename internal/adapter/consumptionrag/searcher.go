// Package consumptionrag is the production gRPC adapter implementing the
// recommender tool's Searcher port (CHO-1662 REAL RAG). It wraps the
// chora-consumption Consumption.RecommendAtomsForLearner RPC, which reads the
// local `atom_index` projection (hydrated from chora.creation.atom.created.v1).
//
// The recommender crew runs in the ai-kernel namespace and CANNOT read
// chora_consumption directly (cross-DB FORBIDDEN), so this adapter dials
// chora-consumption:9090 (in-mesh) for REAL published atoms instead of returning
// atom-stub-*. tenant_id + learner gcid are RLS-bearing and come from the tool's
// session-state resolution (NOT the model) — see tool.RecommendWithSearcher.
//
// Hexagonal posture: the adapter depends on a narrowed RecommendClient seam (one
// method) so tests fake only what they exercise; the concrete generated
// consumptionv1.ConsumptionClient satisfies it structurally.
package consumptionrag

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	consumptionv1 "github.com/apollo-chora/chora-contracts/gen/go/chora/services/consumption/v1"

	"github.com/apollo-chora/chora-recommender/internal/tool"
)

// RecommendClient is the slice of consumptionv1.ConsumptionClient the adapter
// uses. Narrowed to one method so tests fake only RecommendAtomsForLearner; the
// concrete generated client satisfies it structurally.
type RecommendClient interface {
	RecommendAtomsForLearner(
		ctx context.Context,
		in *consumptionv1.RecommendAtomsForLearnerRequest,
		opts ...grpc.CallOption,
	) (*consumptionv1.RecommendAtomsForLearnerResponse, error)
}

// Searcher implements tool.Searcher over the consumption gRPC RPC.
type Searcher struct {
	client RecommendClient
}

// NewSearcher constructs the adapter. Panics on a nil client (programmer error —
// the composition root must dial CONSUMPTION_GRPC_ENDPOINT first).
func NewSearcher(client RecommendClient) *Searcher {
	if client == nil {
		panic("consumptionrag: NewSearcher requires a non-nil Consumption client")
	}
	return &Searcher{client: client}
}

// Compile-time guarantee the adapter satisfies the tool port.
var _ tool.Searcher = (*Searcher)(nil)

// Search calls the consumption RPC and maps the proto recommendations into
// tool.Atom. The RankScore descends by result order (the server already returns
// them ranked: topic-matches first, then recency) so the model + UI preserve a
// stable preference order. Snippet carries the topic as a lightweight UI hook.
func (s *Searcher) Search(ctx context.Context, p tool.SearchParams) ([]tool.Atom, error) {
	resp, err := s.client.RecommendAtomsForLearner(ctx, &consumptionv1.RecommendAtomsForLearnerRequest{
		TenantId:    p.TenantID,
		LearnerGcid: p.LearnerGCID,
		TopicHint:   p.TopicHint,
		Limit:       int32(p.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("consumptionrag: RecommendAtomsForLearner: %w", err)
	}

	recs := resp.GetRecommendations()
	atoms := make([]tool.Atom, 0, len(recs))
	for i, r := range recs {
		if r == nil {
			continue
		}
		atoms = append(atoms, tool.Atom{
			AtomID:    r.GetAtomId(),
			Title:     r.GetTitle(),
			Snippet:   r.GetTopic(),
			RankScore: rankScore(i, len(recs)),
		})
	}
	return atoms, nil
}

// rankScore maps a 0-based rank position to a descending [~0, 0.95] score so the
// first result outranks the last. Deterministic; mirrors the stub's 0.95-0.07*i
// shape so downstream UI affordances are unchanged.
func rankScore(i, _ int) float64 {
	return 0.95 - 0.07*float64(i)
}
