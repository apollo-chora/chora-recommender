package main

// Interim W3 stamp (ADR-254 D7, coordinator ruling 2026-08-22 17:57Z): every
// gateway client of this web-mode binary names its crew as the Invoke
// surface; the D7 gateway refuses an unstamped call. The tests pin the stamp
// per client so a future edit cannot drop it silently.
//
// The model id is env-only: resolveModel reads RECOMMENDER_MODEL (REQUIRED)
// and the optional RECOMMENDER_FALLBACK_MODELS chain. The tests below assert an
// unset var is a boot error and that the env-provided value is what reaches the
// gateway client identity.

import (
	"testing"
)

func Test_resolveModel_requiresModelVar(t *testing.T) {
	t.Setenv(envRecommenderModel, "")
	if _, _, err := resolveModel(); err == nil {
		t.Fatal("want an error when RECOMMENDER_MODEL is unset")
	}
}

func Test_resolveModel_envOnly(t *testing.T) {
	t.Setenv(envRecommenderModel, "env-recommender")
	t.Setenv(envRecommenderFallback, "env-fb-a, env-fb-b")

	model, fallback, err := resolveModel()
	if err != nil {
		t.Fatalf("resolveModel: %v", err)
	}
	if model != "env-recommender" {
		t.Fatalf("model = %q, want the env-provided value", model)
	}
	if len(fallback) != 2 || fallback[0] != "env-fb-a" || fallback[1] != "env-fb-b" {
		t.Fatalf("fallback = %v, want the env-provided chain", fallback)
	}
}

func Test_resolveModel_noFallbackWhenUnset(t *testing.T) {
	t.Setenv(envRecommenderModel, "env-recommender")
	t.Setenv(envRecommenderFallback, "")

	_, fallback, err := resolveModel()
	if err != nil {
		t.Fatalf("resolveModel: %v", err)
	}
	if fallback != nil {
		t.Fatalf("fallback = %v, want no chain when the env var is blank", fallback)
	}
}

func Test_recommenderGatewayConfig_stampsTheSurface(t *testing.T) {
	t.Setenv(envRecommenderModel, "env-recommender")
	t.Setenv(envRecommenderFallback, "env-fb")
	modelName, modelFallback, err := resolveModel()
	if err != nil {
		t.Fatalf("resolveModel: %v", err)
	}

	cfg := recommenderGatewayConfig("crew-x", "gw:443", "gcid-1", "tenant-1", modelName, modelFallback, "https://gateway.test.invalid", false)
	if cfg.Surface != crewSurface || crewSurface != "content_recommender" {
		t.Fatalf("surface = %q, want the ADR-254 D7 crew id content_recommender", cfg.Surface)
	}
	if cfg.AgentID != "content_recommender" || cfg.Endpoint != "gw:443" || cfg.TenantID != "tenant-1" || cfg.GCID != "gcid-1" || cfg.CrewKind != "crew-x" || cfg.LogicalModelID != "env-recommender" || len(cfg.FallbackModelIDs) != 1 || cfg.FallbackModelIDs[0] != "env-fb" {
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
	cfg := recommenderGatewayConfig("crew-x", "gw:443", "gcid-1", "tenant-1", "m", nil, "aud", true)
	if !cfg.Insecure {
		t.Fatalf("Insecure must forward to the gateway client config")
	}
}
