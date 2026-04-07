package workspace

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/core/modules"
)

// isLocalRef performs a fast heuristic check to determine whether a module
// reference string refers to a local path instead of a git source.
func isLocalRef(source, pin string) bool {
	if pin != "" {
		return false
	}
	if len(source) > 0 && (source[0] == '/' || source[0] == '.') {
		return true
	}
	return !strings.Contains(source, ".")
}

// MigrationResult holds the output of a successful migration.
type MigrationResult struct {
	ProjectRoot         string
	ConfigPath          string
	MigrationReportPath string
	ModuleName          string
	ModuleNewPath       string
	OldSourcePath       string
	LookupSources       []string
	DepRewriteCount     int
	IncRewriteCount     int
	ToolchainCount      int
	BlueprintMigrated   bool
	RemovedFiles        []string
	Warnings            []string
}

// MigrationPlan is the pure filesystem plan for migrating a legacy
// dagger.json project to workspace format.
type MigrationPlan struct {
	Result                   *MigrationResult
	WorkspaceConfigData      []byte
	MigratedModuleConfigData []byte
	MigratedModuleConfigPath string
	SourceCopyPath           string
	SourceCopyDest           string
	RemoveOldSource          bool
	MigrationReportData      []byte
	LockData                 []byte
	GapCount                 int
}

// Summary returns a human-readable summary of the migration.
func (r *MigrationResult) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Migrated to workspace format: %s\n", r.ConfigPath)
	if r.ModuleName != "" {
		fmt.Fprintf(&b, "  Module %q configured at %s\n", r.ModuleName, r.ModuleNewPath)
	}
	if r.OldSourcePath != "" {
		fmt.Fprintf(&b, "  Source moved: %s -> %s\n", r.OldSourcePath, r.ModuleNewPath)
	}
	if r.DepRewriteCount > 0 || r.IncRewriteCount > 0 {
		fmt.Fprintf(&b, "  Rewritten %d dependency path(s), %d include path(s)\n", r.DepRewriteCount, r.IncRewriteCount)
	}
	if r.ToolchainCount > 0 {
		fmt.Fprintf(&b, "  %d toolchain(s) converted to workspace modules\n", r.ToolchainCount)
	}
	if r.BlueprintMigrated {
		b.WriteString("  Blueprint converted to workspace module\n")
	}
	for _, f := range r.RemovedFiles {
		fmt.Fprintf(&b, "  Removed: %s\n", f)
	}
	if len(r.Warnings) > 0 {
		if r.MigrationReportPath != "" {
			fmt.Fprintf(&b, "  Warning: %d migration gap(s) need manual review; see %s\n", len(r.Warnings), r.MigrationReportPath)
		} else {
			for _, w := range r.Warnings {
				fmt.Fprintf(&b, "  Warning: %s\n", w)
			}
		}
	}
	return b.String()
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
	result := &MigrationResult{
		ProjectRoot: compatWorkspace.ProjectRoot,
		ConfigPath:  filepath.Join(compatWorkspace.ProjectRoot, LockDirName, ConfigFileName),
	}

	hasSDK := cfg.SDK != nil && cfg.SDK.Source != ""
	hasNonLocalSource := cfg.Source != "" && cfg.Source != "."
	needsProjectModuleMigration := hasSDK

	plan := &MigrationPlan{
		Result: result,
	}

	if needsProjectModuleMigration {
		modulePath := filepath.Join("modules", cfg.Name)
		result.ModuleName = cfg.Name
		result.ModuleNewPath = filepath.Join(LockDirName, modulePath)

		newJSON, depCount, incCount, err := buildMigratedModuleJSON(cfg, modulePath)
		if err != nil {
			return nil, fmt.Errorf("building migrated module JSON: %w", err)
		}
		result.DepRewriteCount = depCount
		result.IncRewriteCount = incCount
		plan.MigratedModuleConfigData = newJSON
		plan.MigratedModuleConfigPath = filepath.Join(LockDirName, modulePath, ModuleConfigFileName)

		if hasNonLocalSource {
			result.OldSourcePath = cfg.Source
			plan.SourceCopyPath = cfg.Source
			plan.SourceCopyDest = filepath.Join(LockDirName, modulePath)

			newFullPath := filepath.Join(LockDirName, modulePath)
			if strings.HasPrefix(newFullPath+"/", cfg.Source+"/") {
				slog.Warn("old source dir is ancestor of new location; skipping cleanup",
					"oldSource", cfg.Source, "newLocation", newFullPath)
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("old source dir %q is ancestor of new location; skipped cleanup", cfg.Source))
			} else {
				plan.RemoveOldSource = true
			}
		}
	}

	warnings := analyzeCustomizations(cfg.Toolchains)
	plan.GapCount = len(warnings)
	for _, w := range warnings {
		result.Warnings = append(result.Warnings, w.message)
	}

	wsCfg := &Config{Modules: make(map[string]ModuleEntry)}
	if compatWorkspace != nil {
		wsCfg = compatWorkspace.WorkspaceConfig()
	}
	if needsProjectModuleMigration && compatWorkspace != nil && compatWorkspace.MainModule != nil {
		wsCfg.Modules[cfg.Name] = compatWorkspace.MainModule.Entry
	}
	plan.WorkspaceConfigData = SerializeConfig(wsCfg)

	migrateLock := NewLock()
	hasLockEntries := false
	if compatWorkspace != nil {
		for _, mod := range compatWorkspace.Modules {
			if mod.Entry.Blueprint {
				result.BlueprintMigrated = true
			} else {
				result.ToolchainCount++
			}
			addMigrationLookupSource(result, mod.Entry.Source)

			if mod.Pin != "" {
				if err := setMigrationModuleResolvePin(migrateLock, mod.Entry.Source, mod.Pin); err != nil {
					return nil, fmt.Errorf("setting lock for module %q: %w", mod.ConfigName, err)
				}
				hasLockEntries = true
			}
		}
	}
	finalizeMigrationLookupSources(result)

	if len(warnings) > 0 {
		result.MigrationReportPath = filepath.Join(LockDirName, "migration-report.md")
		plan.MigrationReportData = []byte(generateMigrationReportMarkdown(compatWorkspace.ConfigPath, warnings))
	}

	if hasLockEntries {
		lockBytes, err := migrateLock.Marshal()
		if err != nil {
			return nil, fmt.Errorf("serializing workspace lock: %w", err)
		}
		plan.LockData = lockBytes
	}

	return plan, nil
}

// buildMigratedModuleJSON creates the cleaned-up dagger.json for the migrated
// module. It returns the JSON bytes, the number of rewritten dependency paths,
// and the number of rewritten include paths.
func buildMigratedModuleJSON(cfg *modules.ModuleConfig, newModulePath string) ([]byte, int, int, error) {
	depth := len(strings.Split(filepath.ToSlash(newModulePath), "/")) + 1
	prefix := strings.Repeat("../", depth)

	source := ""
	if cfg.Source == "." {
		source = prefix
	}

	deps := make([]*modules.ModuleConfigDependency, 0, len(cfg.Dependencies))
	depRewriteCount := 0
	for _, dep := range cfg.Dependencies {
		if dep == nil {
			continue
		}

		depSource := dep.Source
		if isLocalRef(dep.Source, dep.Pin) {
			depSource = filepath.Join(prefix, dep.Source)
			depRewriteCount++
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
	incRewriteCount := 0
	for _, inc := range cfg.Include {
		if strings.HasPrefix(inc, "!") {
			includes = append(includes, "!"+prefix+inc[1:])
		} else {
			includes = append(includes, prefix+inc)
		}
		incRewriteCount++
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

	out, err := json.MarshalIndent(newCfg, "", "  ")
	if err != nil {
		return nil, 0, 0, err
	}
	out = append(out, '\n')
	return out, depRewriteCount, incRewriteCount, nil
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
	fmt.Fprintf(&b, "Migration completed, but %d legacy configuration item(s) could not be migrated automatically.\n\n", len(warnings))
	b.WriteString("Review the items below and re-apply them manually if they still matter.\n\n")
	fmt.Fprintf(&b, "Legacy config: `%s`\n", filepath.Base(configPath))

	for i, warning := range warnings {
		fmt.Fprintf(&b, "\n## %d. Module `%s`\n\n", i+1, warning.module)
		fmt.Fprintf(&b, "Problem: %s\n", warning.message)
		if origJSON := warning.originalJSON(); origJSON != "" {
			fmt.Fprintf(&b, "\nOriginal legacy customization:\n\n```json\n%s\n```\n", origJSON)
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
				warnings = append(warnings, migrationWarning{
					module: tc.Name,
					message: fmt.Sprintf(
						"function customization for %q could not be migrated automatically because workspace config only carries constructor config values",
						funcName,
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
				msg += " " + strings.Join(parts, " and ") + " customization that cannot be expressed as a workspace config value"
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

func addMigrationLookupSource(result *MigrationResult, source string) {
	if source == "" || isLocalRef(source, "") {
		return
	}
	result.LookupSources = append(result.LookupSources, source)
}

func finalizeMigrationLookupSources(result *MigrationResult) {
	if len(result.LookupSources) < 2 {
		return
	}

	sort.Strings(result.LookupSources)
	compacted := result.LookupSources[:1]
	for _, source := range result.LookupSources[1:] {
		if source != compacted[len(compacted)-1] {
			compacted = append(compacted, source)
		}
	}
	result.LookupSources = compacted
}

func setMigrationModuleResolvePin(lock *Lock, source, pin string) error {
	if source == "" || pin == "" {
		return nil
	}

	existingResult, ok, err := lock.GetModuleResolve(source)
	if err != nil {
		return err
	}
	if ok {
		if existingResult.Value != pin {
			return fmt.Errorf("conflicting pins for source %q: %q vs %q", source, existingResult.Value, pin)
		}
		return nil
	}

	return lock.SetModuleResolve(source, LookupResult{
		Value:  pin,
		Policy: PolicyPin,
	})
}
