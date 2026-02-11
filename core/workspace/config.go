package workspace

import (
	"fmt"
	"sort"
	"strings"

	neontoml "github.com/neongreen/mono/lib/toml"
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

// SerializeConfigWithHints serializes a Config to TOML bytes, preserving existing
// comments from existingTOML and inserting commented-out hint lines for constructor args.
func SerializeConfigWithHints(cfg *Config, existingTOML []byte, hints map[string][]ConstructorArgHint) ([]byte, error) {
	// Parse existing TOML or create fresh document
	var doc *neontoml.Document
	var err error
	if len(existingTOML) > 0 {
		doc, err = neontoml.Parse(existingTOML)
		if err != nil {
			return nil, fmt.Errorf("parsing existing config: %w", err)
		}
	} else {
		doc, err = neontoml.ParseString("[modules]\n")
		if err != nil {
			return nil, fmt.Errorf("creating fresh document: %w", err)
		}
	}

	// Set module entries via the comment-preserving library
	names := make([]string, 0, len(cfg.Modules))
	for name := range cfg.Modules {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		entry := cfg.Modules[name]
		if err := doc.Set(fmt.Sprintf("modules.%s.source", name), entry.Source); err != nil {
			return nil, fmt.Errorf("setting modules.%s.source: %w", name, err)
		}
		if entry.Alias {
			if err := doc.Set(fmt.Sprintf("modules.%s.alias", name), true); err != nil {
				return nil, fmt.Errorf("setting modules.%s.alias: %w", name, err)
			}
		}
		if len(entry.Config) > 0 {
			keys := make([]string, 0, len(entry.Config))
			for k := range entry.Config {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if err := doc.Set(fmt.Sprintf("modules.%s.config.%s", name, k), entry.Config[k]); err != nil {
					return nil, fmt.Errorf("setting modules.%s.config.%s: %w", name, k, err)
				}
			}
		}
	}

	// Serialize, then insert hint comments
	tomlStr := doc.String()
	if len(hints) > 0 {
		tomlStr = insertHintComments(tomlStr, cfg, hints)
	}

	return []byte(tomlStr), nil
}

// insertHintComments injects commented-out config hint lines into the serialized
// TOML string, positioned after each module's last real key.
// Handles both dotted-key format (moduleName.source = ...) and section-header
// format ([modules.moduleName] / source = ...).
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

		// Find insertion point and determine whether keys are under a section header.
		// If under [modules.name], comment keys should be relative (config.x = ...),
		// so that uncommenting produces the correct fully-qualified path.
		// If under [modules] with dotted keys, use fully-qualified (name.config.x = ...).
		insertIdx, useRelativeKeys := findModuleInsertionPoint(lines, moduleName)
		if insertIdx == -1 {
			continue
		}

		// Build comment lines, skipping args that already have a config entry
		var commentLines []string
		for _, hint := range argHints {
			if existingConfigKeys[strings.ToLower(hint.Name)] {
				continue
			}
			if useRelativeKeys {
				commentLines = append(commentLines,
					fmt.Sprintf("# config.%s = %s # %s", hint.Name, hint.ExampleValue, hint.TypeLabel))
			} else {
				commentLines = append(commentLines,
					fmt.Sprintf("# %s.config.%s = %s # %s", moduleName, hint.Name, hint.ExampleValue, hint.TypeLabel))
			}
		}
		if len(commentLines) == 0 {
			continue
		}

		// Insert comment lines after the insertion point
		newLines := make([]string, 0, len(lines)+len(commentLines))
		newLines = append(newLines, lines[:insertIdx+1]...)
		newLines = append(newLines, commentLines...)
		newLines = append(newLines, lines[insertIdx+1:]...)
		lines = newLines
	}

	return strings.Join(lines, "\n")
}

// findModuleInsertionPoint locates where to insert hint comments for a module.
// Returns the line index to insert after, and whether the module uses a section
// header (meaning comment keys should be relative, not fully qualified).
func findModuleInsertionPoint(lines []string, moduleName string) (insertIdx int, useRelativeKeys bool) {
	// Strategy 1: dotted keys under [modules] — lines starting with "moduleName."
	dottedPrefix := moduleName + "."
	lastDottedIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, dottedPrefix) {
			lastDottedIdx = i
		}
	}
	if lastDottedIdx != -1 {
		return lastDottedIdx, false
	}

	// Strategy 2: section header [modules.moduleName]
	sectionHeader := "[modules." + moduleName + "]"
	inSection := false
	lastSectionIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == sectionHeader {
			inSection = true
			lastSectionIdx = i
			continue
		}
		if inSection {
			// Another section header ends this section
			if strings.HasPrefix(trimmed, "[") {
				break
			}
			// Track last non-empty, non-comment line as the insertion point
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				lastSectionIdx = i
			}
		}
	}
	if lastSectionIdx != -1 {
		return lastSectionIdx, true
	}

	return -1, false
}
