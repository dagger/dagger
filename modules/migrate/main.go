// Migration tool for converting legacy dagger.json projects to the new workspace format.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"dagger/migrate/internal/dagger"
)

// Migrate converts legacy Dagger projects to the new workspace format.
type Migrate struct{}

// Migrate a legacy dagger.json project to the new workspace format (.dagger/config.toml).
//
// Returns a Changeset representing all the file changes needed.
func (m *Migrate) Migrate(
	ctx context.Context,
	// The project root directory containing a legacy dagger.json.
	// +defaultPath="/"
	// +ignore=["*", "!**/dagger.json", "!**/.dagger"]
	source *dagger.Directory,
) (*dagger.Changeset, error) {
	// 1. Parse dagger.json
	configJSON, err := source.File("dagger.json").Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading dagger.json: %w", err)
	}

	var cfg LegacyConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, fmt.Errorf("parsing dagger.json: %w", err)
	}

	// Normalize: if SDK is set but Source isn't, Source was implicitly "."
	if cfg.SDK != nil && cfg.SDK.Source != "" && cfg.Source == "" {
		cfg.Source = "."
	}

	// 2. Detect triggers
	hasSDK := cfg.SDK != nil && cfg.SDK.Source != ""
	hasNonLocalSource := cfg.Source != "" && cfg.Source != "."
	hasToolchains := len(cfg.Toolchains) > 0
	needsProjectModuleMigration := hasSDK && (hasNonLocalSource || hasToolchains)

	if !needsProjectModuleMigration && !hasToolchains {
		// Nothing to migrate — return empty changeset
		return source.Changes(source), nil
	}

	// 3. Build the "after" directory
	after := source
	report := &reportBuilder{}

	// Analyze toolchain customizations for warnings
	warnings := analyzeCustomizations(cfg.Toolchains)
	report.setWarnings(warnings)

	// Project module migration
	if needsProjectModuleMigration {
		after, err = migrateProjectModule(ctx, after, &cfg, report)
		if err != nil {
			return nil, fmt.Errorf("migrating project module: %w", err)
		}
	}

	// Remove root dagger.json
	after = after.WithoutFile("dagger.json")
	report.addRemovedFile("dagger.json")

	// Generate .dagger/config.toml
	tomlContent := generateConfigTOML(&cfg, warnings)
	after = after.WithNewFile(".dagger/config.toml", tomlContent)

	// Record toolchain entries in the report
	warningsByToolchain := make(map[string]int)
	for _, w := range warnings {
		warningsByToolchain[w.toolchain]++
	}
	for _, tc := range cfg.Toolchains {
		report.addToolchain(tc.Name, "../"+tc.Source, warningsByToolchain[tc.Name])
	}

	// 4. Write migration report
	reportContent := report.String()
	fmt.Println(reportContent)
	after = after.WithNewFile(".dagger/migration-report.md", reportContent)

	// 5. Return changeset
	return after.Changes(source), nil
}

// migrateProjectModule moves the module source to .dagger/modules/<name>/ and
// rewrites the dagger.json inside it.
func migrateProjectModule(
	ctx context.Context,
	dir *dagger.Directory,
	cfg *LegacyConfig,
	report *reportBuilder,
) (*dagger.Directory, error) {
	newModulePath := ".dagger/modules/" + cfg.Name
	oldSourcePath := cfg.Source

	report.setProjectModule(cfg.Name, oldSourcePath, newModulePath)

	// Extract module source from its current location
	var moduleDir *dagger.Directory
	if oldSourcePath == "." {
		// Source is in the project root itself — take the whole tree
		// (the new dagger.json will be written at the new location)
		moduleDir = dir
	} else {
		moduleDir = dir.Directory(oldSourcePath)
	}

	// Place module source at new location
	dir = dir.WithDirectory(newModulePath, moduleDir)

	// Remove old source files. We can't use WithoutDirectory when the old source
	// is a parent/ancestor of the new location (e.g. old=".dagger/", new=".dagger/modules/<name>/").
	// Instead, enumerate entries and remove them individually.
	if oldSourcePath != "." {
		entries, err := moduleDir.Entries(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing old source entries: %w", err)
		}
		for _, entry := range entries {
			entryPath := path.Join(oldSourcePath, entry)
			// Don't remove the "modules" directory if it's inside the old source path,
			// since that's where we're putting the new module.
			if strings.HasPrefix(path.Join(newModulePath)+"/", path.Join(oldSourcePath, entry)+"/") {
				continue
			}
			// Try removing as both file and directory (we don't know which it is)
			dir = dir.WithoutFile(entryPath).WithoutDirectory(entryPath)
		}
	}

	// Build updated dagger.json for the module at its new location
	newJSON, depCount, includeCount, err := buildNewModuleJSON(cfg, newModulePath)
	if err != nil {
		return nil, fmt.Errorf("building new dagger.json: %w", err)
	}
	report.setRewrittenDeps(depCount)
	report.setRewrittenIncludes(includeCount)

	// Write updated dagger.json at new module location
	dir = dir.WithNewFile(path.Join(newModulePath, "dagger.json"), string(newJSON))

	return dir, nil
}

// buildNewModuleJSON creates the cleaned-up dagger.json for the migrated module.
// Returns the JSON bytes, count of rewritten dep paths, and count of rewritten include paths.
func buildNewModuleJSON(cfg *LegacyConfig, newModulePath string) ([]byte, int, int, error) {
	// Relative prefix to rewrite paths from the new module location back to project root.
	// Per the design proposal, deps and includes use "../../" (from .dagger/modules/<name>/).
	prefix := "../../"

	// Rewrite dependency paths
	var deps []*LegacyDependency
	for _, dep := range cfg.Dependencies {
		newDep := &LegacyDependency{
			Name:   dep.Name,
			Source: prefix + dep.Source,
			Pin:    dep.Pin,
			// Customizations are not carried into dependencies
		}
		deps = append(deps, newDep)
	}

	// Rewrite include paths
	var includes []string
	for _, inc := range cfg.Include {
		// Preserve negation prefix
		if strings.HasPrefix(inc, "!") {
			includes = append(includes, "!"+prefix+inc[1:])
		} else {
			includes = append(includes, prefix+inc)
		}
	}

	newCfg := NewModuleJSON{
		Name:         cfg.Name,
		SDK:          cfg.SDK,
		Dependencies: deps,
		Include:      includes,
		Codegen:      cfg.Codegen,
		Clients:      cfg.Clients,
	}

	out, err := json.MarshalIndent(newCfg, "", "  ")
	if err != nil {
		return nil, 0, 0, err
	}
	// Append trailing newline
	out = append(out, '\n')
	return out, len(deps), len(includes), nil
}
