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

	// Paths to explicitly exclude from the module, relative to the configuration file.
	Exclude []string `json:"exclude,omitempty"`

	// The modules this module depends on.
	Dependencies []*ModuleConfigDependency `json:"dependencies,omitempty"`

	// The path, relative to this config file, to the subdir containing the module's implementation source code.
	Source string `json:"source,omitempty"`
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
