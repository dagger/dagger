package workspace

import (
	"fmt"

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
	Config map[string]string `toml:"config"`
	Alias  bool              `toml:"alias"`
}

// ParseConfig parses a config.toml file from raw bytes.
func ParseConfig(data []byte) (*Config, error) {
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config.toml: %w", err)
	}
	return &cfg, nil
}
