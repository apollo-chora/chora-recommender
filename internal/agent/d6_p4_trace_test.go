package agent

import "testing"

// D6 Pillar 4 — OTel span emission contract. Span attributes must
// cover learner instancing for cross-tenant attribution.

func TestD6P4_mandatorySpanAttributesNoDuplicates(t *testing.T) {
	got := MandatorySpanAttributes()
	seen := make(map[string]struct{}, len(got))
	for _, k := range got {
		if _, dup := seen[k]; dup {
			t.Errorf("duplicate attribute %q", k)
		}
		seen[k] = struct{}{}
	}
}
