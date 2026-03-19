package llmconfig

import (
	"os"
	"strconv"
)

// envMapping maps environment variable names to (provider, field) pairs.
var envMapping = []struct {
	envVar   string
	provider string
	setter   func(p *Provider, val string)
}{
	{"ANTHROPIC_API_KEY", "anthropic", func(p *Provider, v string) { p.APIKey = v }},
	{"ANTHROPIC_BASE_URL", "anthropic", func(p *Provider, v string) { p.BaseURL = v }},
	{"ANTHROPIC_MODEL", "anthropic", func(p *Provider, v string) { p.Model = v }},
	{"OPENAI_API_KEY", "openai", func(p *Provider, v string) { p.APIKey = v }},
	{"OPENAI_BASE_URL", "openai", func(p *Provider, v string) { p.BaseURL = v }},
	{"OPENAI_MODEL", "openai", func(p *Provider, v string) { p.Model = v }},
	{"OPENAI_AZURE_VERSION", "openai", func(p *Provider, v string) { p.AzureVersion = v }},
	{"GEMINI_API_KEY", "google", func(p *Provider, v string) { p.APIKey = v }},
	{"GEMINI_BASE_URL", "google", func(p *Provider, v string) { p.BaseURL = v }},
	{"GEMINI_MODEL", "google", func(p *Provider, v string) { p.Model = v }},
}

// MergeEnvVars reads LLM-related environment variables and merges them
// into cfg, overriding any values from the config file.  If cfg is nil
// a new Config is allocated.
func MergeEnvVars(cfg *Config) *Config {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.LLM.Providers == nil {
		cfg.LLM.Providers = make(map[string]Provider)
	}

	for _, m := range envMapping {
		val := os.Getenv(m.envVar)
		if val == "" {
			continue
		}
		p := cfg.LLM.Providers[m.provider]
		m.setter(&p, val)
		cfg.LLM.Providers[m.provider] = p
	}

	// OPENAI_DISABLE_STREAMING is a bool, handle separately.
	if v := os.Getenv("OPENAI_DISABLE_STREAMING"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			p := cfg.LLM.Providers["openai"]
			p.DisableStreaming = b
			cfg.LLM.Providers["openai"] = p
		}
	}

	// DAGGER_MODEL overrides the default model for all providers.
	if v := os.Getenv("DAGGER_MODEL"); v != "" {
		cfg.LLM.DefaultModel = v
	}

	return cfg
}
