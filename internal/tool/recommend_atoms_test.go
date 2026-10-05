package tool

import (
	"context"
	"strings"
	"testing"
)

func TestRecommendAtomsForLearner_stubModeReturnsRankedAtoms(t *testing.T) {
	t.Setenv("CONSUMPTION_GRPC_ENDPOINT", "stub://chora-consumption")
	resp, err := RecommendAtomsForLearner(context.Background(), RecommendAtomsRequest{
		LearnerGCID: "01957c8c-aaaa-7000-bbbb-cccccccccccc",
		Persona:     "curious-explorer",
		TopicHint:   "graph-algorithms",
		Limit:       3,
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(resp.Atoms) != 3 {
		t.Errorf("want 3 atoms; got %d", len(resp.Atoms))
	}
	for _, a := range resp.Atoms {
		if !strings.HasPrefix(a.AtomID, "atom-stub-") {
			t.Errorf("want stub atom_id; got %q", a.AtomID)
		}
	}
}

func TestRecommendAtomsForLearner_defaultLimitIsFive(t *testing.T) {
	t.Setenv("CONSUMPTION_GRPC_ENDPOINT", "stub://chora-consumption")
	resp, err := RecommendAtomsForLearner(context.Background(), RecommendAtomsRequest{
		LearnerGCID: "u-1",
		Persona:     "cert-focused",
		TopicHint:   "calculus",
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(resp.Atoms) != 5 {
		t.Errorf("default limit must be 5; got %d", len(resp.Atoms))
	}
}

func TestRecommendAtomsForLearner_personaBiasesRanking(t *testing.T) {
	t.Setenv("CONSUMPTION_GRPC_ENDPOINT", "stub://chora-consumption")
	curiousResp, _ := RecommendAtomsForLearner(context.Background(), RecommendAtomsRequest{
		LearnerGCID: "u-1", Persona: "curious-explorer", TopicHint: "x", Limit: 3,
	})
	certResp, _ := RecommendAtomsForLearner(context.Background(), RecommendAtomsRequest{
		LearnerGCID: "u-1", Persona: "cert-focused", TopicHint: "x", Limit: 3,
	})
	// Personas must influence ranking — stub bakes persona into the
	// snippet text so different personas produce visibly different output.
	if curiousResp.Atoms[0].Snippet == certResp.Atoms[0].Snippet {
		t.Error("persona must bias snippet text (no-op stub bug?)")
	}
}

func TestRecommendAtomsForLearner_missingEndpointRefuses(t *testing.T) {
	t.Setenv("CONSUMPTION_GRPC_ENDPOINT", "")
	_, err := RecommendAtomsForLearner(context.Background(), RecommendAtomsRequest{
		LearnerGCID: "u-1", Persona: "x", TopicHint: "x",
	})
	if err == nil {
		t.Fatal("missing CONSUMPTION_GRPC_ENDPOINT must refuse")
	}
}
