package workspace

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

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

// ParseCompatWorkspace parses a legacy dagger.json into the migration-compatible
// compat-workspace representation. Returns nil if the legacy config does not
// need to migrate into workspace config at its own location.
func ParseCompatWorkspace(data []byte) (*CompatWorkspace, error) {
	return ParseCompatWorkspaceAt(data, "")
}

// ParseCompatWorkspaceAt parses a legacy dagger.json into the migration-compatible
// compat-workspace representation, with optional provenance from the config
// path. Returns nil if the legacy config does not need to migrate into
// workspace config at its own location.
func ParseCompatWorkspaceAt(data []byte, configPath string) (*CompatWorkspace, error) {
	cfg, err := parseLegacyConfig(data)
	if err != nil {
		return nil, err
	}
	if !mustMigrateToWorkspaceConfig(cfg) {
		return nil, nil
	}
	return buildCompatWorkspace(cfg, configPath), nil
}

// ParseRuntimeCompatWorkspaceAt parses a legacy dagger.json into the runtime
// compat-workspace representation, with optional provenance from the config
// path. Returns nil if the legacy config cannot create ambient workspace
// context.
func ParseRuntimeCompatWorkspaceAt(data []byte, configPath string) (*CompatWorkspace, error) {
	cfg, err := parseLegacyConfig(data)
	if err != nil {
		return nil, err
	}
	if !legacyConfigCreatesRuntimeCompatWorkspace(cfg) {
		return nil, nil
	}
	return buildCompatWorkspace(cfg, configPath), nil
}

// ParseMigrationCompatWorkspaceAt parses a legacy dagger.json for migration
// planning. Unlike runtime loading, migration may need to plan a best-effort
// diff for a module that requires a newer engine so `dagger setup` (with
// the migration step's --force flow) can still write reviewable workspace
// files.
func ParseMigrationCompatWorkspaceAt(data []byte, configPath string) (*CompatWorkspace, error) {
	cfg, err := parseLegacyConfig(data)
	if err != nil {
		if !strings.Contains(err.Error(), "module requires dagger") {
			return nil, err
		}
		cfg, err = parseLegacyConfigShape(data)
		if err != nil {
			return nil, err
		}
	}
	if !legacyConfigCreatesRuntimeCompatWorkspace(cfg) {
		return nil, nil
	}
	return buildCompatWorkspace(cfg, configPath), nil
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
				Settings:          ExtractConfigDefaults(tc.Customizations),
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
				Entrypoint:        true,
				LegacyDefaultPath: true,
			},
		})
	}

	if cfg.SDK != nil && cfg.Name != "" {
		compatWorkspace.MainModule = &CompatMainModule{
			Name:       cfg.Name,
			ConfigName: cfg.Name,
			Entry: ModuleEntry{
				Source:     filepath.Join(LockDirName, "modules", cfg.Name),
				Entrypoint: cfg.Blueprint == nil,
			},
		}
	}

	if len(compatWorkspace.Modules) == 0 && compatWorkspace.MainModule == nil {
		return nil
	}
	return compatWorkspace
}

func legacyWorkspaceModuleSource(source, pin string) string {
	if pin == "" {
		return source
	}
	if idx := strings.LastIndex(source, "@"); idx >= 0 {
		return source[:idx+1] + pin
	}
	return source + "@" + pin
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
			Settings:          cloneConfigDefaults(mod.Entry.Settings),
			Entrypoint:        mod.Entry.Entrypoint,
			LegacyDefaultPath: mod.Entry.LegacyDefaultPath,
		}
	}
	if compatWorkspace.Config != nil {
		for _, tc := range compatWorkspace.Config.Toolchains {
			if tc == nil {
				continue
			}
			entry, ok := cfg.Modules[tc.Name]
			if !ok {
				continue
			}
			entry.Up.Skip = append([]string(nil), tc.IgnoreServices...)
			entry.Generate.Skip = append([]string(nil), tc.IgnoreGenerators...)
			entry.Check.Skip = append([]string(nil), tc.IgnoreChecks...)
			cfg.Modules[tc.Name] = entry

			for svc, raws := range tc.PortMappings {
				for _, raw := range raws {
					host, container, err := parseHostContainerPort(raw)
					if err != nil {
						continue
					}
					if cfg.Ports == nil {
						cfg.Ports = map[string]PortMapping{}
					}
					cfg.Ports[strconv.Itoa(host)] = PortMapping{
						BackendService: tc.Name + ":" + svc,
						BackendPort:    container,
					}
				}
			}
		}
	}
	return cfg
}

// MustMigrateToWorkspaceConfig reports whether this compat workspace was
// created from a legacy dagger.json that must be replaced by dagger.toml
// at the same location during migration.
func (compatWorkspace *CompatWorkspace) MustMigrateToWorkspaceConfig() bool {
	if compatWorkspace == nil {
		return false
	}
	return mustMigrateToWorkspaceConfig(compatWorkspace.Config)
}

// parseHostContainerPort parses a "host:container" port string. Local
// reimplementation of the engine's core.ParsePortMapping to avoid an import
// cycle (core/workspace is imported by core).
func parseHostContainerPort(raw string) (host, container int, err error) {
	parts := strings.Split(raw, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("port mapping %q: expected host:container", raw)
	}
	host, err = strconv.Atoi(parts[0])
	if err != nil || host < 1 || host > 65535 {
		return 0, 0, fmt.Errorf("port mapping %q: invalid host port %q", raw, parts[0])
	}
	container, err = strconv.Atoi(parts[1])
	if err != nil || container < 1 || container > 65535 {
		return 0, 0, fmt.Errorf("port mapping %q: invalid container port %q", raw, parts[1])
	}
	return host, container, nil
}

func legacyConfigCreatesRuntimeCompatWorkspace(cfg *modules.ModuleConfig) bool {
	if mustMigrateToWorkspaceConfig(cfg) {
		return true
	}
	return cfg != nil && cfg.SDK != nil && cfg.Name != ""
}

// mustMigrateToWorkspaceConfig reports whether a legacy dagger.json carries
// workspace-level semantics and must be replaced by dagger.toml at the
// same location during migration.
func mustMigrateToWorkspaceConfig(cfg *modules.ModuleConfig) bool {
	if cfg == nil {
		return false
	}
	if cfg.Blueprint != nil || len(cfg.Toolchains) > 0 {
		return true
	}
	return cfg.SDK != nil && cfg.Source != "" && cfg.Source != "."
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
			ConfigDefaults: ExtractConfigDefaults(tc.Customizations),
			Customizations: cloneCustomizations(tc.Customizations),
		}
	}
	return result, nil
}

// parseLegacyConfig parses a legacy dagger.json into the internal representation.
func parseLegacyConfig(data []byte) (*modules.ModuleConfig, error) {
	cfg, err := modules.ParseModuleConfigForFormat(data, modules.ConfigFormatLegacy)
	if err != nil {
		return nil, fmt.Errorf("parsing legacy config: %w", err)
	}
	return &cfg.ModuleConfig, nil
}

func parseLegacyConfigShape(data []byte) (*modules.ModuleConfig, error) {
	var cfg modules.ModuleConfigWithUserFields
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to decode module config: %w", err)
	}
	return &cfg.ModuleConfig, nil
}

// ExtractConfigDefaults returns constructor arg defaults from customizations.
func ExtractConfigDefaults(customizations []*modules.ModuleConfigArgument) map[string]any {
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
