package agent

import (
	"sync"
	"testing"
)

// D6 Pillar 3 — Multi-tenant + multi-workflow isolation (stub). Recommender
// is stateless w.r.t. tenant — N concurrent compose calls must NOT bleed
// state across tenants.

func TestD6P3_composeIsConcurrencySafeAcrossTenants(t *testing.T) {
	const N = 32
	var wg sync.WaitGroup
	results := make([]string, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ctx := TaskContext{
				TenantID:    "tenant-" + string(rune('A'+idx%26)),
				LearnerGCID: "learner-stub",
				TopicHint:   "topic-stub",
			}
			results[idx] = ComposeRecommenderInstruction("curious-explorer", ctx)
		}(i)
	}
	wg.Wait()

	// Each tenant_id should surface in its own prompt — no cross-bleed.
	for i, got := range results {
		wantSubstr := "tenant-" + string(rune('A'+i%26))
		if got == "" {
			t.Errorf("goroutine %d: empty prompt (race?)", i)
		}
		if !contains(got, wantSubstr) {
			t.Errorf("goroutine %d: prompt missing own tenant_id %q (cross-bleed?)", i, wantSubstr)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
