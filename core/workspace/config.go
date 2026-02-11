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

// ConstructorArgHint captures information about a module constructor argument
// for generating commented-out hint lines in config.toml.
type ConstructorArgHint struct {
	Name         string // lowerCamelCase arg name (used as config key)
	TypeLabel    string // e.g. "string", "Container", "MyType (not configurable via config)"
	ExampleValue string // TOML-formatted example value, e.g. `""`, `false`, `"alpine:latest"`
}

// SerializeConfigWithHints serializes a Config to TOML bytes with commented-out
// hint lines for constructor args inserted after each module's entries.
func SerializeConfigWithHints(cfg *Config, hints map[string][]ConstructorArgHint) []byte {
	tomlStr := string(SerializeConfig(cfg))
	if len(hints) > 0 {
		tomlStr = insertHintComments(tomlStr, cfg, hints)
	}
	return []byte(tomlStr)
}

// insertHintComments injects commented-out config hint lines into the serialized
// TOML string, positioned after each module's last real key.
func insertHintComments(tomlStr string, cfg *Config, hints map[string][]ConstructorArgHint) string {
	// Sort module names for deterministic output
	moduleNames := make([]string, 0, len(hints))
	for name := range hints {
		moduleNames = append(moduleNames, name)
	}
	sort.Strings(moduleNames)

	lines := strings.Split(tomlStr, "\n")

	for _, moduleName := range moduleNames {
		argHints := hints[moduleName]
		if len(argHints) == 0 {
			continue
		}

		// Build the set of config keys already set for this module
		existingConfigKeys := make(map[string]bool)
		if entry, ok := cfg.Modules[moduleName]; ok && entry.Config != nil {
			for k := range entry.Config {
				existingConfigKeys[strings.ToLower(k)] = true
			}
		}

		// Build comment lines, skipping args that already have a config entry
		var commentLines []string
		for _, hint := range argHints {
			if existingConfigKeys[strings.ToLower(hint.Name)] {
				continue
			}
			commentLines = append(commentLines,
				fmt.Sprintf("# %s.config.%s = %s # %s", moduleName, hint.Name, hint.ExampleValue, hint.TypeLabel))
		}
		if len(commentLines) == 0 {
			continue
		}

		// Find the last line belonging to this module (starts with "moduleName.")
		prefix := moduleName + "."
		lastIdx := -1
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, prefix) {
				lastIdx = i
			}
		}
		if lastIdx == -1 {
			continue
		}

		// Insert comment lines after the last module line
		newLines := make([]string, 0, len(lines)+len(commentLines))
		newLines = append(newLines, lines[:lastIdx+1]...)
		newLines = append(newLines, commentLines...)
		newLines = append(newLines, lines[lastIdx+1:]...)
		lines = newLines
	}

	return strings.Join(lines, "\n")
}
