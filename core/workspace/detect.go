package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
				if err := checkMigrationTriggers(data); err != nil {
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
			if err := checkMigrationTriggers(data); err != nil {
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

// DetectLocal finds the workspace root from the local filesystem.
// This is the CLI-side counterpart to Detect(), which requires a buildkit session.
// It walks up from cwd looking for .dagger/, then .git, then falls back to cwd.
// It does not check migration triggers (that's the engine's responsibility).
func DetectLocal(cwd string) (*Workspace, error) {
	// Walk up looking for .dagger/
	dir := cwd
	for {
		info, err := os.Stat(filepath.Join(dir, WorkspaceDirName))
		if err == nil && info.IsDir() {
			configPath := filepath.Join(dir, WorkspaceDirName, ConfigFileName)
			data, err := os.ReadFile(configPath)
			if err == nil {
				cfg, err := ParseConfig(data)
				if err != nil {
					return nil, fmt.Errorf("parsing %s: %w", configPath, err)
				}
				return &Workspace{Root: dir, Config: cfg}, nil
			}
			// .dagger/ exists but no config.toml — valid empty workspace
			return &Workspace{Root: dir}, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	// Walk up looking for .git
	dir = cwd
	for {
		_, err := os.Stat(filepath.Join(dir, ".git"))
		if err == nil {
			return &Workspace{Root: dir}, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	// Nothing found — cwd is workspace root
	return &Workspace{Root: cwd}, nil
}

// checkMigrationTriggers checks if a legacy dagger.json requires migration.
// Returns an error if migration triggers are present.
func checkMigrationTriggers(data []byte) error {
	var legacy struct {
		Source     string `json:"source"`
		Toolchains []any  `json:"toolchains"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		// Can't parse it — ignore it
		return nil
	}

	hasToolchains := len(legacy.Toolchains) > 0
	hasNonDotSource := legacy.Source != "" && legacy.Source != "."

	if hasToolchains || hasNonDotSource {
		return fmt.Errorf(
			"this project uses a legacy dagger.json that needs migration to the workspace format (.dagger/config.toml); see https://github.com/dagger/dagger for migration instructions",
		)
	}

	return nil
}
