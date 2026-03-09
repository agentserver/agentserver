package sandboxproxy

import "os"

// Config holds sandbox-proxy configuration loaded from environment variables.
type Config struct {
	DatabaseURL             string
	ListenAddr              string
	BaseDomain              string
	OpencodeAssetDomain     string
	OpencodeSubdomainPrefix string
	OpenclawSubdomainPrefix string
}

// LoadConfigFromEnv reads configuration from environment variables.
func LoadConfigFromEnv() Config {
	cfg := Config{
		DatabaseURL:             os.Getenv("DATABASE_URL"),
		ListenAddr:              os.Getenv("LISTEN_ADDR"),
		BaseDomain:              os.Getenv("BASE_DOMAIN"),
		OpencodeAssetDomain:     os.Getenv("OPENCODE_ASSET_DOMAIN"),
		OpencodeSubdomainPrefix: os.Getenv("OPENCODE_SUBDOMAIN_PREFIX"),
		OpenclawSubdomainPrefix: os.Getenv("OPENCLAW_SUBDOMAIN_PREFIX"),
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8082"
	}
	if cfg.OpencodeSubdomainPrefix == "" {
		cfg.OpencodeSubdomainPrefix = "code"
	}
	if cfg.OpenclawSubdomainPrefix == "" {
		cfg.OpenclawSubdomainPrefix = "claw"
	}
	if cfg.OpencodeAssetDomain == "" && cfg.BaseDomain != "" {
		cfg.OpencodeAssetDomain = "opencodeapp." + cfg.BaseDomain
	}
	return cfg
}
