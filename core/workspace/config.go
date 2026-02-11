package workspace

import (
	"fmt"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml"
)

// Config represents a parsed .dagger/config.toml workspace configuration.
type Config struct {
	Modules map[string]ModuleEntry `toml:"modules"`
	Ignore  []string               `toml:"ignore"`
}

// ModuleEntry represents a single module entry in the workspace config.
type ModuleEntry struct {
	Source string            `toml:"source"`
	Config map[string]string `toml:"config,omitempty"`
	Alias  bool              `toml:"alias,omitempty"`
}

// ParseConfig parses a config.toml file from raw bytes.
func ParseConfig(data []byte) (*Config, error) {
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config.toml: %w", err)
	}
	return &cfg, nil
}

// SerializeConfig serializes a Config to TOML bytes.
// Uses hand-built TOML with dotted-key format matching modules/migrate/toml.go.
func SerializeConfig(cfg *Config) []byte {
	var b strings.Builder

	if len(cfg.Ignore) > 0 {
		b.WriteString("ignore = [")
		for i, pat := range cfg.Ignore {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q", pat)
		}
		b.WriteString("]\n\n")
	}

	if len(cfg.Modules) > 0 {
		// Sort module names for deterministic output
		names := make([]string, 0, len(cfg.Modules))
		for name := range cfg.Modules {
			names = append(names, name)
		}
		sort.Strings(names)

		b.WriteString("[modules]\n")
		for _, name := range names {
			entry := cfg.Modules[name]
			fmt.Fprintf(&b, "%s.source = %q\n", name, entry.Source)
			if entry.Alias {
				fmt.Fprintf(&b, "%s.alias = true\n", name)
			}
			if len(entry.Config) > 0 {
				// Sort config keys for deterministic output
				keys := make([]string, 0, len(entry.Config))
				for k := range entry.Config {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					fmt.Fprintf(&b, "%s.config.%s = %q\n", name, k, entry.Config[k])
				}
			}
		}
	}

	return []byte(b.String())
}
