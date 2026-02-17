package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/util/parallel"
	"go.opentelemetry.io/otel/attribute"
)

// MigrationIO abstracts host file operations needed for migration.
type MigrationIO interface {
	ReadCallerHostFile(ctx context.Context, path string) ([]byte, error)
	LocalFileExport(ctx context.Context, srcPath string, filePath string, destPath string, allowParentDirPath bool) error
	LocalDirExport(ctx context.Context, srcPath string, destPath string, merge bool, removePaths []string) error
	ImportCallerHostDir(ctx context.Context, hostPath string) (string, error)
}

// MigrationResult holds the output of a successful migration.
type MigrationResult struct {
	ProjectRoot     string
	ConfigPath      string   // path to new .dagger/config.toml
	ModuleName      string   // name of migrated project module (if any)
	ModuleNewPath   string   // new location of module config
	OldSourcePath   string   // original source path (for moved modules)
	DepRewriteCount int      // number of dependency paths rewritten
	IncRewriteCount int      // number of include paths rewritten
	ToolchainCount  int      // number of toolchains converted
	RemovedFiles    []string // files removed during migration
	Warnings        []string // non-fatal warnings
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
// If introspect is non-nil, it is called for each toolchain to discover
// constructor arguments and generate commented-out config hints.
func Migrate(ctx context.Context, bk MigrationIO, migErr *ErrMigrationRequired, introspect ConstructorIntrospector) (*MigrationResult, error) {
	// 1. Read and parse legacy config
	data, err := bk.ReadCallerHostFile(ctx, migErr.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("reading legacy config: %w", err)
	}

	cfg, err := parseLegacyConfig(data)
	if err != nil {
		return nil, err
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
		newJSON, depCount, incCount, err := buildMigratedModuleJSON(cfg, modulePath)
		if err != nil {
			return nil, fmt.Errorf("building migrated module JSON: %w", err)
		}
		result.DepRewriteCount = depCount
		result.IncRewriteCount = incCount

		newJSONPath := filepath.Join(migErr.ProjectRoot, WorkspaceDirName, modulePath, "dagger.json")
		if err := writeHostFile(ctx, bk, newJSONPath, newJSON); err != nil {
			return nil, fmt.Errorf("writing migrated module config: %w", err)
		}

		// Move source files to new location when source != "."
		if hasNonLocalSource {
			srcDir := filepath.Join(migErr.ProjectRoot, cfg.Source)
			destDir := filepath.Join(migErr.ProjectRoot, WorkspaceDirName, modulePath)
			result.OldSourcePath = cfg.Source

			if err := copyHostDir(ctx, bk, srcDir, destDir); err != nil {
				return nil, fmt.Errorf("moving source files: %w", err)
			}

			// Clean up old source directory, with ancestor safety check:
			// skip delete if old source is an ancestor of the new location.
			newFullPath := filepath.Join(WorkspaceDirName, modulePath)
			if strings.HasPrefix(newFullPath+"/", cfg.Source+"/") {
				slog.Warn("old source dir is ancestor of new location; skipping cleanup",
					"oldSource", cfg.Source, "newLocation", newFullPath)
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("old source dir %q is ancestor of new location; skipped cleanup", cfg.Source))
			} else {
				if err := deleteHostDir(ctx, bk, srcDir); err != nil {
					slog.Warn("could not remove old source directory",
						"path", srcDir, "error", err)
					result.Warnings = append(result.Warnings,
						fmt.Sprintf("could not remove old source directory %q: %v", cfg.Source, err))
				} else {
					result.RemovedFiles = append(result.RemovedFiles, cfg.Source+"/")
				}
			}
		}
	}

	// 3b. Handle toolchains -> workspace modules
	warnings := analyzeCustomizations(cfg.Toolchains)
	for _, w := range warnings {
		result.Warnings = append(result.Warnings, w.message)
	}

	for _, tc := range cfg.Toolchains {
		source := tc.Source
		if core.FastModuleSourceKindCheck(tc.Source, tc.Pin) == core.ModuleSourceKindLocal {
			source = filepath.Join("..", tc.Source)
		}
		entry := ModuleEntry{
			Source: source,
			Config: extractConfigDefaults(tc.Customizations),
		}
		wsCfg.Modules[tc.Name] = entry
		result.ToolchainCount++
	}

	// 3c. Introspect constructor args for config hints (in parallel)
	var allHints map[string][]ConstructorArgHint
	if introspect != nil {
		allHints = make(map[string][]ConstructorArgHint)
		var mu sync.Mutex
		jobs := parallel.New().WithContextualTracer(true)
		for _, tc := range cfg.Toolchains {
			jobs = jobs.WithJob(fmt.Sprintf("introspecting %s", tc.Source), func(ctx context.Context) error {
				hints, err := introspect(ctx, tc.Source)
				if err != nil {
					slog.Warn("could not introspect constructor args for config hints",
						"toolchain", tc.Name, "error", err)
					return nil
				}
				if len(hints) > 0 {
					mu.Lock()
					allHints[tc.Name] = hints
					mu.Unlock()
				}
				return nil
			}, attribute.String("toolchain.ref", tc.Source))
		}
		if err := jobs.Run(ctx); err != nil {
			return nil, fmt.Errorf("introspecting toolchains: %w", err)
		}
	}

	// 4. Write .dagger/config.toml
	configContent := generateMigrationConfigTOML(cfg, warnings, allHints)
	configPath := filepath.Join(migErr.ProjectRoot, WorkspaceDirName, ConfigFileName)
	if err := writeHostFile(ctx, bk, configPath, []byte(configContent)); err != nil {
		return nil, fmt.Errorf("writing workspace config: %w", err)
	}

	// 5. Delete root dagger.json
	if err := deleteHostFile(ctx, bk, migErr.ConfigPath); err != nil {
		return nil, fmt.Errorf("removing legacy config: %w", err)
	}
	result.RemovedFiles = append(result.RemovedFiles, "dagger.json")

	return result, nil
}

// buildMigratedModuleJSON creates the cleaned-up dagger.json for the migrated module.
// Source and Toolchains are removed; dependency/include paths are rewritten
// relative to the new module location.
// Returns the JSON bytes, the number of dependency paths rewritten, and the
// number of include paths rewritten.
func buildMigratedModuleJSON(cfg *legacyConfig, newModulePath string) ([]byte, int, int, error) {
	// Relative prefix to rewrite paths from the new module location back to project root.
	// newModulePath is relative to .dagger/ (e.g. "modules/myapp"), so the full path
	// from the project root is .dagger/modules/myapp/ — that's 3 levels up: ../../../
	depth := len(strings.Split(newModulePath, "/")) + 1 // +1 for .dagger/ directory
	prefix := strings.Repeat("../", depth)

	// For source=".", the code stays at project root. The new dagger.json
	// at .dagger/modules/<name>/ needs to point back there.
	source := ""
	if cfg.Source == "." {
		source = prefix
	}

	// Rewrite dependency paths (only local paths need adjusting; git refs stay as-is)
	var deps []*legacyDependency
	depRewriteCount := 0
	for _, dep := range cfg.Dependencies {
		source := dep.Source
		if core.FastModuleSourceKindCheck(dep.Source, dep.Pin) == core.ModuleSourceKindLocal {
			source = filepath.Join(prefix, dep.Source)
			depRewriteCount++
		}
		newDep := &legacyDependency{
			Name:   dep.Name,
			Source: source,
			Pin:    dep.Pin,
		}
		deps = append(deps, newDep)
	}

	// Rewrite include paths
	var includes []string
	incRewriteCount := 0
	for _, inc := range cfg.Include {
		if strings.HasPrefix(inc, "!") {
			includes = append(includes, "!"+prefix+inc[1:])
		} else {
			includes = append(includes, prefix+inc)
		}
		incRewriteCount++
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
		return nil, 0, 0, err
	}
	out = append(out, '\n')
	return out, depRewriteCount, incRewriteCount, nil
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

	b.WriteString("[modules]\n")

	// Project module entry (if there is an SDK)
	if cfg.SDK != nil && cfg.SDK.Source != "" {
		b.WriteString("\n")
		fmt.Fprintf(&b, "[modules.%s]\n", cfg.Name)
		fmt.Fprintf(&b, "source = \"modules/%s\"\n", cfg.Name)
		b.WriteString("alias = true\n")
	}

	// Toolchain entries
	for _, tc := range cfg.Toolchains {
		b.WriteString("\n")

		// Add warning comments before the section header
		for _, w := range warningsByTC[tc.Name] {
			b.WriteString(w.tomlComment())
		}
		fmt.Fprintf(&b, "[modules.%s]\n", tc.Name)
		source := tc.Source
		if core.FastModuleSourceKindCheck(tc.Source, tc.Pin) == core.ModuleSourceKindLocal {
			source = filepath.Join("..", tc.Source)
		}
		fmt.Fprintf(&b, "source = %q\n", source)

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
				hintLines = append(hintLines, fmt.Sprintf("# %s = %s%s\n",
					hint.Name, hint.ExampleValue, hint.CommentSuffix()))
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

// copyHostDir copies a directory from one host location to another via the engine.
// It imports the source directory into a temp dir, then exports it to the destination.
func copyHostDir(ctx context.Context, bk MigrationIO, srcPath, destPath string) error {
	tmpDir, err := bk.ImportCallerHostDir(ctx, srcPath)
	if err != nil {
		return fmt.Errorf("import source dir %q: %w", srcPath, err)
	}
	defer os.RemoveAll(tmpDir)

	if err := bk.LocalDirExport(ctx, tmpDir, destPath, true, nil); err != nil {
		return fmt.Errorf("export to dest dir %q: %w", destPath, err)
	}
	return nil
}

// deleteHostDir deletes a directory on the host via LocalDirExport with removePaths.
// Uses trailing "/" convention to trigger os.RemoveAll on the client side.
func deleteHostDir(ctx context.Context, bk MigrationIO, dirPath string) error {
	parentDir := filepath.Dir(dirPath)
	dirName := filepath.Base(dirPath) + "/" // trailing "/" triggers os.RemoveAll

	tmpDir, err := os.MkdirTemp("", "dagger-migrate-empty-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	return bk.LocalDirExport(ctx, tmpDir, parentDir, true, []string{dirName})
}

// LocalMigrationIO implements MigrationIO using direct filesystem operations.
// Used by the CLI to perform migration without needing the engine.
type LocalMigrationIO struct{}

func (LocalMigrationIO) ReadCallerHostFile(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (LocalMigrationIO) LocalFileExport(_ context.Context, srcPath, _ string, destPath string, allowParentDirPath bool) error {
	if allowParentDirPath {
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}
	}
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	return os.WriteFile(destPath, data, 0644)
}

func (LocalMigrationIO) LocalDirExport(_ context.Context, srcPath, destPath string, merge bool, removePaths []string) error {
	for _, p := range removePaths {
		target := filepath.Join(destPath, p)
		os.RemoveAll(target)
	}
	if !merge {
		os.RemoveAll(destPath)
	}
	return copyDirLocal(srcPath, destPath)
}

func (LocalMigrationIO) ImportCallerHostDir(_ context.Context, hostPath string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "dagger-migrate-import-*")
	if err != nil {
		return "", err
	}
	if err := copyDirLocal(hostPath, tmpDir); err != nil {
		os.RemoveAll(tmpDir)
		return "", err
	}
	return tmpDir, nil
}

// copyDirLocal recursively copies a directory.
func copyDirLocal(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
