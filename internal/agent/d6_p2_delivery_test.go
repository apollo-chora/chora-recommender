package agent

import (
	"strings"
	"testing"
)

// D6 Pillar 2 — Delivery resilience (stub). Recommender's termination
// emits chora.ai_kernel.agent.terminated.v1 like every other crew.

func TestD6P2_terminationEventTopicCanonical(t *testing.T) {
	want := "chora.ai_kernel.agent.terminated.v1"
	if !strings.HasPrefix(want, "chora.ai_kernel.") {
		t.Errorf("canonical topic must live under chora.ai_kernel.*; got %q", want)
	}
	if !strings.HasSuffix(want, ".v1") {
		t.Errorf("canonical topic must carry .v1; got %q", want)
	}
}
