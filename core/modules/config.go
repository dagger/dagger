package modules

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

// Filename is the name of the module config file.
const Filename = "dagger.json"

const legacyRootUsageMessage = `Cannot load module config with legacy "root" setting %q, manual updates needed. Delete the current dagger.json file and re-initialize the module from the directory where root is pointing, using the -m flag to point to the module's source directory.
`

// ModulesConfig is the config for one or more modules as loaded from a dagger.json file.
type ModulesConfig struct {
	// The modules managed by this configuration file.
	Modules []*ModuleConfig `json:"modules"`
}

func (modsCfg *ModulesConfig) UnmarshalJSON(data []byte) error {
	if modsCfg == nil {
		return fmt.Errorf("cannot unmarshal into nil ModulesConfig")
	}

	type alias ModulesConfig // lets us use the default json unmashaler
	var tmp alias
	if err := json.Unmarshal(data, &tmp); err != nil {
		return fmt.Errorf("unmarshal modules config: %w", err)
	}

	if tmp.Modules == nil {
		// this is probably a legacy config file with a single module
		var modCfg ModuleConfig
		if err := json.Unmarshal(data, &modCfg); err != nil {
			return fmt.Errorf("unmarshal legacy config: %w", err)
		}
		if modCfg.Root != "" {
			// this is too hard to handle automatically, just tell the user they need to update via error message
			// nolint:stylecheck // we're okay with an error message formated as full sentences in this case
			return fmt.Errorf(legacyRootUsageMessage, modCfg.Root)
		}
		modCfg.Source = "."
		tmp.Modules = []*ModuleConfig{&modCfg}
	}

	*modsCfg = ModulesConfig(tmp)
	return nil
}

func (modsCfg *ModulesConfig) Validate() error {
	for _, modCfg := range modsCfg.Modules {
		if err := modCfg.Validate(); err != nil {
			return fmt.Errorf("module %s: %w", modCfg.Name, err)
		}
	}
	return nil
}

func (modsCfg *ModulesConfig) ModuleConfigByPath(sourcePath string) (*ModuleConfig, bool) {
	sourcePath = filepath.Clean(sourcePath)
	for _, modCfg := range modsCfg.Modules {
		if modCfg.Source == sourcePath {
			return modCfg, true
		}
	}
	return nil, false
}

// ModuleConfig is the config for a single module as loaded from a dagger.json file.
type ModuleConfig struct {
	// The name of the module.
	Name string `json:"name"`

	// The path to the module source code dir relative to the configuration file.
	Source string `json:"source"`

	// The SDK this module uses
	SDK string `json:"sdk"`

	// Paths to explicitly include from the module, relative to the configuration file.
	Include []string `json:"include,omitempty"`

	// Paths to explicitly exclude from the module, relative to the configuration file.
	Exclude []string `json:"exclude,omitempty"`

	// The modules this module depends on.
	Dependencies []*ModuleConfigDependency `json:"dependencies,omitempty"`

	// Deprecated: use Source instead, only used to identify legacy config files
	Root string `json:"root,omitempty"`
}

func (modCfg *ModuleConfig) Validate() error {
	if modCfg.Source != "" {
		// IsLocal validates that it's not absolute and doesn't have any ../'s in it
		if !filepath.IsLocal(modCfg.Source) {
			return fmt.Errorf("%s is not under the module configuration root", modCfg.Source)
		}
	}
	return nil
}

type ModuleConfigDependency struct {
	// The ref of the module dependency.
	Ref string `json:"ref"`
}

func (depCfg *ModuleConfigDependency) UnmarshalJSON(data []byte) error {
	if depCfg == nil {
		return fmt.Errorf("cannot unmarshal into nil ModuleConfigDependency")
	}
	if len(data) == 0 {
		depCfg.Ref = ""
		return nil
	}

	// check if this is a legacy config, where deps were just a list of strings
	if data[0] == '"' {
		var depRefStr string
		if err := json.Unmarshal(data, &depRefStr); err != nil {
			return fmt.Errorf("unmarshal module config dependency: %w", err)
		}
		*depCfg = ModuleConfigDependency{Ref: depRefStr}
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
