package workspace

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/sdk/sdkmeta"
)

// ConventionalSDKShortName returns the workspace-side short name to use for
// an SDK install derived from its canonical source ref. Builtin runtimes
// ("go", "python", etc.) pass through unchanged; external refs collapse to
// the last path segment with any @version suffix stripped — matching the
// convention `dagger install` uses when no --name is supplied.
func ConventionalSDKShortName(sdkRef string) string {
	ref := sdkRef
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		ref = ref[i+1:]
	}
	return ref
}

// migrationSDKInstallName returns the workspace module install name to record
// for a legacy SDK ref. A builtin runtime short name (e.g. "go", "php@v1") is
// keyed by a "dagger-"-prefixed canonical basename ("dagger-go-sdk",
// "dagger-php-sdk"), matching `dagger sdk install`, so the SDK install cannot
// collide with an unrelated module legitimately named "go". External refs and
// custom/local SDK names keep their existing basename.
func migrationSDKInstallName(sdkRef string) string {
	name := ConventionalSDKShortName(sdkRef)
	if sdkmeta.IsBuiltin(name) {
		return sdkmeta.InstallNamePrefix + name + "-sdk"
	}
	return name
}

// AddMigratedModuleSDK records, in a workspace config, the SDK/runtime a
// migrated module uses: modulePath is added to the as-sdk managed-module list
// of the install that exposes sdkSource. An existing as-sdk install for the
// same runtime source is reused (so several locally-referenced modules sharing
// a runtime collapse to one [modules.<sdk>] entry); otherwise a new one is
// created, matching how the root module's SDK is recorded. This keeps every
// locally-defined module's runtime installed and pinned in the workspace.
func AddMigratedModuleSDK(wsCfg *Config, sdkSource, modulePath string) {
	if wsCfg == nil || sdkSource == "" {
		return
	}
	if wsCfg.Modules == nil {
		wsCfg.Modules = map[string]ModuleEntry{}
	}

	// Reuse the existing as-sdk install for this runtime, if any, so the same
	// runtime is not installed twice under different names.
	for name, entry := range wsCfg.Modules {
		if entry.AsSDK != nil && entry.Source == sdkSource {
			entry.AsSDK.Modules = append(entry.AsSDK.Modules, SDKManagedModule{Path: modulePath})
			wsCfg.Modules[name] = entry
			return
		}
	}

	sdkName := migrationSDKInstallName(sdkSource)
	sdkIsBuiltin := sdkmeta.IsBuiltin(ConventionalSDKShortName(sdkSource))
	entry, exists := wsCfg.Modules[sdkName]
	// A builtin SDK's legacy source (e.g. "go") is a runtime name, not a
	// module source, so an existing same-named module is never the same
	// install. For external/custom SDKs the source is a real ref/path, so
	// reuse the entry only when it matches; otherwise don't clobber it.
	if exists && (sdkIsBuiltin || entry.Source != sdkSource) {
		sdkName = uniqueModuleName(wsCfg.Modules, sdkName)
		exists = false
	}
	if !exists {
		entry = ModuleEntry{Source: sdkSource}
	}
	if entry.AsSDK == nil {
		entry.AsSDK = &ModuleAsSDK{}
	}
	entry.AsSDK.Modules = append(entry.AsSDK.Modules, SDKManagedModule{Path: modulePath})
	wsCfg.Modules[sdkName] = entry
}

// uniqueModuleName returns base if free, otherwise base with a numeric suffix,
// so a migrated SDK install never silently overwrites an unrelated module that
// happens to share its name.
func uniqueModuleName(modules map[string]ModuleEntry, base string) string {
	if _, taken := modules[base]; !taken {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if _, taken := modules[candidate]; !taken {
			return candidate
		}
	}
}

// IsLocalRef performs a fast heuristic check to determine whether a module
// reference string refers to a local path instead of a git source.
func IsLocalRef(source, pin string) bool {
	if pin != "" {
		return false
	}
	if len(source) > 0 && (source[0] == '/' || source[0] == '.') {
		return true
	}
	return !strings.Contains(source, ".")
}

// MigrationPlan is the pure filesystem plan for migrating a legacy
// dagger.json project to workspace format.
type MigrationPlan struct {
	ProjectRoot              string
	Warnings                 []string
	MigrationGapCount        int
	MigrationReportPath      string
	WorkspaceConfigData      []byte
	MigratedModuleConfigData []byte
	MigratedModuleConfigPath string
	MigrationReportData      []byte
}

// PlanMigration computes the pure filesystem plan for migrating a compat
// workspace into workspace format.
func PlanMigration(compatWorkspace *CompatWorkspace) (*MigrationPlan, error) {
	if compatWorkspace == nil || compatWorkspace.Config == nil {
		return nil, fmt.Errorf("compat workspace is required")
	}
	if compatWorkspace.ProjectRoot == "" {
		return nil, fmt.Errorf("compat workspace project root is required")
	}
	if compatWorkspace.ConfigPath == "" {
		return nil, fmt.Errorf("compat workspace config path is required")
	}

	cfg := compatWorkspace.Config
	if !mustMigrateToWorkspaceConfig(cfg) {
		return nil, fmt.Errorf("dagger.json does not require workspace config migration")
	}
	hasSDK := cfg.SDK != nil && cfg.SDK.Source != ""
	needsProjectModuleMigration := hasSDK

	plan := &MigrationPlan{
		ProjectRoot: compatWorkspace.ProjectRoot,
	}

	if needsProjectModuleMigration {
		modulePath := filepath.Join("modules", cfg.Name)
		moduleDir := filepath.Join(LockDirName, modulePath)
		newModuleConfig, err := buildMigratedModuleConfig(cfg, moduleDir)
		if err != nil {
			return nil, fmt.Errorf("building migrated module config: %w", err)
		}
		plan.MigratedModuleConfigData = newModuleConfig
		plan.MigratedModuleConfigPath = filepath.Join(moduleDir, ModuleConfigFileName)
	}

	warnings := analyzeCustomizations(cfg.Toolchains)
	plan.MigrationGapCount = len(warnings)
	for _, w := range warnings {
		plan.Warnings = append(plan.Warnings, w.message)
	}

	wsCfg := compatWorkspace.WorkspaceConfig()
	if needsProjectModuleMigration && compatWorkspace.MainModule != nil {
		wsCfg.Modules[cfg.Name] = compatWorkspace.MainModule.Entry
	}

	// Surface the legacy module's SDK ref as a workspace module installed
	// AS an SDK, with the migrated module recorded under the SDK's as-sdk
	// authoring list. Legacy dagger.json carried the SDK inline on the
	// module; new dagger.toml records every install (regular module or SDK)
	// under [modules.*], with the SDK-role data nested in
	// [modules.<sdk>.as-sdk.*]. This is the file-format catch-up for the
	// runtime/SDK split.
	if hasSDK {
		AddMigratedModuleSDK(wsCfg, cfg.SDK.Source, filepath.Join(LockDirName, "modules", cfg.Name))
	}
	workspaceConfigData, err := renderMigrationWorkspaceConfig(wsCfg, compatWorkspace.MainModule)
	if err != nil {
		return nil, fmt.Errorf("serializing workspace config: %w", err)
	}
	plan.WorkspaceConfigData = workspaceConfigData

	if len(warnings) > 0 {
		plan.MigrationReportPath = filepath.Join(LockDirName, "migration-report.md")
		plan.MigrationReportData = []byte(generateMigrationReportMarkdown(compatWorkspace.ConfigPath, warnings))
	}

	return plan, nil
}

func renderMigrationWorkspaceConfig(cfg *Config, mainModule *CompatMainModule) ([]byte, error) {
	if mainModule == nil {
		return UpdateConfigBytes(nil, cfg)
	}

	mainEntry, ok := cfg.Modules[mainModule.ConfigName]
	if !ok {
		return UpdateConfigBytes(nil, cfg)
	}

	seeded, err := UpdateConfigBytes(nil, &Config{
		Modules: map[string]ModuleEntry{
			mainModule.ConfigName: mainEntry,
		},
	})
	if err != nil {
		return nil, err
	}

	return UpdateConfigBytes(seeded, cfg)
}

// buildMigratedModuleConfig creates the cleaned-up dagger-module.toml for the migrated
// module. newModuleDir is relative to the project root.
func buildMigratedModuleConfig(cfg *modules.ModuleConfig, newModuleDir string) ([]byte, error) {
	source, err := migratedModuleRelPath(newModuleDir, cfg.Source)
	if err != nil {
		return nil, fmt.Errorf("rebasing source path: %w", err)
	}

	rootPrefix, err := migratedModuleRootPrefix(newModuleDir)
	if err != nil {
		return nil, fmt.Errorf("rebasing project root: %w", err)
	}

	deps := make([]*modules.ModuleConfigDependency, 0, len(cfg.Dependencies))
	for _, dep := range cfg.Dependencies {
		if dep == nil {
			continue
		}

		depSource := dep.Source
		if IsLocalRef(dep.Source, dep.Pin) {
			depSource, err = migratedModuleRelPath(newModuleDir, dep.Source)
			if err != nil {
				return nil, fmt.Errorf("rebasing dependency %q source path: %w", dep.Name, err)
			}
		}

		deps = append(deps, &modules.ModuleConfigDependency{
			Name:             dep.Name,
			Source:           depSource,
			Pin:              dep.Pin,
			Customizations:   cloneCustomizations(dep.Customizations),
			IgnoreChecks:     append([]string(nil), dep.IgnoreChecks...),
			IgnoreGenerators: append([]string(nil), dep.IgnoreGenerators...),
		})
	}

	includes := make([]string, 0, len(cfg.Include))
	for _, inc := range cfg.Include {
		if strings.HasPrefix(inc, "!") {
			includes = append(includes, "!"+rootPrefix+inc[1:])
		} else {
			includes = append(includes, rootPrefix+inc)
		}
	}

	newCfg := modules.ModuleConfig{
		Name:                          cfg.Name,
		EngineVersion:                 cfg.EngineVersion,
		SDK:                           cfg.SDK,
		Source:                        source,
		Dependencies:                  deps,
		Include:                       includes,
		Codegen:                       cfg.Codegen,
		Clients:                       cfg.Clients,
		DisableDefaultFunctionCaching: cfg.DisableDefaultFunctionCaching,
	}

	out, err := modules.MarshalModuleConfigForFormat(&modules.ModuleConfigWithUserFields{
		ModuleConfig: newCfg,
	}, modules.ConfigFormatCurrent)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func migratedModuleRelPath(newModuleDir, source string) (string, error) {
	if source == "" {
		source = "."
	}
	if filepath.IsAbs(source) {
		return "", fmt.Errorf("source path %q is absolute", source)
	}

	rel, err := filepath.Rel(newModuleDir, filepath.Clean(source))
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

func migratedModuleRootPrefix(newModuleDir string) (string, error) {
	rootRel, err := migratedModuleRelPath(newModuleDir, ".")
	if err != nil {
		return "", err
	}
	if rootRel == "." {
		return "", nil
	}
	return rootRel + "/", nil
}

type migrationWarning struct {
	module   string
	message  string
	original *modules.ModuleConfigArgument
}

func (w migrationWarning) originalJSON() string {
	if w.original == nil {
		return ""
	}
	origJSON, err := json.MarshalIndent(w.original, "", "  ")
	if err != nil {
		return ""
	}
	return string(origJSON)
}

func generateMigrationReportMarkdown(configPath string, warnings []migrationWarning) string {
	var b strings.Builder

	b.WriteString("# Migration Report\n\n")
	b.WriteString("Dagger migrated `dagger.json`, but some old settings need a manual check.\n\n")
	b.WriteString("ACTION: Review each item below. If your project still relies on it, add the setting back manually.\n\n")
	fmt.Fprintf(&b, "Legacy config: `%s`\n", filepath.Base(configPath))

	for i, warning := range warnings {
		fmt.Fprintf(&b, "\n## %d. `%s` needs a manual check\n\n", i+1, warning.module)
		fmt.Fprintf(&b, "Dagger could not migrate this setting automatically: %s\n", warning.message)
		if origJSON := warning.originalJSON(); origJSON != "" {
			fmt.Fprintf(&b, "\nOriginal setting:\n\n```json\n%s\n```\n", origJSON)
		}
	}

	return b.String()
}

func analyzeCustomizations(toolchains []*modules.ModuleConfigDependency) []migrationWarning {
	var warnings []migrationWarning
	for _, tc := range toolchains {
		if tc == nil {
			continue
		}
		for _, cust := range tc.Customizations {
			if cust == nil {
				continue
			}
			if !isConstructorCustomization(cust) {
				funcName := strings.Join(cust.Function, ".")
				settingName := funcName
				if cust.Argument != "" {
					settingName += "." + cust.Argument
				}
				warnings = append(warnings, migrationWarning{
					module: tc.Name,
					message: fmt.Sprintf(
						"function setting %q is not supported in workspace config",
						settingName,
					),
					original: cust,
				})
				continue
			}
			if len(cust.Ignore) > 0 || cust.DefaultPath != "" || cust.DefaultAddress != "" {
				msg := fmt.Sprintf("constructor arg %q has", cust.Argument)
				var parts []string
				if len(cust.Ignore) > 0 {
					parts = append(parts, "'ignore'")
				}
				if cust.DefaultPath != "" {
					parts = append(parts, "'defaultPath'")
				}
				if cust.DefaultAddress != "" {
					parts = append(parts, "'defaultAddress'")
				}
				msg += " " + strings.Join(parts, " and ") + ", which workspace settings do not support"
				warnings = append(warnings, migrationWarning{
					module:   tc.Name,
					message:  msg,
					original: cust,
				})
			}
		}
	}
	return warnings
}

func isConstructorCustomization(cust *modules.ModuleConfigArgument) bool {
	return len(cust.Function) == 0
}
