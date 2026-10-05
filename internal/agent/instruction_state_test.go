package agent

import (
	"errors"
	"testing"
)

// fakeState is a minimal StateReader for testing TaskContextFromState without
// the ADK runtime. Get returns errAbsent for keys not in the map (mirroring the
// ADK session store's key-absent error).
type fakeState struct{ m map[string]any }

var errAbsent = errors.New("absent")

func (f fakeState) Get(key string) (any, error) {
	if v, ok := f.m[key]; ok {
		return v, nil
	}
	return nil, errAbsent
}

func TestTaskContextFromState_readsCanonicalKeys(t *testing.T) {
	st := fakeState{m: map[string]any{
		"tenant_id":  "11111111-1111-7111-8111-111111111111",
		"user_gcid":  "00000000-0000-7000-8000-000000001999",
		"topic_hint": "scrum",
	}}
	tc := TaskContextFromState(st)
	if tc.TenantID != "11111111-1111-7111-8111-111111111111" {
		t.Errorf("TenantID = %q", tc.TenantID)
	}
	if tc.LearnerGCID != "00000000-0000-7000-8000-000000001999" {
		t.Errorf("LearnerGCID = %q", tc.LearnerGCID)
	}
	if tc.TopicHint != "scrum" {
		t.Errorf("TopicHint = %q", tc.TopicHint)
	}
}

// The consumption Recommend client writes user_gcid; tolerate a learner_gcid
// alias so the resolver is robust to either caller convention.
func TestTaskContextFromState_userGCIDAliasFallback(t *testing.T) {
	st := fakeState{m: map[string]any{"learner_gcid": "gcid-alias"}}
	if tc := TaskContextFromState(st); tc.LearnerGCID != "gcid-alias" {
		t.Errorf("LearnerGCID alias fallback = %q", tc.LearnerGCID)
	}
}

func TestTaskContextFromState_missingKeysAreEmpty(t *testing.T) {
	if tc := TaskContextFromState(fakeState{m: map[string]any{}}); tc != (TaskContext{}) {
		t.Errorf("missing keys should yield zero TaskContext; got %+v", tc)
	}
}

func TestTaskContextFromState_nilStateIsEmpty(t *testing.T) {
	if tc := TaskContextFromState(nil); tc != (TaskContext{}) {
		t.Errorf("nil state should yield zero TaskContext; got %+v", tc)
	}
}

// Regression: the composed instruction must carry the REAL runtime context — not
// the boot-time "<filled at runtime>" placeholder. That placeholder is what
// stranded the recommender: the model saw topic_hint=<filled at runtime> and the
// required-topic_hint tool schema, said "Missing topic_hint", and returned no
// picks (RUNTIME_ERROR). composeFromState is what NewInstructionProvider runs
// per turn.
func TestComposeFromState_carriesRuntimeContext_noPlaceholder(t *testing.T) {
	st := fakeState{m: map[string]any{
		"tenant_id":       "tid-1",
		"user_gcid":       "gcid-1",
		"topic_hint":      "kubernetes",
		"learner_persona": "cert-focused",
	}}
	got := composeFromState(st)
	if !contains(got, "kubernetes") {
		t.Errorf("instruction must carry the runtime topic_hint; got:\n%s", got)
	}
	if contains(got, "filled at runtime") {
		t.Errorf("instruction must NOT contain the boot placeholder")
	}
	if !contains(got, "cert-focused") {
		t.Errorf("instruction must reflect the runtime learner_persona")
	}
}

// With no topic in state the instruction must still compose cleanly (topic_hint
// is now OPTIONAL — the tool can be called without it). The composer renders the
// "<any>" sentinel, never the boot placeholder.
func TestComposeFromState_noTopicStillComposes(t *testing.T) {
	st := fakeState{m: map[string]any{"tenant_id": "tid", "user_gcid": "gcid"}}
	got := composeFromState(st)
	if contains(got, "filled at runtime") {
		t.Errorf("instruction must NOT contain the boot placeholder when topic absent")
	}
	if !contains(got, "<any>") {
		t.Errorf("absent topic should render the <any> sentinel; got:\n%s", got)
	}
}
