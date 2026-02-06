package main

import (
	"encoding/json"
	"fmt"
)

// LegacyConfig is a subset of the dagger.json config format, containing
// only the fields relevant for migration detection and transformation.
type LegacyConfig struct {
	Name          string              `json:"name"`
	EngineVersion string              `json:"engineVersion"`
	SDK           *LegacySDK          `json:"sdk,omitempty"`
	Source        string              `json:"source,omitempty"`
	Toolchains    []*LegacyDependency `json:"toolchains,omitempty"`
	Dependencies  []*LegacyDependency `json:"dependencies,omitempty"`
	Include       []string            `json:"include,omitempty"`
	Codegen       json.RawMessage     `json:"codegen,omitempty"`
	Clients       json.RawMessage     `json:"clients,omitempty"`
}

// LegacySDK represents the sdk field in dagger.json.
// Supports both string and object forms for backwards compat.
type LegacySDK struct {
	Source string         `json:"source"`
	Config map[string]any `json:"config,omitempty"`
}

func (sdk *LegacySDK) UnmarshalJSON(data []byte) error {
	if sdk == nil {
		return fmt.Errorf("cannot unmarshal into nil LegacySDK")
	}
	if len(data) == 0 {
		sdk.Source = ""
		return nil
	}
	// check if this is a legacy config, where sdk was a string
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("unmarshal sdk as string: %w", err)
		}
		*sdk = LegacySDK{Source: s}
		return nil
	}
	type alias LegacySDK
	var tmp alias
	if err := json.Unmarshal(data, &tmp); err != nil {
		return fmt.Errorf("unmarshal sdk as struct: %w", err)
	}
	*sdk = LegacySDK(tmp)
	return nil
}

// LegacyDependency represents a dependency/toolchain entry in dagger.json.
type LegacyDependency struct {
	Name           string                 `json:"name"`
	Source         string                 `json:"source"`
	Pin            string                 `json:"pin,omitempty"`
	Customizations []*LegacyCustomization `json:"customizations,omitempty"`
	IgnoreChecks   []string               `json:"ignoreChecks,omitempty"`
}

func (dep *LegacyDependency) UnmarshalJSON(data []byte) error {
	if dep == nil {
		return fmt.Errorf("cannot unmarshal into nil LegacyDependency")
	}
	if len(data) == 0 {
		dep.Source = ""
		return nil
	}
	// check if this is a legacy config, where deps were just strings
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("unmarshal dependency as string: %w", err)
		}
		*dep = LegacyDependency{Source: s}
		return nil
	}
	type alias LegacyDependency
	var tmp alias
	if err := json.Unmarshal(data, &tmp); err != nil {
		return fmt.Errorf("unmarshal dependency: %w", err)
	}
	*dep = LegacyDependency(tmp)
	return nil
}

// LegacyCustomization represents a toolchain customization entry.
type LegacyCustomization struct {
	Function    []string `json:"function,omitempty"`
	Argument    string   `json:"argument"`
	Default     string   `json:"default,omitempty"`
	DefaultPath string   `json:"defaultPath,omitempty"`
	Ignore      []string `json:"ignore,omitempty"`
}

// IsConstructor returns true if this customization applies to the constructor
// (i.e. Function is empty or nil).
func (c *LegacyCustomization) IsConstructor() bool {
	return len(c.Function) == 0
}

// NewModuleJSON is the cleaned-up dagger.json for the migrated module.
// Source and Toolchains are removed; dependency paths are rewritten.
type NewModuleJSON struct {
	Name          string              `json:"name"`
	EngineVersion string              `json:"engineVersion,omitempty"`
	SDK           *LegacySDK          `json:"sdk,omitempty"`
	Dependencies  []*LegacyDependency `json:"dependencies,omitempty"`
	Include       []string            `json:"include,omitempty"`
	Codegen       json.RawMessage     `json:"codegen,omitempty"`
	Clients       json.RawMessage     `json:"clients,omitempty"`
}
