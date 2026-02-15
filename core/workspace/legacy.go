package workspace

import (
	"encoding/json"
	"fmt"
)

// legacyConfig is the dagger.json config format used before the workspace model.
type legacyConfig struct {
	Name          string              `json:"name"`
	EngineVersion string              `json:"engineVersion"`
	SDK           *legacySDK          `json:"sdk,omitempty"`
	Source        string              `json:"source,omitempty"`
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
