package modules

import (
	"encoding/json"
	"fmt"

	toml "github.com/pelletier/go-toml"
)

// CurrentModuleConfigWithUserFields is the public schema for dagger-module.toml.
type CurrentModuleConfigWithUserFields struct {
	ModuleConfigUserFields
	CurrentModuleConfig
}

// CurrentModuleConfig is the dagger-module.toml schema.
type CurrentModuleConfig struct {
	// The name of the module.
	Name string `json:"name" toml:"name"`

	// The version of the engine this module was last updated with.
	EngineVersion string `json:"engineVersion" toml:"engineVersion"`

	// Paths to explicitly include from the module, relative to the configuration file.
	Include []string `json:"include,omitempty" toml:"include,omitempty"`

	// The path, relative to this config file, to the subdir containing the module's implementation source code.
	Source string `json:"source,omitempty" toml:"source,omitempty"`

	// Paths to explicitly exclude from the module, relative to the configuration file.
	//
	// Deprecated: Use !<pattern> in the include list instead.
	Exclude []string `json:"exclude,omitempty" toml:"exclude,omitempty"`

	// If true, disable the new default function caching behavior for this module. Functions will
	// instead default to the old behavior of per-session caching.
	DisableDefaultFunctionCaching *bool `json:"disableDefaultFunctionCaching,omitempty" toml:"disableDefaultFunctionCaching,omitempty"`

	// The runtime this module uses.
	Runtime *SDK `json:"runtime,omitempty" toml:"runtime,omitempty"`

	// The modules this module depends on.
	Dependencies []*CurrentModuleConfigDependency `json:"dependencies,omitempty" toml:"dependencies,omitempty"`

	// Codegen configuration for this module.
	Codegen *ModuleCodegenConfig `json:"codegen,omitempty" toml:"codegen,omitempty"`

	// The clients generated for this module.
	Clients []*ModuleConfigClient `json:"clients,omitempty" toml:"clients,omitempty"`
}

// CurrentModuleConfigDependency is a dagger-module.toml dependency.
type CurrentModuleConfigDependency struct {
	// The name to use for this dependency. By default, the same as the dependency module's name,
	// but can also be overridden to use a different name.
	Name string `json:"name,omitempty" toml:"name,omitempty"`

	// The source ref of the module dependency.
	Source string `json:"source" toml:"source"`

	// The pinned version of the module dependency.
	Pin string `json:"pin,omitempty" toml:"pin,omitempty"`
}

// LegacyModuleConfigWithUserFields is the frozen public schema for dagger.json.
type LegacyModuleConfigWithUserFields struct {
	ModuleConfigUserFields
	ModuleConfig
}

func newCurrentModuleConfigWithUserFields(modCfg *ModuleConfigWithUserFields) *CurrentModuleConfigWithUserFields {
	if modCfg == nil {
		return &CurrentModuleConfigWithUserFields{}
	}

	return &CurrentModuleConfigWithUserFields{
		ModuleConfigUserFields: modCfg.ModuleConfigUserFields,
		CurrentModuleConfig: CurrentModuleConfig{
			Name:                          modCfg.Name,
			EngineVersion:                 modCfg.EngineVersion,
			Include:                       append([]string(nil), modCfg.Include...),
			Source:                        modCfg.Source,
			Exclude:                       append([]string(nil), modCfg.Exclude...),
			DisableDefaultFunctionCaching: cloneBoolPtr(modCfg.DisableDefaultFunctionCaching),
			Runtime:                       modCfg.SDK,
			Dependencies:                  currentModuleConfigDependencies(modCfg.Dependencies),
			Codegen:                       modCfg.Codegen,
			Clients:                       cloneModuleConfigClients(modCfg.Clients),
		},
	}
}

func (cfg *CurrentModuleConfigWithUserFields) moduleConfigWithUserFields() *ModuleConfigWithUserFields {
	if cfg == nil {
		return &ModuleConfigWithUserFields{}
	}

	return &ModuleConfigWithUserFields{
		ModuleConfigUserFields: cfg.ModuleConfigUserFields,
		ModuleConfig: ModuleConfig{
			Name:                          cfg.Name,
			EngineVersion:                 cfg.EngineVersion,
			Include:                       append([]string(nil), cfg.Include...),
			Source:                        cfg.Source,
			Exclude:                       append([]string(nil), cfg.Exclude...),
			DisableDefaultFunctionCaching: cloneBoolPtr(cfg.DisableDefaultFunctionCaching),
			SDK:                           cfg.Runtime,
			Dependencies:                  moduleConfigDependenciesFromCurrent(cfg.Dependencies),
			Codegen:                       cfg.Codegen,
			Clients:                       cloneModuleConfigClients(cfg.Clients),
		},
	}
}

func currentModuleConfigDependencies(deps []*ModuleConfigDependency) []*CurrentModuleConfigDependency {
	if len(deps) == 0 {
		return nil
	}
	current := make([]*CurrentModuleConfigDependency, 0, len(deps))
	for _, dep := range deps {
		if dep == nil {
			continue
		}
		current = append(current, &CurrentModuleConfigDependency{
			Name:   dep.Name,
			Source: dep.Source,
			Pin:    dep.Pin,
		})
	}
	return current
}

func moduleConfigDependenciesFromCurrent(deps []*CurrentModuleConfigDependency) []*ModuleConfigDependency {
	if len(deps) == 0 {
		return nil
	}
	current := make([]*ModuleConfigDependency, 0, len(deps))
	for _, dep := range deps {
		if dep == nil {
			continue
		}
		current = append(current, &ModuleConfigDependency{
			Name:   dep.Name,
			Source: dep.Source,
			Pin:    dep.Pin,
		})
	}
	return current
}

func cloneModuleConfigClients(clients []*ModuleConfigClient) []*ModuleConfigClient {
	if len(clients) == 0 {
		return nil
	}
	cloned := make([]*ModuleConfigClient, 0, len(clients))
	for _, client := range clients {
		if client == nil {
			continue
		}
		cloned = append(cloned, client.Clone())
	}
	return cloned
}

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	cloned := *v
	return &cloned
}

func validateCurrentModuleConfigTOML(data []byte) error {
	tree, err := toml.LoadBytes(data)
	if err != nil {
		return fmt.Errorf("failed to decode module config: %w", err)
	}
	if tree.Get("sdk") != nil {
		return fmt.Errorf("%s uses runtime instead of sdk", Filename)
	}
	for _, field := range []string{"blueprint", "toolchains"} {
		if tree.Get(field) != nil {
			return fmt.Errorf("%s does not support %q", Filename, field)
		}
	}

	deps, ok := tree.Get("dependencies").([]*toml.Tree)
	if !ok {
		return nil
	}
	for i, dep := range deps {
		for _, field := range []string{
			"customizations",
			"ignoreChecks",
			"ignoreGenerators",
			"ignoreServices",
			"portMappings",
		} {
			if dep.Get(field) != nil {
				return fmt.Errorf("%s dependency %d does not support %q", Filename, i, field)
			}
		}
	}
	return nil
}

func validateLegacyModuleConfigJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("failed to decode module config: %w", err)
	}
	if _, ok := raw["runtime"]; ok {
		return fmt.Errorf("%s uses sdk instead of runtime", LegacyFilename)
	}
	return nil
}
