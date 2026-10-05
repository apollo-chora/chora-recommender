// instruction_provider.go — per-turn InstructionProvider wiring for the
// Content Recommender crew (ADR-169; the deferred "Iter 3.5"
// runtime instruction composition).
//
// The composer interpolates the learner's tenant / gcid / topic_hint + persona
// from session state. At boot those values are unknown, so the agent must
// recompose its instruction PER TURN from the session state the consumption
// Recommend client populated at async_create_session (tenant_id / user_gcid /
// learner_persona / topic_hint). llmagent.Config.InstructionProvider takes
// precedence over the static Instruction field, so wiring this fixes the
// regression where the boot-time "<filled at runtime>" placeholder reached the
// model — it saw topic_hint=<filled at runtime>, declared "Missing topic_hint",
// and returned no picks.
package agent

import (
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
)

// NewInstructionProvider returns an llmagent.InstructionProvider that composes
// the recommender's instruction from the live session state on each turn. Wire
// it into llmagent.Config.InstructionProvider.
func NewInstructionProvider() llmagent.InstructionProvider {
	return func(rc agent.ReadonlyContext) (string, error) {
		if rc == nil {
			return composeFromState(nil), nil
		}
		st := rc.ReadonlyState()
		if st == nil {
			return composeFromState(nil), nil
		}
		return composeFromState(st), nil
	}
}
