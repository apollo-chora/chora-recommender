// Package agent holds the Content Recommender P1 single-agent ReAct
// crew's CREATE-pattern composer + few-shot loader + D6 attribute contract.
//
// Phyllis Step 8 daily-dose 40% slot per docs/RESUME_PROMPT_PHYLLIS_UX_2026-05-12.md.
// Companion to Familiar (CHO-1525, 30% slot) + Ebbinghaus (future iter, 30% slot).
//
// Unlike Familiar there is NO per-instance config (no per-Recommender entity).
// The only learner-axis is `learner_persona` (curious-explorer / cert-focused /
// social-leader per ADR-116 Amendment 2). Persona drives both the [AUDIENCE]
// tone calibration AND the few-shot fixture selection.
package agent

import (
	"fmt"
	"strings"
)

// TaskContext is the per-call context the composer weaves into the
// [CONTEXT] block. Populated from session.State() by upstream chora-web
// at session-create time.
type TaskContext struct {
	TenantID    string
	LearnerGCID string
	TopicHint   string
}

// StateReader is the minimal readonly session-state seam used to build a
// per-turn TaskContext. It matches the ADK ReadonlyState contract
// (Get(key) → (value, error); error when the key is absent), so an
// llmagent.InstructionProvider can pass `rc.ReadonlyState()` directly while
// tests substitute a fake. Mirrors the moderation crew's StateReader.
type StateReader interface {
	Get(key string) (any, error)
}

// TaskContextFromState builds a per-call TaskContext from the session state the
// consumption Recommend client populated at async_create_session (tenant_id /
// user_gcid / topic_hint). This is the runtime wiring the boot-time static
// instruction could never carry: previously the agent was composed once at
// startup with a literal "<filled at runtime>" placeholder for TopicHint (the
// deferred "Iter 3.5" InstructionProvider was never wired), so the model
// received the placeholder topic, refused to call the required-topic_hint tool,
// and returned no picks. user_gcid is the canonical key; learner_gcid is
// tolerated as an alias. Missing / non-string keys yield empty fields (safe
// fallback handled downstream by safe()).
func TaskContextFromState(state StateReader) TaskContext {
	if state == nil {
		return TaskContext{}
	}
	gcid := stateString(state, "user_gcid")
	if gcid == "" {
		gcid = stateString(state, "learner_gcid")
	}
	return TaskContext{
		TenantID:    stateString(state, "tenant_id"),
		LearnerGCID: gcid,
		TopicHint:   stateString(state, "topic_hint"),
	}
}

// composeFromState composes the per-turn recommender instruction from the live
// session state. Persona comes from the learner_persona key (defaults to
// curious-explorer at compose); the rest of the context comes from
// TaskContextFromState. Pure given the state — what NewInstructionProvider runs
// on each turn.
func composeFromState(state StateReader) string {
	persona := stateString(state, "learner_persona")
	return ComposeRecommenderInstruction(persona, TaskContextFromState(state))
}

// stateString reads a string value from session state; absent / wrong-typed
// keys return "".
func stateString(state StateReader, key string) string {
	if state == nil {
		return ""
	}
	raw, err := state.Get(key)
	if err != nil {
		return ""
	}
	s, _ := raw.(string)
	return s
}

// ComposeRecommenderInstruction emits the deterministic 6-block CREATE
// prompt. Pure function — same (persona, ctx) in always yields same
// prompt out (IMDA D2 transparency per ADR-141).
func ComposeRecommenderInstruction(persona string, ctx TaskContext) string {
	canonical := canonicalisePersona(persona)

	var b strings.Builder

	// [CONTEXT] — platform framing + per-call context.
	b.WriteString("## [CONTEXT]\n")
	b.WriteString("You are running inside the Chora learning platform as the Content Recommender — " +
		"the 40% slot of the A+ surface's daily-dose feature. Every reply is audited against IMDA " +
		"Model AI Governance criteria.\n")
	fmt.Fprintf(&b, "Call context: tenant_id=%s, learner_gcid=%s, topic_hint=%s.\n\n",
		safe(ctx.TenantID, "<unset>"), safe(ctx.LearnerGCID, "<unset>"), safe(ctx.TopicHint, "<any>"))

	// [ROLE]
	b.WriteString("## [ROLE]\n")
	b.WriteString("You are the Content Recommender — a P1 single-agent ReAct crew that surfaces 3-5 " +
		"new LearningAtoms the learner is most likely to engage with right now. You are NOT a tutor; " +
		"the Familiar Companion crew handles conversation. You output ranked atom recommendations.\n\n")

	// [EXAMPLES] — 3 few-shots for the persona.
	b.WriteString("## [EXAMPLES]\n")
	examples := lookupFewShots(canonical)
	for i, ex := range examples {
		fmt.Fprintf(&b, "Example %d:\n", i+1)
		fmt.Fprintf(&b, "  Learner: %s\n", strings.TrimSpace(ex.UserPrompt))
		fmt.Fprintf(&b, "  Recommender: %s\n", strings.TrimSpace(ex.AssistantReply))
	}
	b.WriteString("\n")

	// [AUDIENCE] — persona-driven tone.
	b.WriteString("## [AUDIENCE]\n")
	fmt.Fprintf(&b, "Your learner is a %s — %s\n\n", canonical, personaSummary(canonical))

	// [TASK]
	b.WriteString("## [TASK]\n")
	b.WriteString("- Use the recommend_atoms_for_learner tool to retrieve candidate atoms.\n")
	b.WriteString("- Use the query_knowledge_graph tool when the learner's topic_hint is " +
		"under-specified (lean on neighbour atoms in the KG).\n")
	b.WriteString("- Score each candidate via score_atom_for_learner (recency × persona-fit × difficulty-match).\n")
	b.WriteString("- Cite every recommended atom_id via cite_atom BEFORE emitting it to the learner.\n")
	b.WriteString("- NEVER fabricate atom_ids — if no atom matches, return an empty list with reason.\n\n")

	// [EXPECTED OUTPUT]
	b.WriteString("## [EXPECTED OUTPUT]\n")
	b.WriteString("JSON: {\"recommendations\": [{\"atom_id\": string, \"title\": string, " +
		"\"why_this_atom\": string (1 sentence persona-attuned hook)}], \"reason\": string}. " +
		"Length 3-5 recommendations OR empty list + reason. NEVER write commentary outside the JSON.\n")

	return b.String()
}

// canonicalisePersona returns the canonical learner_persona value,
// defaulting empty/unknown to "curious-explorer" per ADR-116 Amendment 2.
func canonicalisePersona(in string) string {
	switch strings.ToLower(strings.TrimSpace(in)) {
	case "cert-focused":
		return "cert-focused"
	case "social-leader":
		return "social-leader"
	case "curious-explorer", "":
		return "curious-explorer"
	default:
		return "curious-explorer"
	}
}

func personaSummary(persona string) string {
	switch persona {
	case "cert-focused":
		return "engages content to clear a specific certification gate. Frame " +
			"recommendations around path progression + curriculum blueprints; de-emphasise " +
			"lateral exploration."
	case "social-leader":
		return "engages content by teaching others. Frame recommendations as shareable " +
			"atoms (likely to surface in C+ posts/duels); mention discussion hooks."
	default:
		return "engages content by following novelty. Frame recommendations as open " +
			"invitations + neighbour-atom suggestions; lean into 'why' before 'how'."
	}
}

func lookupFewShots(persona string) []FewShotExample {
	store, err := LoadFewShots()
	if err != nil || store == nil {
		return nil
	}
	if ex, ok := store.Lookup(persona); ok {
		return ex
	}
	if ex, ok := store.Lookup("curious-explorer"); ok {
		return ex
	}
	return nil
}

// MandatorySpanAttributes is the canonical OTel span attribute list every
// Recommender span MUST stamp. Per agentic-resilience-d6
// SKILL Pillar 4 + ADR-141 D1 accountability.
func MandatorySpanAttributes() []string {
	return []string{
		"chora.tenant_id",
		"chora.learner_gcid",
		"chora.mana_tier",
		"chora.crew_kind",
		"gen_ai.request.model",
		"gen_ai.usage.output_tokens",
	}
}

func safe(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
