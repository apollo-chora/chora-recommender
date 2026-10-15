package agentconfig_test

import (
	"testing"

	"github.com/apollo-chora/chora-recommender/internal/agentconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecommender_HighTierLadder(t *testing.T) {
	cfg, err := agentconfig.Recommender()
	require.NoError(t, err)
	assert.Equal(t, "content_recommender", cfg.Agent)

	// The model id is env-only (RECOMMENDER_MODEL), so the YAML carries no
	// model name to assert — the Tier assertion below carries the intent.

	// HIGH tier — the single ranking sub-agent.
	recommend, err := cfg.Sub("recommend")
	require.NoError(t, err)
	assert.Equal(t, "high", recommend.Tier)
	assert.Equal(t, "v1", recommend.PromptVersion)
}

func TestSub_MissingSubAgentFailsLoud(t *testing.T) {
	cfg, err := agentconfig.Recommender()
	require.NoError(t, err)
	_, err = cfg.Sub("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no sub-agent")
}
