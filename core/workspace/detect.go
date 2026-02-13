package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core"
)

const (
	// WorkspaceDirName is the name of the workspace directory.
	WorkspaceDirName = ".dagger"

	// ConfigFileName is the name of the workspace config file within .dagger/.
	ConfigFileName = "config.toml"

	// LegacyConfigFileName is the legacy module config filename.
	LegacyConfigFileName = "dagger.json"
)

// Workspace represents a detected workspace with its root directory and config.
type Workspace struct {
	Root   string  // absolute path to workspace root
	Config *Config // parsed config (nil if no config.toml)
}

// Detect finds the workspace root and config from the given working directory.
//
// Uses a 4-step fallback chain:
//  1. FindUp .dagger/ → stat .dagger/config.toml → parse config, workspace root = parent of .dagger/
//  2. No .dagger/ → FindUp dagger.json → check migration triggers → fail or ignore
//  3. FindUp .git → workspace root = directory containing .git (empty workspace)
//  4. No .git → cwd is workspace root (empty workspace)
func Detect(
	ctx context.Context,
	statFS core.StatFS,
	readFile func(ctx context.Context, path string) ([]byte, error),
	cwd string,
) (*Workspace, error) {
	// Single-pass find-up for all markers
	soughtNames := map[string]struct{}{
		WorkspaceDirName:     {},
		LegacyConfigFileName: {},
		".git":               {},
	}
	found, err := core.Host{}.FindUpAll(ctx, statFS, cwd, soughtNames)
	if err != nil {
		return nil, fmt.Errorf("workspace detection: %w", err)
	}

	// Step 1: .dagger/ found → look for config.toml
	if daggerDir, ok := found[WorkspaceDirName]; ok {
		configPath := filepath.Join(daggerDir, WorkspaceDirName, ConfigFileName)
		data, err := readFile(ctx, configPath)
		if err == nil {
			cfg, err := ParseConfig(data)
			if err != nil {
				return nil, fmt.Errorf("parsing %s: %w", configPath, err)
			}
			return &Workspace{
				Root:   daggerDir,
				Config: cfg,
			}, nil
		}
		// config.toml doesn't exist inside .dagger/ — but a legacy dagger.json
		// may still be present (e.g. .dagger/ is a module source dir, not a
		// workspace config dir). Check migration triggers before treating as
		// empty workspace.
		if legacyDir, ok := found[LegacyConfigFileName]; ok {
			legacyPath := filepath.Join(legacyDir, LegacyConfigFileName)
			data, err := readFile(ctx, legacyPath)
			if err == nil {
				if err := CheckMigrationTriggers(data, legacyPath, legacyDir); err != nil {
					return nil, err
				}
			}
		}
		return &Workspace{Root: daggerDir}, nil
	}

	// Step 2: No .dagger/ → check for legacy dagger.json
	if legacyDir, ok := found[LegacyConfigFileName]; ok {
		legacyPath := filepath.Join(legacyDir, LegacyConfigFileName)
		data, err := readFile(ctx, legacyPath)
		if err == nil {
			if err := CheckMigrationTriggers(data, legacyPath, legacyDir); err != nil {
				return nil, err
			}
		}
		// dagger.json without triggers is ignored, fall through
	}

	// Step 3: .git found → workspace root = directory containing .git
	if gitDir, ok := found[".git"]; ok {
		return &Workspace{Root: gitDir}, nil
	}

	// Step 4: nothing found → cwd is workspace root
	return &Workspace{Root: cwd}, nil
}

// ErrMigrationRequired indicates a legacy dagger.json needs migration
// to the workspace format.
type ErrMigrationRequired struct {
	LegacyConfigPath string // absolute path to the dagger.json
	ProjectRoot      string // directory containing it
}

func (e *ErrMigrationRequired) Error() string {
	return `Migration required: run "dagger migrate" to update this project to the workspace format.`
}

// CheckMigrationTriggers checks if a legacy dagger.json requires migration.
// Returns *ErrMigrationRequired if migration triggers are present.
func CheckMigrationTriggers(data []byte, legacyConfigPath, projectRoot string) error {
	var legacy struct {
		Source     string `json:"source"`
		Toolchains []any  `json:"toolchains"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}

	hasToolchains := len(legacy.Toolchains) > 0
	hasNonDotSource := legacy.Source != "" && legacy.Source != "."

	if hasToolchains || hasNonDotSource {
		return &ErrMigrationRequired{
			LegacyConfigPath: legacyConfigPath,
			ProjectRoot:      projectRoot,
		}
	}

	return nil
}
