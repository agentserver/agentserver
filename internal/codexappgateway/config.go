package codexappgateway

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// S3Config matches the shape used by internal/ccbroker/workspace/s3store.go;
// dedup into a shared storage package is a known follow-up. Until then,
// keep validation here in sync with ccbroker's.
type S3Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	PathStyle       bool
}

type ServeConfig struct {
	InboundHMACSecret         []byte
	S3                        S3Config
	TmpRoot                   string
	IdleShutdown              time.Duration
	ExecGatewayWSURL          string
	ExecGatewayInternalURL    string
	ExecGatewayInternalSecret string
	CapTokenHMACSecret        []byte
	CapTokenTTL               time.Duration
	// BrokerPoolIdleTTL is how long a pooled per-workspace broker.Conn
	// (WS to the codex app-server subprocess) may sit without any frame
	// flowing in either direction before the pool reaps it. Must be
	// long enough to cover the silent stretch of a single in-flight
	// turn — e.g. a high-reasoning gpt-5 turn can produce no frames for
	// many minutes between tool calls. Should be ≥ IdleShutdown so we
	// never reap a pool conn while its underlying subprocess is still
	// alive and serving the same workspace. Override via
	// CXG_BROKER_POOL_IDLE_TTL.
	BrokerPoolIdleTTL time.Duration
	LogLevel          slog.Level

	// Model provider config — written verbatim into each per-thread
	// config.toml. The codex subprocess reads ModelProviderEnvKey from its
	// own env (forwarded from CodexAPIKey here) to authenticate to the
	// LLM gateway (typically llmproxy in-cluster).
	ModelProvider        string
	Model                string
	ReasoningEffort      string // optional; writes `model_reasoning_effort` into per-thread config.toml
	ModelProviderBaseURL string
	ModelProviderEnvKey  string
	ModelProviderWireAPI string
	CodexAPIKey          string

	// ProjectTrustedPaths is the list of paths marked `trust_level = "trusted"`
	// in config.toml. Without at least one, codex refuses to run shell-side
	// operations on the project root.
	ProjectTrustedPaths []string

	// AgentserverInternalURL is the http base for codex token verification
	// (e.g. "http://release-agentserver.namespace.svc:8080"). Required when
	// the gateway uses RemoteVerifier (production default).
	AgentserverInternalURL string

	// AgentserverInternalSecret matches the agentserver's INTERNAL_API_SECRET
	// env. Sent in every verify request as X-Internal-Secret.
	AgentserverInternalSecret string

	// ListenAddr is the gateway's HTTP listen address (e.g. ":8086"). Used
	// to derive the loopback URL env-mcp uses for /internal/connected.
	// Set by main.go before NewServer; tests may leave it empty (codexhome
	// then emits no AppGatewayInternalURL and env-mcp won't be able to
	// list environments, which is fine for tests that don't exercise
	// list_environments).
	ListenAddr string

	// Scheduler config — when AgentserverInternalURL is empty the scheduler is
	// disabled (no point polling without an agentserver to call).
	SchedulerTickInterval  time.Duration // CXG_SCHED_TICK         (default 15s)
	SchedulerLeaseSeconds  int           // CXG_SCHED_LEASE_SECONDS (default 1800)
	SchedulerConcurrency   int           // CXG_SCHED_CONCURRENCY   (default 4)
	ImbridgeBaseURL        string        // CXG_IMBRIDGE_BASE_URL   (e.g. http://imbridgesvc:6090)
	ImbridgeInternalSecret string        // CXG_IMBRIDGE_SECRET     (same value as imbridge's INTERNAL_API_SECRET)
}

func LoadServeConfigFromEnv() (ServeConfig, error) {
	cfg := ServeConfig{
		TmpRoot: envOr("CXG_TMP_ROOT", "/tmp/codex-app-gateway"),
		IdleShutdown: 30 * time.Minute,
		// CapTokenTTL bounds the cap-token's validity. The token is
		// minted at codex app-server spawn and re-used by env-mcp for
		// the subprocess's whole lifetime. IdleShutdown is 30 min, so
		// 24h is comfortably longer than any realistic session — keeps
		// long-running codex --remote TUIs from hitting 401 mid-call
		// without giving up the bound altogether.
		CapTokenTTL:       24 * time.Hour,
		BrokerPoolIdleTTL: 30 * time.Minute,
		LogLevel:          slog.LevelInfo,
		S3: S3Config{
			Endpoint:        os.Getenv("CXG_S3_ENDPOINT"),
			Region:          envOr("CXG_S3_REGION", "us-east-1"),
			Bucket:          os.Getenv("CXG_S3_BUCKET"),
			AccessKeyID:     os.Getenv("CXG_S3_ACCESS_KEY_ID"),
			SecretAccessKey: os.Getenv("CXG_S3_SECRET_ACCESS_KEY"),
			PathStyle:       strings.EqualFold(os.Getenv("CXG_S3_PATH_STYLE"), "true"),
		},
		InboundHMACSecret:         []byte(os.Getenv("CXG_INBOUND_HMAC_SECRET")),
		ExecGatewayWSURL:          os.Getenv("CXG_EXEC_GATEWAY_URL"),
		ExecGatewayInternalURL:    os.Getenv("CXG_EXEC_GATEWAY_INTERNAL_URL"),
		ExecGatewayInternalSecret: os.Getenv("CXG_EXEC_GATEWAY_INTERNAL_SECRET"),
		CapTokenHMACSecret:        []byte(os.Getenv("CXG_CAPTOKEN_HMAC_SECRET")),
		ModelProvider:             envOr("CXG_MODEL_PROVIDER", "modelserver"),
		Model:                     envOr("CXG_MODEL", "gpt-5.5"),
		ReasoningEffort:           os.Getenv("CXG_REASONING_EFFORT"),
		ModelProviderBaseURL:      envOr("CXG_MODEL_PROVIDER_BASE_URL", "http://llmproxy:8085/v1"),
		ModelProviderEnvKey:       envOr("CXG_MODEL_PROVIDER_ENV_KEY", "CODEX_API_KEY"),
		ModelProviderWireAPI:      envOr("CXG_MODEL_PROVIDER_WIRE_API", "responses"),
		CodexAPIKey:               os.Getenv("CXG_CODEX_API_KEY"),
	}
	if v := os.Getenv("CXG_PROJECT_TRUSTED_PATHS"); v != "" {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cfg.ProjectTrustedPaths = append(cfg.ProjectTrustedPaths, p)
			}
		}
	}
	cfg.AgentserverInternalURL = os.Getenv("CXG_AGENTSERVER_INTERNAL_URL")
	cfg.AgentserverInternalSecret = os.Getenv("CXG_AGENTSERVER_INTERNAL_SECRET")
	if cfg.S3.Endpoint == "" {
		return cfg, fmt.Errorf("CXG_S3_ENDPOINT is required")
	}
	if u, err := url.Parse(cfg.S3.Endpoint); err != nil {
		return cfg, fmt.Errorf("CXG_S3_ENDPOINT not a valid URL: %w", err)
	} else if u.Scheme != "http" && u.Scheme != "https" {
		return cfg, fmt.Errorf("CXG_S3_ENDPOINT must use http:// or https:// scheme, got %q", cfg.S3.Endpoint)
	}
	if cfg.S3.Bucket == "" {
		return cfg, fmt.Errorf("CXG_S3_BUCKET is required")
	}
	if cfg.ExecGatewayWSURL == "" {
		return cfg, fmt.Errorf("CXG_EXEC_GATEWAY_URL is required")
	}
	if cfg.ExecGatewayInternalURL == "" {
		return cfg, fmt.Errorf("CXG_EXEC_GATEWAY_INTERNAL_URL is required")
	}
	if cfg.ExecGatewayInternalSecret == "" {
		return cfg, fmt.Errorf("CXG_EXEC_GATEWAY_INTERNAL_SECRET is required")
	}
	if len(cfg.CapTokenHMACSecret) == 0 {
		return cfg, fmt.Errorf("CXG_CAPTOKEN_HMAC_SECRET is required")
	}
	if cfg.AgentserverInternalURL == "" {
		return cfg, fmt.Errorf("CXG_AGENTSERVER_INTERNAL_URL is required")
	}
	if cfg.AgentserverInternalSecret == "" {
		return cfg, fmt.Errorf("CXG_AGENTSERVER_INTERNAL_SECRET is required")
	}
	if v := os.Getenv("CXG_IDLE_SHUTDOWN"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return cfg, fmt.Errorf("parse CXG_IDLE_SHUTDOWN: %w", err)
		}
		cfg.IdleShutdown = d
	}
	if v := os.Getenv("CXG_CAPTOKEN_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return cfg, fmt.Errorf("parse CXG_CAPTOKEN_TTL: %w", err)
		}
		cfg.CapTokenTTL = d
	}
	if v := os.Getenv("CXG_BROKER_POOL_IDLE_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return cfg, fmt.Errorf("parse CXG_BROKER_POOL_IDLE_TTL: %w", err)
		}
		cfg.BrokerPoolIdleTTL = d
	}
	if v := strings.ToLower(os.Getenv("CXG_LOG_LEVEL")); v != "" {
		switch v {
		case "debug":
			cfg.LogLevel = slog.LevelDebug
		case "warn":
			cfg.LogLevel = slog.LevelWarn
		case "error":
			cfg.LogLevel = slog.LevelError
		}
	}
	// Scheduler config — defaults applied here; also enforced in scheduler.New
	// but we want them visible in config dumps.
	cfg.SchedulerTickInterval = 15 * time.Second
	cfg.SchedulerLeaseSeconds = 1800
	cfg.SchedulerConcurrency = 4
	if v := os.Getenv("CXG_SCHED_TICK"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			cfg.SchedulerTickInterval = 15 * time.Second
		} else {
			cfg.SchedulerTickInterval = d
		}
	}
	if v := os.Getenv("CXG_SCHED_LEASE_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			cfg.SchedulerLeaseSeconds = n
		}
	}
	if v := os.Getenv("CXG_SCHED_CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			cfg.SchedulerConcurrency = n
		}
	}
	cfg.ImbridgeBaseURL = os.Getenv("CXG_IMBRIDGE_BASE_URL")
	cfg.ImbridgeInternalSecret = os.Getenv("CXG_IMBRIDGE_SECRET")
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
