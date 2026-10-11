package main

// Interim W3 stamp (ADR-254 D7, coordinator ruling 2026-08-22 17:57Z): every
// gateway client of this web-mode binary names its crew as the Invoke
// surface; the D7 gateway refuses an unstamped call. The tests pin the stamp
// per client so a future edit cannot drop it silently.

import (
	"testing"

	"github.com/apollo-chora/chora-recommender/internal/agentconfig"
)

func Test_recommenderGatewayConfig_stampsTheSurface(t *testing.T) {
	cfg := recommenderGatewayConfig("crew-x", "gw:443", "gcid-1", "tenant-1", "longcat-2.5-preview", agentconfig.SubAgentConfig{FallbackModels: []string{"fb"}}, "https://gateway.test.invalid", false)
	if cfg.Surface != crewSurface || crewSurface != "content_recommender" {
		t.Fatalf("surface = %q, want the ADR-254 D7 crew id content_recommender", cfg.Surface)
	}
	if cfg.AgentID != "content_recommender" || cfg.Endpoint != "gw:443" || cfg.TenantID != "tenant-1" || cfg.GCID != "gcid-1" || cfg.CrewKind != "crew-x" || cfg.LogicalModelID != "longcat-2.5-preview" || len(cfg.FallbackModelIDs) != 1 || cfg.FallbackModelIDs[0] != "fb" {
		t.Fatalf("identity changed: %+v", cfg)
	}
	if cfg.ActionCode != "content_recommendation" {
		t.Fatalf("action code = %q, want %q", cfg.ActionCode, "content_recommendation")
	}
	if cfg.Insecure {
		t.Fatalf("Insecure must default to false (production posture: TLS + token)")
	}
}

func Test_recommenderGatewayConfig_insecureFlag(t *testing.T) {
	cfg := recommenderGatewayConfig("crew-x", "gw:443", "gcid-1", "tenant-1", "m", agentconfig.SubAgentConfig{}, "aud", true)
	if !cfg.Insecure {
		t.Fatalf("Insecure must forward to the gateway client config")
	}
}
