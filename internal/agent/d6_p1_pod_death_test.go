package agent

import "testing"

// D6 Pillar 1 — Pod-death chaos (stub). Recommender is stateless: the
// recovery key is the session.State() snapshot (tenant_id + learner_gcid +
// mana_tier), NOT a per-instance entity like Familiar's familiar_id.

func TestD6P1_composerIsPureAcrossRecovery(t *testing.T) {
	ctx := TaskContext{TenantID: "t", LearnerGCID: "g", TopicHint: "x"}
	pre := ComposeRecommenderInstruction("curious-explorer", ctx)
	post := ComposeRecommenderInstruction("curious-explorer", ctx)
	if pre != post {
		t.Error("composer must be pure across recovery (D6 P1)")
	}
}
