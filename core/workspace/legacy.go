package workspace

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core/modules"
)

// CompatWorkspace is the shared projection of legacy dagger.json semantics
// used by both runtime compat mode and on-disk migration.
type CompatWorkspace struct {
	Modules     []CompatWorkspaceModule
	MainModule  *CompatMainModule
	Config      *modules.ModuleConfig
	ConfigPath  string
	ProjectRoot string
}

// CompatWorkspaceModule is one workspace-owned module projected out of a
// legacy dagger.json.
type CompatWorkspaceModule struct {
	Name              string
	ConfigName        string
	Source            string
	Pin               string
	Entry             ModuleEntry
	ArgCustomizations []*modules.ModuleConfigArgument
}

// CompatMainModule is the projected legacy root module. It remains a distinct
// part of the compat workspace so runtime compat can load it from the original
// legacy location while migration persists it under .dagger/modules/<name>.
type CompatMainModule struct {
	Name       string
	ConfigName string
	Entry      ModuleEntry
}

// LegacyWorkspace and LegacyWorkspaceModule remain as compatibility aliases
// for the narrower pre-compat naming used by current callers and tests.
type LegacyWorkspace = CompatWorkspace
type LegacyWorkspaceModule = CompatWorkspaceModule

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

// ParseCompatWorkspace parses an eligible legacy dagger.json into the shared
// compat-workspace representation. Returns nil if the legacy config does not
// create ambient workspace context.
func ParseCompatWorkspace(data []byte) (*CompatWorkspace, error) {
	return ParseCompatWorkspaceAt(data, "")
}

// ParseCompatWorkspaceAt parses an eligible legacy dagger.json into the shared
// compat-workspace representation, with optional provenance from the config
// path. Returns nil if the legacy config does not create ambient workspace
// context.
func ParseCompatWorkspaceAt(data []byte, configPath string) (*CompatWorkspace, error) {
	cfg, err := parseLegacyConfig(data)
	if err != nil {
		return nil, err
	}
	if !legacyConfigCreatesCompatWorkspace(cfg) {
		return nil, nil
	}
	return buildCompatWorkspace(cfg, configPath), nil
}

// ParseLegacyWorkspace preserves the previous helper name while returning the
// full compat-workspace projection.
func ParseLegacyWorkspace(data []byte) (*LegacyWorkspace, error) {
	return ParseCompatWorkspace(data)
}

// ParseLegacyBlueprint parses a legacy dagger.json and extracts its blueprint.
// Returns nil if no blueprint is present.
func ParseLegacyBlueprint(data []byte) (*LegacyBlueprint, error) {
	legacyWorkspace, err := ParseLegacyWorkspace(data)
	if err != nil {
		return nil, err
	}
	blueprint := legacyWorkspace.Blueprint()
	if blueprint == nil {
		return nil, nil
	}
	return &LegacyBlueprint{
		Name:   blueprint.Name,
		Source: blueprint.Source,
		Pin:    blueprint.Pin,
	}, nil
}

// ParseLegacyToolchains parses a legacy dagger.json and extracts its toolchains
// with their constructor arg defaults. Returns nil if no toolchains are present.
func ParseLegacyToolchains(data []byte) ([]LegacyToolchain, error) {
	legacyWorkspace, err := ParseLegacyWorkspace(data)
	if err != nil {
		return nil, err
	}
	if legacyWorkspace == nil {
		return nil, nil
	}
	result := make([]LegacyToolchain, 0, len(legacyWorkspace.Modules))
	for _, mod := range legacyWorkspace.Modules {
		if mod.Entry.Blueprint {
			continue
		}
		result = append(result, LegacyToolchain{
			Name:           mod.Name,
			Source:         mod.Source,
			Pin:            mod.Pin,
			ConfigDefaults: cloneConfigDefaults(mod.Entry.Config),
			Customizations: cloneCustomizations(mod.ArgCustomizations),
		})
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func buildCompatWorkspace(cfg *modules.ModuleConfig, configPath string) *CompatWorkspace {
	if cfg == nil {
		return nil
	}

	compatWorkspace := &CompatWorkspace{
		Config:      cfg,
		ConfigPath:  configPath,
		ProjectRoot: filepath.Dir(configPath),
	}
	if configPath == "" {
		compatWorkspace.ProjectRoot = ""
	}

	for _, tc := range cfg.Toolchains {
		if tc == nil {
			continue
		}
		compatWorkspace.Modules = append(compatWorkspace.Modules, CompatWorkspaceModule{
			Name:       tc.Name,
			ConfigName: tc.Name,
			Source:     tc.Source,
			Pin:        tc.Pin,
			Entry: ModuleEntry{
				Source:            legacyWorkspaceModuleSource(tc.Source, tc.Pin),
				Config:            extractConfigDefaults(tc.Customizations),
				LegacyDefaultPath: true,
			},
			ArgCustomizations: cloneCustomizations(tc.Customizations),
		})
	}

	if cfg.Blueprint != nil {
		name := cfg.Blueprint.Name
		if name == "" {
			name = "blueprint"
		}
		compatWorkspace.Modules = append(compatWorkspace.Modules, CompatWorkspaceModule{
			Name:       cfg.Blueprint.Name,
			ConfigName: name,
			Source:     cfg.Blueprint.Source,
			Pin:        cfg.Blueprint.Pin,
			Entry: ModuleEntry{
				Source:            legacyWorkspaceModuleSource(cfg.Blueprint.Source, cfg.Blueprint.Pin),
				Blueprint:         true,
				LegacyDefaultPath: true,
			},
		})
	}

	if cfg.SDK != nil && cfg.Name != "" {
		compatWorkspace.MainModule = &CompatMainModule{
			Name:       cfg.Name,
			ConfigName: cfg.Name,
			Entry: ModuleEntry{
				Source:    filepath.Join("modules", cfg.Name),
				Blueprint: cfg.Blueprint == nil,
			},
		}
	}

	if len(compatWorkspace.Modules) == 0 && compatWorkspace.MainModule == nil {
		return nil
	}
	return compatWorkspace
}

func legacyWorkspaceModuleSource(source, pin string) string {
	if isLocalRef(source, pin) {
		return filepath.Join("..", source)
	}
	return source
}

func (compatWorkspace *CompatWorkspace) WorkspaceConfig() *Config {
	if compatWorkspace == nil {
		return &Config{Modules: map[string]ModuleEntry{}}
	}
	cfg := &Config{
		Modules: make(map[string]ModuleEntry, len(compatWorkspace.Modules)),
	}
	for _, mod := range compatWorkspace.Modules {
		cfg.Modules[mod.ConfigName] = ModuleEntry{
			Source:            mod.Entry.Source,
			Config:            cloneConfigDefaults(mod.Entry.Config),
			Blueprint:         mod.Entry.Blueprint,
			LegacyDefaultPath: mod.Entry.LegacyDefaultPath,
		}
	}
	return cfg
}

func (compatWorkspace *CompatWorkspace) Blueprint() *CompatWorkspaceModule {
	if compatWorkspace == nil {
		return nil
	}
	for i := range compatWorkspace.Modules {
		if compatWorkspace.Modules[i].Entry.Blueprint {
			return &compatWorkspace.Modules[i]
		}
	}
	return nil
}

func legacyConfigCreatesCompatWorkspace(cfg *modules.ModuleConfig) bool {
	if cfg == nil {
		return false
	}
	if cfg.Blueprint != nil || len(cfg.Toolchains) > 0 {
		return true
	}
	return cfg.SDK != nil && cfg.Source != "" && cfg.Source != "."
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
		if cust != nil && len(cust.Function) == 0 && cust.Default != "" {
			config[cust.Argument] = cust.Default
		}
	}
	if len(config) == 0 {
		return nil
	}
	return config
}

func cloneConfigDefaults(config map[string]any) map[string]any {
	if len(config) == 0 {
		return nil
	}
	clone := make(map[string]any, len(config))
	for key, value := range config {
		clone[key] = value
	}
	return clone
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
