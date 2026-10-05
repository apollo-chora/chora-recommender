// Package agentconfig holds the build-time, per-agent model + prompt
// configuration for the content_recommender crew (AGENT-DRIVEN tiering,
// mirrors qgen CR 2026-06-01).
//
// Each agent owns its OWN embedded YAML (separation of concern) declaring,
// per sub-agent: tier / primary_model / fallback_models / prompt_version. The
// agent reads its config at boot and:
//   - uses primary_model as the model id (recommender calls the model
//     broker — chora-model-gateway — via modelgatewayclient; FallbackModels
//     feeds InvokeRequest.fallback_logical_model_ids unchanged, and the
//     gateway honours the chain),
//   - records fallback_models as the declared fallback intent,
//   - composes the prompt_version'd template.
//
// The YAML is the single source of truth; ops may override the primary via env
// var (RECOMMENDER_MODEL) for quick experiments, but fallback + tier + prompt
// version stay config-declared.
//
// Mana is a token-budget QUOTA system (manaplugin gate), NOT a model selector.
// The tieredmodelplugin (mana → model swap) is DELIBERATELY NOT registered in
// cmd/recommender/main.go — model selection is config-driven here. See
// feedback_mana_is_quota_not_model_selector.
package agentconfig

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed recommender.yaml
var recommenderYAML []byte

// SubAgentConfig is one sub-agent's resolved model tier + prompt selection.
type SubAgentConfig struct {
	Tier           string   `yaml:"tier"`
	PrimaryModel   string   `yaml:"primary_model"`
	FallbackModels []string `yaml:"fallback_models"`
	PromptVersion  string   `yaml:"prompt_version"`
}

// AgentConfig is one agent binary's full per-sub-agent config.
type AgentConfig struct {
	Agent     string                    `yaml:"agent"`
	SubAgents map[string]SubAgentConfig `yaml:"sub_agents"`
}

// Sub returns the named sub-agent config, failing loud if the YAML omits it —
// a missing sub-agent is a build/config error, never a silent default (per
// feedback_no_stubs_real_wiring).
func (c AgentConfig) Sub(name string) (SubAgentConfig, error) {
	sc, ok := c.SubAgents[name]
	if !ok {
		return SubAgentConfig{}, fmt.Errorf("agentconfig: agent %q has no sub-agent %q", c.Agent, name)
	}
	if sc.PrimaryModel == "" {
		return SubAgentConfig{}, fmt.Errorf("agentconfig: agent %q sub-agent %q has empty primary_model", c.Agent, name)
	}
	if sc.PromptVersion == "" {
		return SubAgentConfig{}, fmt.Errorf("agentconfig: agent %q sub-agent %q has empty prompt_version", c.Agent, name)
	}
	return sc, nil
}

// Recommender parses the embedded content_recommender config.
func Recommender() (AgentConfig, error) { return parse(recommenderYAML) }

func parse(raw []byte) (AgentConfig, error) {
	var c AgentConfig
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return AgentConfig{}, fmt.Errorf("agentconfig: unmarshal: %w", err)
	}
	if c.Agent == "" {
		return AgentConfig{}, fmt.Errorf("agentconfig: missing top-level agent name")
	}
	if len(c.SubAgents) == 0 {
		return AgentConfig{}, fmt.Errorf("agentconfig: agent %q declares no sub_agents", c.Agent)
	}
	return c, nil
}
