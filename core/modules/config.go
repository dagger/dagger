package modules

import (
	"encoding/json"
	"fmt"
)

// Filename is the name of the module config file.
const Filename = "dagger.json"

// ModuleConfig is the config for a single module as loaded from a dagger.json file.
type ModuleConfig struct {
	// The name of the module.
	Name string `json:"name,omitempty"`

	// The SDK this module uses
	SDK string `json:"sdk,omitempty"`

	// Paths to explicitly include from the module, relative to the configuration file.
	Include []string `json:"include,omitempty"`

	// Deprecated: Use Include patterns with a leading ! to exclude files. Any setting here
	// will be automatically converted to an Include pattern.
	//
	// Paths to explicitly exclude from the module, relative to the configuration file.
	Exclude []string `json:"exclude,omitempty"`

	// The modules this module depends on.
	Dependencies []*ModuleConfigDependency `json:"dependencies,omitempty"`

	// The path, relative to this config file, to the subdir containing the module's implementation source code.
	Source string `json:"source,omitempty"`

	// The version of the engine this module was last updated with.
	EngineVersion string `json:"engineVersion,omitempty"`

	// Named views defined for this module, which are sets of directory filters that can be applied to
	// directory arguments provided to functions.
	Views []*ModuleConfigView `json:"views,omitempty"`
}

func (modCfg *ModuleConfig) UnmarshalJSON(data []byte) error {
	if modCfg == nil {
		return fmt.Errorf("cannot unmarshal into nil ModuleConfig")
	}
	if len(data) == 0 {
		return nil
	}

	type alias ModuleConfig // lets us use the default json unmashaler
	var tmp alias
	if err := json.Unmarshal(data, &tmp); err != nil {
		return fmt.Errorf("unmarshal module config: %w", err)
	}

	// Detect the case where SDK is set but Source isn't, which should only happen when loading an older config.
	// For those cases, the Source was implicitly ".", so set it to that.
	if tmp.SDK != "" && tmp.Source == "" {
		tmp.Source = "."
	}

	// Convert Exclude to Include patterns with a leading !
	if len(tmp.Exclude) > 0 {
		for _, exclude := range tmp.Exclude {
			tmp.Include = append(tmp.Include, "!"+exclude)
		}
		tmp.Exclude = nil
	}

	*modCfg = ModuleConfig(tmp)
	return nil
}

func (modCfg *ModuleConfig) DependencyByName(name string) (*ModuleConfigDependency, bool) {
	for _, dep := range modCfg.Dependencies {
		if dep.Name == name {
			return dep, true
		}
	}
	return nil, false
}

type ModuleConfigDependency struct {
	// The name to use for this dependency. By default, the same as the dependency module's name,
	// but can also be overridden to use a different name.
	Name string `json:"name"`

	// The source ref of the module dependency.
	Source string `json:"source"`
}

func (depCfg *ModuleConfigDependency) UnmarshalJSON(data []byte) error {
	if depCfg == nil {
		return fmt.Errorf("cannot unmarshal into nil ModuleConfigDependency")
	}
	if len(data) == 0 {
		depCfg.Source = ""
		return nil
	}

	// check if this is a legacy config, where deps were just a list of strings
	if data[0] == '"' {
		var depRefStr string
		if err := json.Unmarshal(data, &depRefStr); err != nil {
			return fmt.Errorf("unmarshal module config dependency: %w", err)
		}
		*depCfg = ModuleConfigDependency{Source: depRefStr}
		return nil
	}

	type alias ModuleConfigDependency // lets us use the default json unmashaler
	var tmp alias
	if err := json.Unmarshal(data, &tmp); err != nil {
		return fmt.Errorf("unmarshal module config dependency: %w", err)
	}
	*depCfg = ModuleConfigDependency(tmp)
	return nil
}

type ModuleConfigView struct {
	Name     string   `json:"name"`
	Patterns []string `json:"patterns,omitempty"`
}
