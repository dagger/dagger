package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// MigrationIO abstracts host file operations needed for migration.
type MigrationIO interface {
	ReadCallerHostFile(ctx context.Context, path string) ([]byte, error)
	LocalFileExport(ctx context.Context, srcPath string, filePath string, destPath string, allowParentDirPath bool) error
	LocalDirExport(ctx context.Context, srcPath string, destPath string, merge bool, removePaths []string) error
}

// MigrationResult holds the output of a successful migration.
type MigrationResult struct {
	ProjectRoot    string
	ConfigPath     string   // path to new .dagger/config.toml
	ModuleName     string   // name of migrated project module (if any)
	ModuleNewPath  string   // new location of module config
	ToolchainCount int      // number of toolchains converted
	RemovedFiles   []string // files removed during migration
	Warnings       []string // non-fatal warnings
}

// Summary returns a human-readable summary of the migration.
func (r *MigrationResult) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Migrated to workspace format: %s\n", r.ConfigPath)
	if r.ModuleName != "" {
		fmt.Fprintf(&b, "  Module %q configured at %s\n", r.ModuleName, r.ModuleNewPath)
	}
	if r.ToolchainCount > 0 {
		fmt.Fprintf(&b, "  %d toolchain(s) converted to workspace modules\n", r.ToolchainCount)
	}
	for _, f := range r.RemovedFiles {
		fmt.Fprintf(&b, "  Removed: %s\n", f)
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "  Warning: %s\n", w)
	}
	return b.String()
}

// ConstructorIntrospector loads a module by reference and returns its constructor
// argument hints for generating commented-out config entries.
// Returns nil if the module has no constructor or introspection is not possible.
type ConstructorIntrospector func(ctx context.Context, ref string) ([]ConstructorArgHint, error)

// Migrate performs the legacy dagger.json -> workspace format migration.
// Called when AutoMigrate is set and ErrMigrationRequired was detected.
// If introspect is non-nil, it is called for each toolchain to discover
// constructor arguments and generate commented-out config hints.
func Migrate(ctx context.Context, bk MigrationIO, migErr *ErrMigrationRequired, introspect ConstructorIntrospector) (*MigrationResult, error) {
	// 1. Read and parse legacy config
	data, err := bk.ReadCallerHostFile(ctx, migErr.LegacyConfigPath)
	if err != nil {
		return nil, fmt.Errorf("reading legacy config: %w", err)
	}

	var cfg legacyConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing legacy config: %w", err)
	}

	// Normalize: if SDK is set but Source isn't, Source was implicitly "."
	if cfg.SDK != nil && cfg.SDK.Source != "" && cfg.Source == "" {
		cfg.Source = "."
	}

	result := &MigrationResult{
		ProjectRoot: migErr.ProjectRoot,
		ConfigPath:  filepath.Join(migErr.ProjectRoot, WorkspaceDirName, ConfigFileName),
	}

	// 2. Detect what needs migration
	hasSDK := cfg.SDK != nil && cfg.SDK.Source != ""
	hasNonLocalSource := cfg.Source != "" && cfg.Source != "."
	hasToolchains := len(cfg.Toolchains) > 0
	needsProjectModuleMigration := hasSDK && (hasNonLocalSource || hasToolchains)

	// 3. Build workspace config
	wsCfg := &Config{
		Modules: make(map[string]ModuleEntry),
	}

	// 3a. Handle project module
	if needsProjectModuleMigration {
		modulePath := "modules/" + cfg.Name
		result.ModuleName = cfg.Name
		result.ModuleNewPath = filepath.Join(WorkspaceDirName, modulePath)

		wsCfg.Modules[cfg.Name] = ModuleEntry{
			Source: modulePath,
			Alias:  true,
		}

		// Build new dagger.json for the module at its new location
		newJSON, err := buildMigratedModuleJSON(&cfg, modulePath)
		if err != nil {
			return nil, fmt.Errorf("building migrated module JSON: %w", err)
		}

		newJSONPath := filepath.Join(migErr.ProjectRoot, WorkspaceDirName, modulePath, "dagger.json")
		if err := writeHostFile(ctx, bk, newJSONPath, newJSON); err != nil {
			return nil, fmt.Errorf("writing migrated module config: %w", err)
		}
	}

	// 3b. Handle toolchains -> workspace modules
	warnings := analyzeCustomizations(cfg.Toolchains)
	for _, w := range warnings {
		result.Warnings = append(result.Warnings, w.message)
	}

	for _, tc := range cfg.Toolchains {
		entry := ModuleEntry{
			Source: "../" + tc.Source,
		}
		// Migrate constructor customizations to config entries
		config := make(map[string]string)
		for _, cust := range tc.Customizations {
			if cust.IsConstructor() && cust.Default != "" {
				config[cust.Argument] = cust.Default
			}
		}
		if len(config) > 0 {
			entry.Config = config
		}
		wsCfg.Modules[tc.Name] = entry
		result.ToolchainCount++
	}

	// 3c. Introspect constructor args for config hints
	var allHints map[string][]ConstructorArgHint
	if introspect != nil {
		allHints = make(map[string][]ConstructorArgHint)
		for _, tc := range cfg.Toolchains {
			hints, err := introspect(ctx, tc.Source)
			if err != nil {
				slog.Warn("could not introspect constructor args for config hints",
					"toolchain", tc.Name, "error", err)
				continue
			}
			if len(hints) > 0 {
				allHints[tc.Name] = hints
			}
		}
	}

	// 4. Write .dagger/config.toml
	configContent := generateMigrationConfigTOML(&cfg, warnings, allHints)
	configPath := filepath.Join(migErr.ProjectRoot, WorkspaceDirName, ConfigFileName)
	if err := writeHostFile(ctx, bk, configPath, []byte(configContent)); err != nil {
		return nil, fmt.Errorf("writing workspace config: %w", err)
	}

	// 5. Delete root dagger.json
	if err := deleteHostFile(ctx, bk, migErr.LegacyConfigPath); err != nil {
		return nil, fmt.Errorf("removing legacy config: %w", err)
	}
	result.RemovedFiles = append(result.RemovedFiles, "dagger.json")

	return result, nil
}

// buildMigratedModuleJSON creates the cleaned-up dagger.json for the migrated module.
// Source and Toolchains are removed; dependency/include paths are rewritten
// relative to the new module location.
func buildMigratedModuleJSON(cfg *legacyConfig, newModulePath string) ([]byte, error) {
	// Relative prefix to rewrite paths from the new module location back to project root.
	// From .dagger/modules/<name>/, that's 3 levels up: ../../../
	depth := len(strings.Split(newModulePath, "/"))
	prefix := strings.Repeat("../", depth)

	// For source=".", the code stays at project root. The new dagger.json
	// at .dagger/modules/<name>/ needs to point back there.
	source := ""
	if cfg.Source == "." {
		source = prefix
	}

	// Rewrite dependency paths
	var deps []*legacyDependency
	for _, dep := range cfg.Dependencies {
		newDep := &legacyDependency{
			Name:   dep.Name,
			Source: prefix + dep.Source,
			Pin:    dep.Pin,
		}
		deps = append(deps, newDep)
	}

	// Rewrite include paths
	var includes []string
	for _, inc := range cfg.Include {
		if strings.HasPrefix(inc, "!") {
			includes = append(includes, "!"+prefix+inc[1:])
		} else {
			includes = append(includes, prefix+inc)
		}
	}

	newCfg := newModuleJSON{
		Name:         cfg.Name,
		SDK:          cfg.SDK,
		Source:       source,
		Dependencies: deps,
		Include:      includes,
		Codegen:      cfg.Codegen,
		Clients:      cfg.Clients,
	}

	out, err := json.MarshalIndent(newCfg, "", "  ")
	if err != nil {
		return nil, err
	}
	out = append(out, '\n')
	return out, nil
}

// generateMigrationConfigTOML builds the .dagger/config.toml content.
// Uses hand-built TOML for precise control over warning comments.
// If hints is non-nil, commented-out constructor arg entries are added
// for each toolchain (matching the behavior of 'dagger install').
func generateMigrationConfigTOML(cfg *legacyConfig, warnings []migrationWarning, hints map[string][]ConstructorArgHint) string {
	var b strings.Builder

	// Build warning lookup by toolchain name
	warningsByTC := make(map[string][]migrationWarning)
	for _, w := range warnings {
		warningsByTC[w.toolchain] = append(warningsByTC[w.toolchain], w)
	}

	needsBlank := false

	// Project module entry (if there is an SDK)
	if cfg.SDK != nil && cfg.SDK.Source != "" {
		fmt.Fprintf(&b, "[modules.%s]\n", cfg.Name)
		fmt.Fprintf(&b, "source = \"modules/%s\"\n", cfg.Name)
		b.WriteString("alias = true\n")
		needsBlank = true
	}

	// Toolchain entries
	for _, tc := range cfg.Toolchains {
		if needsBlank {
			b.WriteString("\n")
		}
		// Add warning comments before the section header
		for _, w := range warningsByTC[tc.Name] {
			b.WriteString(w.tomlComment())
		}
		fmt.Fprintf(&b, "[modules.%s]\n", tc.Name)
		fmt.Fprintf(&b, "source = \"../%s\"\n", tc.Source)

		// Collect config values from customizations
		var configEntries []string
		for _, cust := range tc.Customizations {
			if cust.IsConstructor() && cust.Default != "" {
				configEntries = append(configEntries, fmt.Sprintf("%s = %q\n", cust.Argument, cust.Default))
			}
		}

		// Collect commented-out constructor arg hints (from introspection)
		var hintLines []string
		if argHints, ok := hints[tc.Name]; ok {
			for _, hint := range argHints {
				if hasCustomization(tc, hint.Name) {
					continue
				}
				hintLines = append(hintLines, fmt.Sprintf("# %s = %s # %s\n",
					hint.Name, hint.ExampleValue, hint.TypeLabel))
			}
		}

		// Write [modules.<name>.config] section if there are config entries or hints
		if len(configEntries) > 0 || len(hintLines) > 0 {
			fmt.Fprintf(&b, "\n[modules.%s.config]\n", tc.Name)
			for _, entry := range configEntries {
				b.WriteString(entry)
			}
			for _, line := range hintLines {
				b.WriteString(line)
			}
		}
		needsBlank = true
	}

	return b.String()
}

// hasCustomization checks if a toolchain already has a customization for the given arg name.
func hasCustomization(tc *legacyDependency, argName string) bool {
	for _, cust := range tc.Customizations {
		if cust.Argument == argName {
			return true
		}
	}
	return false
}

// migrationWarning represents a warning about a non-migratable customization.
type migrationWarning struct {
	toolchain string
	message   string
	original  *legacyCustomization
}

// tomlComment formats a warning as a TOML comment block.
func (w migrationWarning) tomlComment() string {
	var b strings.Builder
	b.WriteString("# WARNING: ")
	b.WriteString(w.message)
	b.WriteString("\n")
	if w.original != nil {
		origJSON, _ := json.Marshal(w.original)
		b.WriteString("# Original: ")
		b.Write(origJSON)
		b.WriteString("\n")
	}
	return b.String()
}

// analyzeCustomizations inspects toolchain customizations and returns
// warnings for those that can't be migrated to config.toml.
func analyzeCustomizations(toolchains []*legacyDependency) []migrationWarning {
	var warnings []migrationWarning
	for _, tc := range toolchains {
		for _, cust := range tc.Customizations {
			if !cust.IsConstructor() {
				funcName := strings.Join(cust.Function, ".")
				warnings = append(warnings, migrationWarning{
					toolchain: tc.Name,
					message: fmt.Sprintf(
						"customization for function %q could not be migrated (non-constructor)",
						funcName,
					),
					original: cust,
				})
				continue
			}
			if len(cust.Ignore) > 0 || cust.DefaultPath != "" {
				msg := fmt.Sprintf("constructor arg %q has", cust.Argument)
				var parts []string
				if len(cust.Ignore) > 0 {
					parts = append(parts, "'ignore'")
				}
				if cust.DefaultPath != "" {
					parts = append(parts, "'defaultPath'")
				}
				msg += " " + strings.Join(parts, " and ") + " customization that cannot be expressed as a config value"
				warnings = append(warnings, migrationWarning{
					toolchain: tc.Name,
					message:   msg,
					original:  cust,
				})
			}
		}
	}
	return warnings
}

// writeHostFile writes data to a file on the host via temp file + LocalFileExport.
func writeHostFile(ctx context.Context, bk MigrationIO, destPath string, data []byte) error {
	tmpFile, err := os.CreateTemp("", "dagger-migrate-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()

	fileName := filepath.Base(destPath)
	if err := bk.LocalFileExport(ctx, tmpFile.Name(), fileName, destPath, true); err != nil {
		return fmt.Errorf("export file: %w", err)
	}
	return nil
}

// deleteHostFile deletes a file on the host via LocalDirExport with removePaths.
func deleteHostFile(ctx context.Context, bk MigrationIO, filePath string) error {
	dir := filepath.Dir(filePath)
	fileName := filepath.Base(filePath)

	tmpDir, err := os.MkdirTemp("", "dagger-migrate-empty-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	return bk.LocalDirExport(ctx, tmpDir, dir, true, []string{fileName})
}
