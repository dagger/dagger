package workspace

import (
	"context"
	"fmt"
	"path"
	"path/filepath"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/core"
)

const (
	// WorkspaceDirName is the name of the workspace directory.
	WorkspaceDirName = ".dagger"

	// ConfigFileName is the name of the workspace config file within .dagger/.
	ConfigFileName = "config.toml"

	// ModuleConfigFileName is the module config filename.
	ModuleConfigFileName = "dagger.json"
)

// Workspace represents a detected workspace with its root directory and config.
type Workspace struct {
	// Root is the outer filesystem boundary (git root, or workspace dir if no git).
	Root string

	// Path is the workspace location relative to Root (e.g., "apps/frontend" or ".").
	Path string

	// Initialized is true if .dagger/config.toml was found.
	Initialized bool

	Config *Config // parsed config (nil if no config.toml)
}

// Detect finds the workspace root and config from the given working directory.
//
// Uses a 3-step fallback chain:
//  1. FindUp .dagger/ → stat .dagger/config.toml → parse config, workspace root = parent of .dagger/
//  2. FindUp .git → workspace root = directory containing .git (empty workspace)
//  3. No .git → cwd is workspace root (empty workspace)
func Detect(
	ctx context.Context,
	statFS core.StatFS,
	readFile func(ctx context.Context, path string) ([]byte, error),
	cwd string,
) (*Workspace, error) {
	// Single-pass find-up for workspace markers
	soughtNames := map[string]struct{}{
		WorkspaceDirName:     {},
		".git":               {},
		ModuleConfigFileName: {},
	}
	found, err := core.Host{}.FindUpAll(ctx, statFS, cwd, soughtNames)
	if err != nil {
		return nil, fmt.Errorf("workspace detection: %w", err)
	}

	daggerDir, hasDaggerDir := found[WorkspaceDirName]
	gitDir, hasGit := found[".git"]
	daggerJSONDir, hasDaggerJSON := found[ModuleConfigFileName]

	// Helper: find sandbox root (git root or fallback to workspace dir).
	sandboxFor := func(workspaceDir string) string {
		if hasGit {
			return gitDir
		}
		return workspaceDir
	}
	// Helper: compute workspace path relative to sandbox root.
	relPath := func(sandboxRoot, workspaceDir string) string {
		rel, err := filepath.Rel(sandboxRoot, workspaceDir)
		if err != nil {
			return "."
		}
		return rel
	}

	// if we found a dagger.json at a deeper nesting than .dagger/,
	// pretend it's an uninitialized workspace
	if hasDaggerJSON &&
		(!hasDaggerDir || len(daggerJSONDir) > len(filepath.Dir(daggerDir))) {
		// --- Compat mode: extract toolchains/blueprints from legacy dagger.json ---
		// When no workspace config exists but a nearby dagger.json has toolchains
		// or a blueprint, extract them as workspace-level modules (loaded alongside
		// the implicit CWD module in the gathering phase below).
		sandbox := sandboxFor(daggerJSONDir)
		daggerJSONPath := filepath.Join(daggerJSONDir, ModuleConfigFileName)
		data, readErr := readFile(ctx, daggerJSONPath)
		if readErr != nil {
			// Can't read the dagger.json we just found — treat as
			// uninitialized workspace with no modules.
			return &Workspace{
				Root: sandbox,
				Path: relPath(sandbox, daggerJSONDir),
			}, nil
		}
		legacyCfg, err := parseLegacyConfig(data)
		if err != nil {
			return nil, err
		}
		config := &Config{
			Modules: map[string]ModuleEntry{},
		}
		// Sources in legacy dagger.json are relative to the dagger.json
		// directory. The session resolves config module sources relative
		// to .dagger/, so prepend ../ to escape that prefix.
		legacyLocalSource := func(src, pin string) string {
			if core.FastModuleSourceKindCheck(src, pin) == core.ModuleSourceKindLocal {
				return path.Join("..", src)
			}
			return src
		}
		for _, tc := range legacyCfg.Toolchains {
			config.Modules[tc.Name] = ModuleEntry{
				Source:            legacyLocalSource(tc.Source, tc.Pin),
				LegacyDefaultPath: true,
			}
		}
		if lb := legacyCfg.Blueprint; lb != nil {
			config.Modules[lb.Name] = ModuleEntry{
				Source:            legacyLocalSource(lb.Source, lb.Pin),
				Blueprint:         true,
				LegacyDefaultPath: true,
			}
		}
		if len(config.Modules) > 0 || path.Clean(legacyCfg.Source) != "." {
			fmt.Fprintf(telemetry.GlobalWriter(ctx, ""), "Inferring workspace configuration from legacy module config (%s). Run 'dagger migrate' soon.\n", daggerJSONPath)
		}
		// The module itself is the blueprint unless an explicit blueprint
		// was configured — in that case it takes precedence.
		// Source is ".." because the module root (dagger.json) is one
		// level above the synthetic .dagger/ directory.
		config.Modules[legacyCfg.Name] = ModuleEntry{
			Source:            "..",
			Blueprint:         legacyCfg.Blueprint == nil,
			LegacyDefaultPath: true,
		}
		return &Workspace{
			Root:        sandbox,
			Path:        relPath(sandbox, daggerJSONDir),
			Config:      config,
			Initialized: false,
		}, nil
	}

	// Step 1: .dagger/ found → look for config.toml
	if hasDaggerDir {
		configPath := filepath.Join(daggerDir, WorkspaceDirName, ConfigFileName)
		data, err := readFile(ctx, configPath)
		if err == nil {
			cfg, err := ParseConfig(data)
			if err != nil {
				return nil, fmt.Errorf("parsing %s: %w", configPath, err)
			}
			sandbox := sandboxFor(daggerDir)
			return &Workspace{
				Root:        sandbox,
				Path:        relPath(sandbox, daggerDir),
				Initialized: true,
				Config:      cfg,
			}, nil
		}
		// .dagger/ exists but no config.toml — empty workspace
		sandbox := sandboxFor(daggerDir)
		return &Workspace{
			Root: sandbox,
			Path: relPath(sandbox, daggerDir),
		}, nil
	}

	// Step 2: .git found → workspace = CWD, sandbox = git root
	if hasGit {
		return &Workspace{
			Root: gitDir,
			Path: relPath(gitDir, cwd),
		}, nil
	}

	// Step 3: nothing found → cwd is both workspace and sandbox root
	return &Workspace{
		Root: cwd,
		Path: ".",
	}, nil
}

// ErrMigrationRequired indicates a dagger.json needs migration
// to the workspace format.
type ErrMigrationRequired struct {
	ConfigPath  string // absolute path to the dagger.json
	ProjectRoot string // directory containing it
}

func (e *ErrMigrationRequired) Error() string {
	return `Migration required: run "dagger migrate" to update this project to the workspace format.`
}
