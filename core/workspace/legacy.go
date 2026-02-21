package workspace

import (
	"encoding/json"
	"fmt"
)

// LegacyToolchain represents a toolchain extracted from a legacy dagger.json,
// with constructor arg defaults already resolved from customizations.
type LegacyToolchain struct {
	Name           string
	Source         string
	Pin            string
	ConfigDefaults map[string]any
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
		result[i] = LegacyToolchain{
			Name:           tc.Name,
			Source:         tc.Source,
			Pin:            tc.Pin,
			ConfigDefaults: extractConfigDefaults(tc.Customizations),
		}
	}
	return result, nil
}

// parseLegacyConfig parses a legacy dagger.json into the internal representation.
func parseLegacyConfig(data []byte) (*legacyConfig, error) {
	var cfg legacyConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing legacy config: %w", err)
	}
	// Normalize: if SDK is set but Source isn't, Source was implicitly "."
	if cfg.SDK != nil && cfg.SDK.Source != "" && cfg.Source == "" {
		cfg.Source = "."
	}
	return &cfg, nil
}

// extractConfigDefaults extracts constructor arg defaults from legacy customizations.
// Only constructor-level customizations with a non-empty Default value are included.
func extractConfigDefaults(customizations []*legacyCustomization) map[string]any {
	config := make(map[string]any)
	for _, cust := range customizations {
		if cust.IsConstructor() && cust.Default != "" {
			config[cust.Argument] = cust.Default
		}
	}
	if len(config) == 0 {
		return nil
	}
	return config
}

// legacyConfig is the dagger.json config format used before the workspace model.
type legacyConfig struct {
	Name          string              `json:"name"`
	EngineVersion string              `json:"engineVersion"`
	SDK           *legacySDK          `json:"sdk,omitempty"`
	Source        string              `json:"source,omitempty"`
	Blueprint     *legacyDependency   `json:"blueprint,omitempty"`
	Toolchains    []*legacyDependency `json:"toolchains,omitempty"`
	Dependencies  []*legacyDependency `json:"dependencies,omitempty"`
	Include       []string            `json:"include,omitempty"`
	Codegen       json.RawMessage     `json:"codegen,omitempty"`
	Clients       json.RawMessage     `json:"clients,omitempty"`
}

// legacySDK represents the sdk field in dagger.json.
// Supports both string and object forms for backwards compat.
type legacySDK struct {
	Source string         `json:"source"`
	Config map[string]any `json:"config,omitempty"`
}

func (sdk *legacySDK) UnmarshalJSON(data []byte) error {
	if sdk == nil {
		return fmt.Errorf("cannot unmarshal into nil legacySDK")
	}
	if len(data) == 0 {
		sdk.Source = ""
		return nil
	}
	// Legacy format: sdk was a string like "go"
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("unmarshal sdk as string: %w", err)
		}
		*sdk = legacySDK{Source: s}
		return nil
	}
	type alias legacySDK
	var tmp alias
	if err := json.Unmarshal(data, &tmp); err != nil {
		return fmt.Errorf("unmarshal sdk as struct: %w", err)
	}
	*sdk = legacySDK(tmp)
	return nil
}

// legacyDependency represents a dependency/toolchain entry in dagger.json.
type legacyDependency struct {
	Name           string                 `json:"name"`
	Source         string                 `json:"source"`
	Pin            string                 `json:"pin,omitempty"`
	Customizations []*legacyCustomization `json:"customizations,omitempty"`
	IgnoreChecks   []string               `json:"ignoreChecks,omitempty"`
}

func (dep *legacyDependency) UnmarshalJSON(data []byte) error {
	if dep == nil {
		return fmt.Errorf("cannot unmarshal into nil legacyDependency")
	}
	if len(data) == 0 {
		dep.Source = ""
		return nil
	}
	// Legacy format: deps were just strings like "./path"
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("unmarshal dependency as string: %w", err)
		}
		*dep = legacyDependency{Source: s}
		return nil
	}
	type alias legacyDependency
	var tmp alias
	if err := json.Unmarshal(data, &tmp); err != nil {
		return fmt.Errorf("unmarshal dependency: %w", err)
	}
	*dep = legacyDependency(tmp)
	return nil
}

// legacyCustomization represents a toolchain customization entry.
type legacyCustomization struct {
	Function    []string `json:"function,omitempty"`
	Argument    string   `json:"argument"`
	Default     string   `json:"default,omitempty"`
	DefaultPath string   `json:"defaultPath,omitempty"`
	Ignore      []string `json:"ignore,omitempty"`
}

// IsConstructor returns true if this customization applies to the constructor
// (i.e. Function is empty or nil).
func (c *legacyCustomization) IsConstructor() bool {
	return len(c.Function) == 0
}

// newModuleJSON is the cleaned-up dagger.json for a migrated module.
// Source and Toolchains are removed; dependency paths are rewritten.
type newModuleJSON struct {
	Name          string              `json:"name"`
	EngineVersion string              `json:"engineVersion,omitempty"`
	SDK           *legacySDK          `json:"sdk,omitempty"`
	Source        string              `json:"source,omitempty"`
	Dependencies  []*legacyDependency `json:"dependencies,omitempty"`
	Include       []string            `json:"include,omitempty"`
	Codegen       json.RawMessage     `json:"codegen,omitempty"`
	Clients       json.RawMessage     `json:"clients,omitempty"`
}
