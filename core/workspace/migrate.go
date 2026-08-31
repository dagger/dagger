package workspace

import (
	"encoding/json"
	"fmt"
	"path"
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
	sdkName := ensureMigratedSDKInstall(wsCfg, sdkSource)
	if sdkName == "" {
		return
	}
	entry := wsCfg.Modules[sdkName]
	entry.AsSDK.Modules = append(entry.AsSDK.Modules, SDKManagedModule{Path: modulePath})
	wsCfg.Modules[sdkName] = entry
}

// AddMigratedSDKInstall records a workspace SDK install for sdkSource without
// registering a managed module — the "repo is just a dagger module" pin, where
// the one install is expected to serve every module in the repo. It reuses an
// existing as-sdk install for the same runtime source, so a later
// AddMigratedModuleSDK for the same runtime lands on the same entry instead of
// installing the SDK twice.
func AddMigratedSDKInstall(wsCfg *Config, sdkSource string) {
	ensureMigratedSDKInstall(wsCfg, sdkSource)
}

// ensureMigratedSDKInstall finds or creates the as-sdk install entry for
// sdkSource and returns its install name ("" if there is nothing to record).
func ensureMigratedSDKInstall(wsCfg *Config, sdkSource string) string {
	if wsCfg == nil || sdkSource == "" {
		return ""
	}
	if wsCfg.Modules == nil {
		wsCfg.Modules = map[string]ModuleEntry{}
	}

	// Reuse the existing as-sdk install for this runtime, if any, so the same
	// runtime is not installed twice under different names.
	for name, entry := range wsCfg.Modules {
		if entry.AsSDK != nil && entry.Source == sdkSource {
			return name
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
	wsCfg.Modules[sdkName] = entry
	return sdkName
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

// MigrationPlan is the pure filesystem plan for migrating a legacy
// dagger.json project to workspace format.
type MigrationPlan struct {
	// ProjectRoot is where the workspace config (dagger.toml) and migration
	// report land.
	ProjectRoot string
	// ModuleProjectRoot is where the legacy dagger.json lives: the migrated
	// dagger-module.toml replaces it there and the legacy file is removed
	// there. It equals ProjectRoot except for a hoisted subdirectory project,
	// whose workspace fields migrate to the workspace root while the module
	// config stays in place.
	ModuleProjectRoot        string
	Warnings                 []string
	MigrationGapCount        int
	MigrationReportPath      string
	WorkspaceConfigData      []byte
	MigratedModuleConfigData []byte
	MigratedModuleConfigPath string
	MigrationReportData      []byte
}

// PlanMigration computes the pure filesystem plan for migrating a compat
// workspace into workspace format. workspaceRoot is the workspace boundary:
// when the legacy config lives below it, the plan is "hoisted" — nested
// dagger.toml files are never created, so the config's workspace fields
// (toolchains) are installed into a dagger.toml at the workspace root with
// their local sources rebased, while the module config still converts in
// place at its own directory and the module itself is not installed.
//
// When the config has an SDK, its toolchains are also recorded as
// dependencies of the migrated module config (see buildMigratedModuleConfig).
func PlanMigration(compatWorkspace *CompatWorkspace, workspaceRoot string) (*MigrationPlan, error) {
	if compatWorkspace == nil || compatWorkspace.Config == nil {
		return nil, fmt.Errorf("compat workspace is required")
	}
	if compatWorkspace.ProjectRoot == "" {
		return nil, fmt.Errorf("compat workspace project root is required")
	}
	if compatWorkspace.ConfigPath == "" {
		return nil, fmt.Errorf("compat workspace config path is required")
	}
	if workspaceRoot == "" {
		workspaceRoot = compatWorkspace.ProjectRoot
	}

	cfg := compatWorkspace.Config
	if !mustMigrateToWorkspaceConfig(cfg) {
		return nil, fmt.Errorf("dagger.json does not require workspace config migration")
	}

	moduleRoot := compatWorkspace.ProjectRoot
	hoistRel, err := filepath.Rel(filepath.Clean(workspaceRoot), filepath.Clean(moduleRoot))
	if err != nil {
		return nil, fmt.Errorf("project root %q relative to workspace root %q: %w", moduleRoot, workspaceRoot, err)
	}
	hoistRel = filepath.ToSlash(hoistRel)
	if hoistRel == ".." || strings.HasPrefix(hoistRel, "../") {
		return nil, fmt.Errorf("project root %q escapes workspace root %q", moduleRoot, workspaceRoot)
	}
	hoisted := hoistRel != "."
	if hoisted && cfg.Blueprint != nil {
		// A subdirectory blueprint config is left as legacy by the caller;
		// hoisting a blueprint would change the repo-wide entrypoint.
		return nil, fmt.Errorf("subdirectory config with a blueprint cannot be hoisted to the workspace root")
	}

	hasSDK := cfg.SDK != nil && cfg.SDK.Source != ""
	sourceAtRoot := ModuleSourceAtRoot(cfg)

	plan := &MigrationPlan{
		ProjectRoot:       workspaceRoot,
		ModuleProjectRoot: moduleRoot,
	}
	if !hoisted {
		plan.ProjectRoot = moduleRoot
	}

	// The module config replaces dagger.json at the exact same location:
	// other repos may install this module by path, and that path must keep
	// resolving to the module config file. Nothing moves and no paths are
	// rebased.
	if hasSDK {
		newModuleConfig, err := buildMigratedModuleConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("building migrated module config: %w", err)
		}
		plan.MigratedModuleConfigData = newModuleConfig
		plan.MigratedModuleConfigPath = ModuleConfigFileName
	}

	warnings := analyzeCustomizations(cfg.Toolchains)
	plan.MigrationGapCount = len(warnings)
	for _, w := range warnings {
		plan.Warnings = append(plan.Warnings, w.message)
	}

	wsCfg := compatWorkspace.WorkspaceConfig()
	if hoisted {
		// The toolchain installs move from the subdirectory config into the
		// workspace-root dagger.toml, so their local sources must be
		// re-expressed relative to the workspace root.
		rebaseHoistedModuleSources(wsCfg, hoistRel)
	}
	mainModule := compatWorkspace.MainModule
	if !hasSDK || sourceAtRoot || hoisted {
		// A module whose source is the project root IS the repo, not a module
		// owned by a surrounding project: it is not installed into the
		// workspace and gets no entrypoint. Its SDK runtime is pinned
		// separately (as a plain module install) by the migration caller.
		// A hoisted subdirectory module is likewise never installed into the
		// repo-wide workspace — only its toolchains are.
		mainModule = nil
	}
	if mainModule != nil {
		wsCfg.Modules[cfg.Name] = mainModule.Entry

		// Surface the legacy module's SDK ref as a workspace module installed
		// AS an SDK, with the migrated module recorded under the SDK's as-sdk
		// authoring list. Legacy dagger.json carried the SDK inline on the
		// module; new dagger.toml records every install (regular module or
		// SDK) under [modules.*], with the SDK-role data nested in
		// [modules.<sdk>.as-sdk.*]. This is the file-format catch-up for the
		// runtime/SDK split. The module path is the module config's directory
		// — the project root, where dagger-module.toml replaced dagger.json.
		AddMigratedModuleSDK(wsCfg, cfg.SDK.Source, ".")
	}
	workspaceConfigData, err := renderMigrationWorkspaceConfig(wsCfg, mainModule)
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

// rebaseHoistedModuleSources rewrites local module install sources so refs
// that were relative to a subdirectory legacy config resolve from the
// workspace root, where the hoisted dagger.toml lives. Remote refs pass
// through untouched.
func rebaseHoistedModuleSources(wsCfg *Config, rel string) {
	for name, entry := range wsCfg.Modules {
		if !IsLocalRef(entry.Source, "") {
			continue
		}
		entry.Source = "./" + path.Join(rel, filepath.ToSlash(entry.Source))
		wsCfg.Modules[name] = entry
	}
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

// buildMigratedModuleConfig creates the cleaned-up dagger-module.toml that
// replaces dagger.json at the same location. The config does not move, so
// source, dependency, and include paths are preserved as-is; only the
// workspace-level fields (toolchains, blueprint, customizations) are dropped —
// those migrate into dagger.toml instead.
//
// Toolchains are additionally recorded as plain dependencies of the migrated
// module: in 0.21 a module's toolchains were also loaded into its own API, so
// module code could call them (e.g. dag.Go) exactly like a dependency. Keeping
// them as dependencies is what keeps that code compiling after migration. The
// workspace-only fields of a toolchain (customizations, ignore lists, port
// mappings) belong to the dagger.toml install and are not carried over.
func buildMigratedModuleConfig(cfg *modules.ModuleConfig) ([]byte, error) {
	if cfg.Source != "" && filepath.IsAbs(cfg.Source) {
		return nil, fmt.Errorf("source path %q is absolute", cfg.Source)
	}
	source := cfg.Source
	if source != "" {
		source = filepath.ToSlash(filepath.Clean(source))
	}
	if source == "." {
		// An empty source means "here", matching configs authored natively.
		source = ""
	}

	deps := make([]*modules.ModuleConfigDependency, 0, len(cfg.Dependencies)+len(cfg.Toolchains))
	depNames := make(map[string]struct{}, len(cfg.Dependencies)+len(cfg.Toolchains))
	for _, dep := range cfg.Dependencies {
		if dep == nil {
			continue
		}
		deps = append(deps, &modules.ModuleConfigDependency{
			Name:             dep.Name,
			Source:           dep.Source,
			Pin:              dep.Pin,
			Customizations:   cloneCustomizations(dep.Customizations),
			IgnoreChecks:     append([]string(nil), dep.IgnoreChecks...),
			IgnoreGenerators: append([]string(nil), dep.IgnoreGenerators...),
		})
		depNames[dep.Name] = struct{}{}
	}
	for _, tc := range cfg.Toolchains {
		if tc == nil {
			continue
		}
		if _, dup := depNames[tc.Name]; dup {
			// Already an explicit dependency under the same name; the
			// dependency entry wins so the module keeps the ref it declared.
			continue
		}
		deps = append(deps, &modules.ModuleConfigDependency{
			Name:   tc.Name,
			Source: tc.Source,
			Pin:    tc.Pin,
		})
		depNames[tc.Name] = struct{}{}
	}

	newCfg := modules.ModuleConfig{
		Name:                          cfg.Name,
		EngineVersion:                 cfg.EngineVersion,
		SDK:                           cfg.SDK,
		Source:                        source,
		Dependencies:                  deps,
		Include:                       append([]string(nil), cfg.Include...),
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
