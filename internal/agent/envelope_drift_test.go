package agent_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	ragent "github.com/apollo-chora/chora-recommender/internal/agent"
	tools "github.com/apollo-chora/chora-recommender/internal/tool"
)

// ragent.ToolAtom is a hand-copy of tools.Atom, declared in the agent package so
// the envelope has no dependency on the tool package. A hand-copy is a drift
// risk, and the envelope comment CLAIMS this test backs it, so the test has to
// exist or the comment is false.
//
// This lives in package agent_test (external) because it must import BOTH
// packages, and the whole point of the copy is that agent does not import tool.

func jsonShape(t *testing.T, v any) map[string]string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out := map[string]string{}
	for k, val := range m {
		out[k] = reflect.TypeOf(val).Kind().String()
	}
	return out
}

func TestToolAtomIsStillAFaithfulCopyOfTheToolsAtom(t *testing.T) {
	// Non-zero on EVERY field: a zero value would be omitted by any stray
	// omitempty and the shapes would match vacuously.
	real := tools.Atom{AtomID: "a", Title: "t", Snippet: "s", RankScore: 1.5}
	copyOf := ragent.ToolAtom{AtomID: "a", Title: "t", Snippet: "s", RankScore: 1.5}

	realShape, copyShape := jsonShape(t, real), jsonShape(t, copyOf)
	if !reflect.DeepEqual(realShape, copyShape) {
		t.Fatalf("ToolAtom has drifted from tool.Atom.\n  tool.Atom: %v\n  ToolAtom : %v\n"+
			"Update ToolAtom (and the envelope map) rather than loosening this test: the "+
			"kennel reads these keys and a dropped field is silent there.", realShape, copyShape)
	}

	// And the field COUNT, so a field added to tool.Atom that the copy lacks
	// fails here rather than being silently absent from every completion.
	if got, want := reflect.TypeOf(copyOf).NumField(), reflect.TypeOf(real).NumField(); got != want {
		keys := make([]string, 0, len(realShape))
		for k := range realShape {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Fatalf("tool.Atom has %d fields, ToolAtom has %d (tool.Atom keys: %v)", want, got, keys)
	}
}

func TestTheEnvelopeCarriesEveryToolAtomFieldThrough(t *testing.T) {
	// The 1:1 map itself: marshal a tool.Atom, feed the same values through
	// EnvelopeFromToolAtoms, and assert no key is lost on the way out.
	realShape := jsonShape(t, tools.Atom{AtomID: "a", Title: "t", Snippet: "s", RankScore: 1.5})

	env := ragent.EnvelopeFromToolAtoms(
		[]ragent.ToolAtom{{AtomID: "a", Title: "t", Snippet: "s", RankScore: 1.5}},
		"why", "m", "v", ragent.PromptSourceConfig)

	var out struct {
		Atoms []map[string]any `json:"atoms"`
	}
	if err := json.Unmarshal([]byte(env.Render()), &out); err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(out.Atoms) != 1 {
		t.Fatalf("want 1 atom, got %d", len(out.Atoms))
	}
	for key := range realShape {
		if _, ok := out.Atoms[0][key]; !ok {
			t.Errorf("tool.Atom field %q does not survive into the envelope", key)
		}
	}
}
