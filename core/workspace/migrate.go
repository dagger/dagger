package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/core/modules"
)

// ErrMigrationRequired indicates a dagger.json needs migration to the
// workspace format.
type ErrMigrationRequired struct {
	ConfigPath  string
	ProjectRoot string
}

func (e *ErrMigrationRequired) Error() string {
	return `Migration required: run "dagger migrate" to update this project to the workspace format.`
}

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

// MigrationIO abstracts host file operations needed for migration.
type MigrationIO interface {
	ReadCallerHostFile(ctx context.Context, path string) ([]byte, error)
	LocalFileExport(ctx context.Context, srcPath string, filePath string, destPath string, allowParentDirPath bool) error
	LocalDirExport(ctx context.Context, srcPath string, destPath string, merge bool, removePaths []string) error
	ImportCallerHostDir(ctx context.Context, hostPath string) (string, error)
}

// MigrationResult holds the output of a successful migration.
type MigrationResult struct {
	ProjectRoot       string
	ConfigPath        string
	ModuleName        string
	ModuleNewPath     string
	OldSourcePath     string
	DepRewriteCount   int
	IncRewriteCount   int
	ToolchainCount    int
	BlueprintMigrated bool
	RemovedFiles      []string
	Warnings          []string
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
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "  Warning: %s\n", w)
	}
	return b.String()
}

// Migrate performs the legacy dagger.json -> workspace format migration.
func Migrate(ctx context.Context, bk MigrationIO, migErr *ErrMigrationRequired) (*MigrationResult, error) {
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
		ConfigPath:  filepath.Join(migErr.ProjectRoot, LockDirName, ConfigFileName),
	}

	hasSDK := cfg.SDK != nil && cfg.SDK.Source != ""
	hasNonLocalSource := cfg.Source != "" && cfg.Source != "."
	needsProjectModuleMigration := hasSDK

	migrateLock := NewLock()
	hasLockEntries := false

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

		newJSONPath := filepath.Join(migErr.ProjectRoot, LockDirName, modulePath, ModuleConfigFileName)
		if err := writeHostFile(ctx, bk, newJSONPath, newJSON); err != nil {
			return nil, fmt.Errorf("writing migrated module config: %w", err)
		}

		if hasNonLocalSource {
			srcDir := filepath.Join(migErr.ProjectRoot, cfg.Source)
			destDir := filepath.Join(migErr.ProjectRoot, LockDirName, modulePath)
			result.OldSourcePath = cfg.Source

			if err := copyHostDir(ctx, bk, srcDir, destDir); err != nil {
				return nil, fmt.Errorf("moving source files: %w", err)
			}

			newFullPath := filepath.Join(LockDirName, modulePath)
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

	warnings := analyzeCustomizations(cfg.Toolchains)
	for _, w := range warnings {
		result.Warnings = append(result.Warnings, w.message)
	}

	wsCfg := &Config{
		Modules: make(map[string]ModuleEntry),
	}
	if needsProjectModuleMigration {
		wsCfg.Modules[cfg.Name] = ModuleEntry{
			Source:    filepath.Join("modules", cfg.Name),
			Blueprint: true,
		}
	}

	for _, tc := range cfg.Toolchains {
		source := tc.Source
		if isLocalRef(tc.Source, tc.Pin) {
			source = filepath.Join("..", tc.Source)
		}
		wsCfg.Modules[tc.Name] = ModuleEntry{
			Source:            source,
			Config:            extractConfigDefaults(tc.Customizations),
			LegacyDefaultPath: true,
		}
		result.ToolchainCount++

		if tc.Pin != "" {
			if err := setMigrationModuleResolvePin(migrateLock, source, tc.Pin); err != nil {
				return nil, fmt.Errorf("setting lock for toolchain %q: %w", tc.Name, err)
			}
			hasLockEntries = true
		}
	}

	if cfg.Blueprint != nil {
		source := cfg.Blueprint.Source
		if isLocalRef(cfg.Blueprint.Source, cfg.Blueprint.Pin) {
			source = filepath.Join("..", cfg.Blueprint.Source)
		}
		name := cfg.Blueprint.Name
		if name == "" {
			name = "blueprint"
		}
		wsCfg.Modules[name] = ModuleEntry{
			Source:            source,
			Blueprint:         true,
			LegacyDefaultPath: true,
		}
		result.BlueprintMigrated = true

		if cfg.Blueprint.Pin != "" {
			if err := setMigrationModuleResolvePin(migrateLock, source, cfg.Blueprint.Pin); err != nil {
				return nil, fmt.Errorf("setting lock for blueprint %q: %w", name, err)
			}
			hasLockEntries = true
		}
	}

	configContent := generateMigrationConfigTOML(wsCfg, warnings)
	configPath := filepath.Join(migErr.ProjectRoot, LockDirName, ConfigFileName)
	if err := writeHostFile(ctx, bk, configPath, []byte(configContent)); err != nil {
		return nil, fmt.Errorf("writing workspace config: %w", err)
	}

	if hasLockEntries {
		lockBytes, err := migrateLock.Marshal()
		if err != nil {
			return nil, fmt.Errorf("serializing workspace lock: %w", err)
		}
		lockPath := filepath.Join(migErr.ProjectRoot, LockDirName, LockFileName)
		if err := writeHostFile(ctx, bk, lockPath, lockBytes); err != nil {
			return nil, fmt.Errorf("writing workspace lock: %w", err)
		}
	}

	if err := deleteHostFile(ctx, bk, migErr.ConfigPath); err != nil {
		return nil, fmt.Errorf("removing legacy config: %w", err)
	}
	result.RemovedFiles = append(result.RemovedFiles, ModuleConfigFileName)

	return result, nil
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

// generateMigrationConfigTOML builds the .dagger/config.toml content using
// hand-written TOML so warning comments stay attached to the migrated entries.
func generateMigrationConfigTOML(cfg *Config, warnings []migrationWarning) string {
	var b strings.Builder

	warningsByModule := make(map[string][]migrationWarning)
	for _, w := range warnings {
		warningsByModule[w.module] = append(warningsByModule[w.module], w)
	}

	b.WriteString("[modules]\n")

	moduleNames := make([]string, 0, len(cfg.Modules))
	for name := range cfg.Modules {
		moduleNames = append(moduleNames, name)
	}
	sort.Strings(moduleNames)

	for _, name := range moduleNames {
		entry := cfg.Modules[name]
		b.WriteString("\n")
		for _, w := range warningsByModule[name] {
			b.WriteString(w.tomlComment())
		}

		fmt.Fprintf(&b, "[modules.%s]\n", name)
		fmt.Fprintf(&b, "source = %q\n", entry.Source)
		if entry.Blueprint {
			b.WriteString("blueprint = true\n")
		}
		if entry.LegacyDefaultPath {
			b.WriteString("legacy-default-path = true\n")
		}

		if len(entry.Config) == 0 {
			continue
		}

		configKeys := make([]string, 0, len(entry.Config))
		for key := range entry.Config {
			configKeys = append(configKeys, key)
		}
		sort.Strings(configKeys)

		fmt.Fprintf(&b, "\n[modules.%s.config]\n", name)
		for _, key := range configKeys {
			fmt.Fprintf(&b, "%s = %s\n", key, formatConfigValue(entry.Config[key]))
		}
	}

	return b.String()
}

type migrationWarning struct {
	module   string
	message  string
	original *modules.ModuleConfigArgument
}

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
						"customization for function %q could not be migrated (non-constructor)",
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
				msg += " " + strings.Join(parts, " and ") + " customization that cannot be expressed as a config value"
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
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	fileName := filepath.Base(destPath)
	if err := bk.LocalFileExport(ctx, tmpFile.Name(), fileName, destPath, true); err != nil {
		return fmt.Errorf("export file: %w", err)
	}
	return nil
}

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

func deleteHostDir(ctx context.Context, bk MigrationIO, dirPath string) error {
	parentDir := filepath.Dir(dirPath)
	dirName := filepath.Base(dirPath) + "/"

	tmpDir, err := os.MkdirTemp("", "dagger-migrate-empty-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	return bk.LocalDirExport(ctx, tmpDir, parentDir, true, []string{dirName})
}

// LocalMigrationIO implements MigrationIO using direct local filesystem
// operations.
type LocalMigrationIO struct{}

func (LocalMigrationIO) ReadCallerHostFile(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (LocalMigrationIO) LocalFileExport(_ context.Context, srcPath, _ string, destPath string, allowParentDirPath bool) error {
	if allowParentDirPath {
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return err
		}
	}
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	return os.WriteFile(destPath, data, 0o644)
}

func (LocalMigrationIO) LocalDirExport(_ context.Context, srcPath, destPath string, merge bool, removePaths []string) error {
	for _, p := range removePaths {
		target := filepath.Join(destPath, p)
		if err := os.RemoveAll(target); err != nil {
			return err
		}
	}
	if !merge {
		if err := os.RemoveAll(destPath); err != nil {
			return err
		}
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
			return os.MkdirAll(target, 0o755)
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
