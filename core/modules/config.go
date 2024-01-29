package modules

import (
	"encoding/json"
	"fmt"
)

// Filename is the name of the module config file.
const Filename = "dagger.json"

// TODO: update message
const legacyRootUsageMessage = `Cannot load module config with legacy "root" setting %q, manual updates needed. Delete the current dagger.json file and re-initialize the module from the directory where root is pointing, using the -m flag to point to the module's source directory.
`

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

	// TODO:
	RootFor []*ModuleConfigRootFor `json:"root-for,omitempty"`

	// Deprecated: use Source instead, only used to identify legacy config files
	Root string `json:"root,omitempty"`
}

func (modCfg *ModuleConfig) Validate() error {
	if modCfg.Root != "" {
		// this is too hard to handle automatically, just tell the user they need to update via error message
		// nolint:stylecheck // we're okay with an error message formated as full sentences in this case
		return fmt.Errorf(legacyRootUsageMessage, modCfg.Root)
	}
	return nil
}

func (modCfg *ModuleConfig) IsRootFor(source string) bool {
	for _, rootFor := range modCfg.RootFor {
		if rootFor.Source == source {
			return true
		}
	}
	return false
}

func (modCfg *ModuleConfig) DependencyByName(name string) (*ModuleConfigDependency, bool) {
	for _, dep := range modCfg.Dependencies {
		if dep.Name == name {
			return dep, true
		}
	}
	return nil, false
}

type ModuleConfigRootFor struct {
	// TODO:
	Source string `json:"source"`
}

type ModuleConfigDependency struct {
	// TODO:
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
