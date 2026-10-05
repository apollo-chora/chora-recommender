// Command recommender is the entry point for the Content Recommender crew
// (Pattern P1 single-agent ReAct per crew-composition SKILL §1 + §2).
//
// Phyllis Step 8 daily-dose 40% slot. Unlike Familiar (CHO-1525) there is
// NO per-instance config — the crew serves any learner via persona-driven
// ranking. Persona comes from session.State() set by upstream chora-web
// at session-create time.
//
// Per ADR-138 §1 Go-first + ADR-145 polyglot model-broker pivot + ADR-146
// Model Broker full retirement + ADR-177 model-gateway metering:
//   - Calls the model broker (chora-model-gateway) via modelgatewayclient
//   - Per-user mana gate + metering is the gateway's job (action_code
//     content_recommendation, debited once per turn-initiating Invoke)
//   - terminationplugin emits chora.ai_kernel.agent.terminated.v1
//   - Auto-wires standard OTLP traces via chora-adk-common/tracing
//
// Model selection is AGENT-DRIVEN (mirrors qgen CR 2026-06-01): the recommend
// sub-agent's model tier + fallback chain is config-declared in the embedded
// agentconfig YAML (HIGH = gemini-3.1-pro-preview → gemini-2.5-pro). Mana is a
// token-budget QUOTA system (gateway-side metering), NOT a model selector, so
// the tieredmodelplugin (mana → per-call model swap) is DELIBERATELY NOT
// registered — see feedback_mana_is_quota_not_model_selector.
//
// Deploy: any host running the Go binary or the container image. The crew
// serves the standard ADK REST API on :8080:
//
//	POST /api/apps/{app}/users/{user}/sessions          create session
//	POST /api/apps/{app}/users/{user}/sessions/{id}     create session with id
//	POST /api/run                                        run the agent (JSON events)
//	POST /api/run_sse                                    run the agent (SSE stream)
//
// Callers create the session first, then run turns against it. The app_name
// path segment is the ADK agent name — "content_recommender" (or empty).
// Session state carries the per-learner context the tool + instruction
// provider read:
//
//	state: {
//	  tenant_id:    "<tenant-uuid>",   // required by the recommend tool (RLS)
//	  user_gcid:    "<gcid>",          // learner identity (learner_gcid alias tolerated)
//	  learner_persona: "curious-explorer", // optional; defaults at compose
//	  topic_hint:   "graph-algorithms",     // optional; <any> when absent
//	  atom_candidates: "[{...}]",           // optional; pre-fetched ranked atoms
//	}
//
// Env vars (NEVER inlined per feedback_no_inline_config):
//
//	RECOMMENDER_MODEL             — optional ops override of the agentconfig primary_model
//	                                (HIGH tier = gemini-3.1-pro-preview); no per-call mana swap
//	PORT                          — REST API listen port
//	TENANCY_GRPC_ENDPOINT         — stub:// for POC; gRPC URL in prod
//	CONSUMPTION_GRPC_ENDPOINT     — stub:// for POC; gRPC URL in prod
//	CREATION_GRPC_ENDPOINT        — stub:// for POC; gRPC URL in prod (cite_atom)
//	CHORA_ENV                     — dev | staging | prod
//	CHORA_GATEWAY_ENDPOINT        — model-gateway gRPC target
//	CHORA_GATEWAY_TENANT_ID       — process-fallback tenant for gateway Invoke
//	CHORA_GATEWAY_GCID            — process-fallback actor for gateway Invoke
//	CHORA_GATEWAY_AUDIENCE        — audience of the gateway token
//	CHORA_GATEWAY_INSECURE        — plaintext gRPC to a local gateway (dev only)
//	CHORA_GATEWAY_TOKEN           — static bearer token for the gateway
//	OTEL_EXPORTER_OTLP_ENDPOINT   — OTLP trace endpoint (stdout when unset)
package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/server/adkrest"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	consumptionv1 "github.com/apollo-chora/chora-contracts/gen/go/chora/services/consumption/v1"

	"github.com/apollo-chora/chora-adk-common/modelgatewayclient"
	"github.com/apollo-chora/chora-adk-common/terminationplugin"
	"github.com/apollo-chora/chora-adk-common/tracing"

	"github.com/apollo-chora/chora-recommender/internal/adapter/consumptionrag"
	recoagent "github.com/apollo-chora/chora-recommender/internal/agent"
	"github.com/apollo-chora/chora-recommender/internal/agentconfig"
	tools "github.com/apollo-chora/chora-recommender/internal/tool"
)

const crewKind = "recommender"

// Set at build time via -ldflags (see Dockerfile).
var (
	serviceName    = "chora-recommender"
	serviceVersion = "0.1.0"
	gitSHA         = "unknown"
	buildTime      = "unknown"
)

// envOr returns os.Getenv(name) if non-empty, else fallback.
// Lesson learned from QGen Iter 2: adkgo plumbs only 4 env vars at
// CreateReasoningEngine; the Chora-specific vars are PATCHed in AFTER
// the initial start. Bootstrap must survive that first-start window.
func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	ctx := context.Background()

	// Wire the OTel exporter + W3C TraceContext propagator BEFORE any
	// agent / runner / plugin construction so every span flows to the
	// configured OTLP endpoint and continues an inbound traceparent. ADK
	// Go does NOT auto-wire this; per the trace wave 2026-05-29 every ADK
	// agent calls tracing.Init so it is traceable at per-agent level
	// (service.name = the registry name).
	traceShutdown, err := tracing.Init(ctx, "content_recommender")
	if err != nil {
		log.Fatalf("tracing.Init: %v", err)
	}
	defer func() {
		if err := traceShutdown(context.Background()); err != nil {
			slog.Error("trace shutdown error", "err", err)
		}
	}()

	// AGENT-DRIVEN model selection (mirrors qgen CR 2026-06-01): the single
	// `recommend` sub-agent's tier + primary + fallback chain is the single
	// source of truth in the embedded agentconfig YAML (HIGH = ranking
	// quality). Fail loud on a missing / malformed config — never a silent
	// default (feedback_no_stubs_real_wiring). RECOMMENDER_MODEL may override
	// the primary for quick ops experiments; tier + fallback stay
	// config-declared.
	rcfg, err := agentconfig.Recommender()
	if err != nil {
		log.Fatalf("recommender: load agent config: %v", err)
	}
	recommendCfg, err := rcfg.Sub("recommend")
	if err != nil {
		log.Fatalf("recommender: %v", err)
	}
	modelName := envOr("RECOMMENDER_MODEL", recommendCfg.PrimaryModel)

	tenancyEndpoint := envOr("TENANCY_GRPC_ENDPOINT", "stub://chora-tenancy")
	consumptionEndpoint := envOr("CONSUMPTION_GRPC_ENDPOINT", "stub://chora-consumption")
	creationEndpoint := envOr("CREATION_GRPC_ENDPOINT", "stub://chora-creation")
	choraEnv := envOr("CHORA_ENV", "dev")

	if v := os.Getenv("TENANCY_GRPC_ENDPOINT"); v == "" {
		_ = os.Setenv("TENANCY_GRPC_ENDPOINT", tenancyEndpoint)
	}
	if v := os.Getenv("CONSUMPTION_GRPC_ENDPOINT"); v == "" {
		_ = os.Setenv("CONSUMPTION_GRPC_ENDPOINT", consumptionEndpoint)
	}
	if v := os.Getenv("CREATION_GRPC_ENDPOINT"); v == "" {
		_ = os.Setenv("CREATION_GRPC_ENDPOINT", creationEndpoint)
	}

	slog.Info("recommender boot",
		"service", serviceName,
		"service_version", serviceVersion,
		"git", gitSHA,
		"built", buildTime,
		"model", modelName,
		"model_tier", recommendCfg.Tier,
		"model_fallback", recommendCfg.FallbackModels,
		"prompt_version", recommendCfg.PromptVersion,
		"tenancy_endpoint", tenancyEndpoint,
		"consumption_endpoint", consumptionEndpoint,
		"creation_endpoint", creationEndpoint,
		"chora_env", choraEnv,
	)

	// Model — route through chora-model-gateway (ADR-177 full mana umbrella).
	// Every recommend LLM turn flows through the one metered chokepoint
	// (central model policy enforcement, per-tenant budget, token-usage
	// ledger). modelName stays AGENT-DRIVEN (the agentconfig HIGH-tier
	// primary, env-overridable); fallback chain is forwarded so the gateway
	// honours it. Per-request tenant/gcid come from session state via the
	// propagation plugin; the env values are the process fallback.
	gatewayEndpoint := envOr("CHORA_GATEWAY_ENDPOINT", "gateway.chora.site:443")
	// D6 step 1: the ID-token audience is read HERE and defaulted explicitly.
	// modelgatewayclient still defaults it internally in TWO places
	// (client.go:181-182 and image.go:110-111); passing it makes the value
	// stateable and is what lets step 4 remove those defaults safely.
	gatewayAudience := envOr("CHORA_GATEWAY_AUDIENCE", "https://gateway.chora.site")
	gatewayTenantID := os.Getenv("CHORA_GATEWAY_TENANT_ID")
	gatewayGCID := os.Getenv("CHORA_GATEWAY_GCID")
	if gatewayTenantID == "" || gatewayGCID == "" {
		log.Fatalf("recommender: CHORA_GATEWAY_TENANT_ID + CHORA_GATEWAY_GCID required " +
			"(process fallback; per-request values come from session state — no silent " +
			"mis-attribution per ADR-169 + feedback_no_stubs_real_wiring)")
	}
	model, err := modelgatewayclient.New(ctx, recommenderGatewayConfig(crewKind, gatewayEndpoint, gatewayGCID, gatewayTenantID, modelName, recommendCfg, gatewayAudience, gatewayInsecure()))
	if err != nil {
		log.Fatalf("recommender: modelgatewayclient.New: %v", err)
	}

	// Per-request tenant propagation (ADR-169) — stamp the REQUESTING
	// tenant_id/user_gcid (from session state) onto every gateway Invoke so
	// RLS + ledger + budget attribute to the learner's tenant. No
	// ActionCodeResolver: the recommender's action_code is static (set above).
	tenantPropP, err := modelgatewayclient.NewTenantPropagationPlugin(crewKind)
	if err != nil {
		log.Fatalf("recommender: modelgatewayclient.NewTenantPropagationPlugin: %v", err)
	}

	// REAL RAG source (CHO-1662) — SESSION-STATE INJECTION is the PRIMARY path:
	// chora-consumption owns atom_index and already initiates the Recommend call,
	// so it pre-fetches the candidate atoms in-process and injects them into the
	// ADK session state (state["atom_candidates"]). The recommend tool reads them
	// directly — NO gRPC dial, NO mesh hop. This is the design pivot away from the
	// gRPC dial (which was BLOCKED: the crew is sidecar-less and consumption :9090
	// is STRICT-mTLS, so a plaintext dial is rejected — loosening it is a
	// Security-wins violation on a PII service). Mirrors the familiar bug-3
	// mesh-sidestep.
	//
	// The Searcher below is now the NO-STATE FALLBACK only (sandbox smoke /
	// external callers that don't inject candidates):
	//   - stub://…  → deterministic StubSearcher (the deployed posture).
	//   - gRPC URL  → consumptionrag.NewSearcher (retained as an external-caller
	//                 surface; the daily-dose path does NOT depend on dialing it).
	searcher, err := buildSearcher(ctx, consumptionEndpoint)
	if err != nil {
		log.Fatalf("recommender: buildSearcher: %v", err)
	}

	// Tool: recommend_atoms_for_learner — the main capability. The handler reads
	// tenant_id + learner gcid + the injected atom_candidates from the ADK session
	// state (RLS-bearing; NOT the model args). When candidates are present it
	// returns them (real atoms, no dial); otherwise it falls back to the searcher.
	recommendTool, err := functiontool.New(functiontool.Config{
		Name:        "recommend_atoms_for_learner",
		Description: "Retrieve 3-5 ranked atom recommendations for this learner, biased by persona + topic_hint.",
	}, newRecommendHandler(searcher))
	if err != nil {
		log.Fatalf("functiontool.New(recommend): %v", err)
	}

	allTools := []tool.Tool{recommendTool}

	// Per-turn instruction composition (Iter 3.5, wired 2026-06-05): the agent
	// recomposes its instruction from the live session state every turn via
	// InstructionProvider (tenant_id / user_gcid / learner_persona / topic_hint
	// the consumption Recommend client wrote at async_create_session).
	// InstructionProvider takes precedence over the static Instruction field
	// (llmagent.go:209). The static Instruction stays as a deploy-time / no-state
	// fallback, but it now uses the "<any>" sentinel (composer default) rather
	// than the "<filled at runtime>" placeholder that previously reached the
	// model and made it refuse the required-topic_hint tool → no picks.
	staticCtx := recoagent.TaskContext{}
	rootAgent, err := llmagent.New(llmagent.Config{
		Name:                "content_recommender",
		Model:               model,
		Description:         "Per-learner content recommender (P1 single-agent ReAct). Phyllis Step 8 daily-dose 40% slot.",
		Instruction:         recoagent.ComposeRecommenderInstruction("curious-explorer", staticCtx),
		InstructionProvider: recoagent.NewInstructionProvider(),
		Tools:               allTools,
	})
	if err != nil {
		log.Fatalf("llmagent.New: %v", err)
	}

	loader := adkagent.NewSingleLoader(rootAgent)

	// Mana gate + metering is now the gateway's job (ADR-177): the gateway
	// debits content_recommendation once per turn (action_code set on the model
	// client above). The agent-side manaplugin debit is retired; tenant/gcid
	// attribution is handled by tenantPropP (built above).

	// NOTE: the tieredmodelplugin (ADR-149 mana × growth LLM matrix) is
	// DELIBERATELY NOT registered here (mirrors qgen CR 2026-06-01, user
	// directive). Mana is a token-budget QUOTA system (gateway-side metering,
	// above) — it must NOT dictate which LLM model is used. Model selection is
	// AGENT-DRIVEN via the embedded agentconfig YAML (recommend = HIGH tier =
	// gemini-3.1-pro-preview → gemini-2.5-pro). The old plugin's 2.5-only
	// matrix (lite/flash/pro) silently pinned every recommender call to
	// gemini-2.5-flash-lite (empty mana_tier → tierBasic), clobbering the
	// config's HIGH-tier primary. See
	// feedback_mana_is_quota_not_model_selector + the CR tracker.

	// Termination plugin — emits chora.ai_kernel.agent.terminated.v1 on
	// every entry-boundary exit. MaxIterations cap = 3 (Recommender is a
	// ReAct single-agent: retrieve → reflect → respond).
	terminationP, err := terminationplugin.New(terminationplugin.Config{
		Publisher:     &terminationplugin.LoggingPublisher{},
		AgentID:       "content_recommender",
		Runtime:       "AGENT_EXECUTION_RUNTIME_ADK_GO",
		CrewKind:      crewKind,
		CrewPattern:   "P1_SINGLE_AGENT_REACT",
		MaxIterations: 3,
	})
	if err != nil {
		log.Fatalf("terminationplugin.New: %v", err)
	}

	// SessionService — REQUIRED for the launcher (web mode fails to start
	// without it). The crew is stateless w.r.t. storage: the in-memory session
	// service holds each recommend turn's session (tenant_id / user_gcid /
	// learner_persona / topic_hint / atom_candidates) for the process
	// lifetime. In-memory sessions require replicas=1 (a recommend turn's
	// session must hit the same instance).
	sessionService := session.InMemoryService()

	// Plugin order: tenantPropP (per-request tenant/gcid stamping onto the
	// gateway Invoke) → terminationplugin (LIFO observes final error state).
	// The tieredmodelplugin is intentionally absent — model selection is
	// config-driven, not mana-driven (see NOTE above).
	pluginConfig := runner.PluginConfig{
		Plugins: []*plugin.Plugin{tenantPropP, terminationP},
	}

	// Standard ADK REST API server — the local replacement for the
	// hosted-agent runtime adapter. This is the same adkrest handler the
	// ADK web `api` sublauncher wraps, wired directly so the process pulls
	// in no cloud telemetry detector. Sessions are addressed by the
	// app_name in the request URL
	// (/api/apps/{app}/users/{user}/sessions/{id}), so no engine-scoped
	// app_name argument is needed — every create/run handler is built from
	// the request path.
	restServer, err := adkrest.NewServer(adkrest.ServerConfig{
		SessionService:  sessionService,
		AgentLoader:     loader,
		SSEWriteTimeout: 120 * time.Second,
		PluginConfig:    pluginConfig,
	})
	if err != nil {
		log.Fatalf("recommender: adkrest.NewServer: %v", err)
	}

	router := mux.NewRouter().StrictSlash(true)
	router.Methods("GET", "POST", "DELETE", "OPTIONS").
		PathPrefix("/api").
		Handler(http.StripPrefix("/api", restServer))

	port := strings.TrimSpace(envOr("PORT", "8080"))
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Serve until the HTTP server fails or a SIGINT/SIGTERM arrives, then
	// drain in-flight recommend turns (sessions are in-memory, so there is
	// no durable state to flush).
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	slog.Info("recommender: serving ADK REST API", "addr", srv.Addr, "path_prefix", "/api")
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("recommender: http server: %v", err)
		}
	case <-sigCh:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("recommender: shutdown", "err", err)
		}
	}
}

// newRecommendHandler returns the ADK tool handler bound to a Searcher. It reads
// the RLS-bearing tenant_id + learner gcid from the tool's session state (the
// consumption Recommend client stamps them at async_create_session) and calls
// the searcher — REAL atoms in prod, deterministic stub in sandbox.
func newRecommendHandler(searcher tools.Searcher) func(tool.Context, tools.RecommendAtomsRequest) (tools.RecommendAtomsResponse, error) {
	return func(tctx tool.Context, in tools.RecommendAtomsRequest) (tools.RecommendAtomsResponse, error) {
		return tools.RecommendWithSearcher(tctx, tctx.ReadonlyState(), searcher, in)
	}
}

// buildSearcher resolves the recommend content source from the consumption
// endpoint. stub://… ⇒ StubSearcher; otherwise dial the gRPC endpoint (plaintext
// — the crew is sidecar-less and consumption admits in-mesh callers on :9090)
// and wrap the generated ConsumptionClient.
func buildSearcher(_ context.Context, endpoint string) (tools.Searcher, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, errMissingConsumptionEndpoint
	}
	if strings.HasPrefix(endpoint, "stub://") {
		slog.Warn("recommender: content searcher = STUB (CONSUMPTION_GRPC_ENDPOINT=stub://; atom-stub-* picks — NOT real RAG)",
			"consumption_endpoint", endpoint)
		return tools.StubSearcher{}, nil
	}
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	slog.Info("recommender: content searcher = chora-consumption gRPC (REAL atom_index RAG)",
		"consumption_endpoint", endpoint)
	return consumptionrag.NewSearcher(consumptionv1.NewConsumptionClient(conn)), nil
}

// errMissingConsumptionEndpoint surfaces the no-inline-config refusal.
var errMissingConsumptionEndpoint = errConst("CONSUMPTION_GRPC_ENDPOINT not set; refusing inline default per feedback_no_inline_config")

type errConst string

func (e errConst) Error() string { return string(e) }

// crewSurface is the ADR-254 D7 surface value of this crew.
const crewSurface = "content_recommender"

// recommenderGatewayConfig is the gateway client identity of the content_recommender call. Surface is
// the crew id (ADR-254 D7): the D7 gateway refuses an unstamped Invoke
// (FAILED_PRECONDITION surface_unstamped), so it is set here, once, and
// asserted by a test; everything else is what the call always sent.
func recommenderGatewayConfig(crewKind string, gatewayEndpoint string, gatewayGCID string, gatewayTenantID string, modelName string, recommendCfg agentconfig.SubAgentConfig, gatewayAudience string, insecure bool) modelgatewayclient.Config {
	return modelgatewayclient.Config{
		Endpoint:         gatewayEndpoint,
		LogicalModelID:   modelName,
		FallbackModelIDs: recommendCfg.FallbackModels,
		AgentID:          "content_recommender",
		CrewKind:         crewKind,
		TenantID:         gatewayTenantID,
		GCID:             gatewayGCID,
		Audience:         gatewayAudience,
		// content_recommendation meters once per turn-initiating Invoke. The
		// authoritative cost lives in chora_identity.mana_action_pricing; until
		// a row exists the gateway logs unpriced→un-metered (serves).
		ActionCode: "content_recommendation",
		Surface:    crewSurface,
		Insecure:   insecure,
	}
}

// gatewayInsecure reports the CHORA_GATEWAY_INSECURE local-dev override
// (plaintext gRPC to a local gateway, no token). Production MUST leave it
// unset so the client uses TLS + CHORA_GATEWAY_TOKEN.
func gatewayInsecure() bool {
	v := strings.TrimSpace(os.Getenv("CHORA_GATEWAY_INSECURE"))
	return v == "1" || strings.EqualFold(v, "true")
}
