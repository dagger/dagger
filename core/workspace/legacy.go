package workspace

import (
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/core/modules"
)

// LegacyToolchain represents a toolchain extracted from a legacy dagger.json,
// with constructor arg defaults already resolved from customizations.
type LegacyToolchain struct {
	Name           string
	Source         string
	Pin            string
	ConfigDefaults map[string]any
	Customizations []*modules.ModuleConfigArgument
}

// LegacyBlueprint represents a blueprint extracted from a legacy dagger.json.
type LegacyBlueprint struct {
	Name   string
	Source string
	Pin    string
}

// ParseLegacyBlueprint parses a legacy dagger.json and extracts its blueprint.
// Returns nil if no blueprint is present.
func ParseLegacyBlueprint(data []byte) (*LegacyBlueprint, error) {
	cfg, err := parseLegacyConfig(data)
	if err != nil {
		return nil, err
	}
	if cfg.Blueprint == nil {
		return nil, nil
	}
	return &LegacyBlueprint{
		Name:   cfg.Blueprint.Name,
		Source: cfg.Blueprint.Source,
		Pin:    cfg.Blueprint.Pin,
	}, nil
}

// ParseLegacyToolchains parses a legacy dagger.json and extracts its toolchains
// with their constructor arg defaults. Returns nil if no toolchains are present.
func ParseLegacyToolchains(data []byte) ([]LegacyToolchain, error) {
	cfg, err := parseLegacyConfig(data)
	if err != nil {
		return nil, err
	}
	if len(cfg.Toolchains) == 0 {
		return nil, nil
	}
	result := make([]LegacyToolchain, len(cfg.Toolchains))
	for i, tc := range cfg.Toolchains {
		if tc == nil {
			continue
		}
		result[i] = LegacyToolchain{
			Name:           tc.Name,
			Source:         tc.Source,
			Pin:            tc.Pin,
			ConfigDefaults: extractConfigDefaults(tc.Customizations),
			Customizations: cloneCustomizations(tc.Customizations),
		}
	}
	return result, nil
}

// parseLegacyConfig parses a legacy dagger.json into the internal representation.
func parseLegacyConfig(data []byte) (*modules.ModuleConfig, error) {
	var cfg modules.ModuleConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing legacy config: %w", err)
	}
	return &cfg, nil
}

// extractConfigDefaults extracts constructor arg defaults from legacy customizations.
// Only constructor-level customizations with a non-empty Default value are included.
func extractConfigDefaults(customizations []*modules.ModuleConfigArgument) map[string]any {
	config := make(map[string]any)
	for _, cust := range customizations {
		if cust == nil || len(cust.Function) != 0 || cust.Default == nil {
			continue
		}
		// Skip empty string defaults
		if s, ok := cust.Default.(string); ok && s == "" {
			continue
		}
		config[cust.Argument] = cust.Default
	}
	if len(config) == 0 {
		return nil
	}
	return config
}

func cloneCustomizations(customizations []*modules.ModuleConfigArgument) []*modules.ModuleConfigArgument {
	result := make([]*modules.ModuleConfigArgument, 0, len(customizations))
	for _, cust := range customizations {
		if cust == nil {
			continue
		}
		result = append(result, &modules.ModuleConfigArgument{
			Function:       append([]string(nil), cust.Function...),
			Argument:       cust.Argument,
			Default:        cust.Default,
			DefaultPath:    cust.DefaultPath,
			DefaultAddress: cust.DefaultAddress,
			Ignore:         append([]string(nil), cust.Ignore...),
		})
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
