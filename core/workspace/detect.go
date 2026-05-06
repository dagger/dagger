package workspace

import (
	"context"
	"fmt"
	"path/filepath"
)

const (
	// ConfigFileName is the workspace config filename inside .dagger/.
	ConfigFileName = "config.toml"

	// ModuleConfigFileName is the module config filename.
	ModuleConfigFileName = "dagger.json"
)

// Workspace represents a detected workspace boundary and selected config.
type Workspace struct {
	// Root is the workspace boundary (git root, or cwd if no git).
	Root string

	// Cwd is the detection start location relative to Root. "." means Root.
	Cwd string

	// ConfigDirectory is the selected .dagger directory relative to Root.
	ConfigDirectory string

	// ConfigFile is the selected config.toml path relative to Root.
	ConfigFile string

	// HasConfig is true if a workspace config was found.
	HasConfig bool
}

// PathExistsFunc checks whether a filesystem path exists.
// Returns the canonical parent directory and whether the path exists.
type PathExistsFunc func(ctx context.Context, path string) (parentDir string, exists bool, err error)

// Detect finds the workspace boundary and selected config from the given working
// directory.
//
// Uses a 2-step boundary fallback:
//  1. FindUp .git -> workspace root = directory containing .git
//  2. No .git -> cwd is workspace root
//
// After the boundary is known, the selected config is the nearest
// .dagger/config.toml walking upward from cwd, stopping at the workspace root.
func Detect(
	ctx context.Context,
	pathExists PathExistsFunc,
	_ func(ctx context.Context, path string) ([]byte, error),
	cwd string,
) (*Workspace, error) {
	gitDir, hasGit, err := findUp(ctx, pathExists, cwd, "", ".git")
	if err != nil {
		return nil, fmt.Errorf("workspace detection: %w", err)
	}

	root := cwd
	if hasGit {
		root = gitDir
	}

	return DetectInRoot(ctx, pathExists, nil, cwd, root)
}

// DetectInRoot detects the workspace cwd and selected config within an already
// known workspace root. This is used for remote workspaces, where the cloned git
// tree root is already the boundary even when .git is not present in the tree.
func DetectInRoot(
	ctx context.Context,
	pathExists PathExistsFunc,
	_ func(ctx context.Context, path string) ([]byte, error),
	cwd string,
	root string,
) (*Workspace, error) {
	root = filepath.Clean(root)
	cwd = filepath.Clean(cwd)

	cwdRel, err := filepath.Rel(root, cwd)
	if err != nil {
		return nil, fmt.Errorf("workspace cwd: %w", err)
	}
	if cwdRel == "" {
		cwdRel = "."
	}
	if cwdRel != "." && (cwdRel == ".." || filepath.IsAbs(cwdRel) || len(cwdRel) > 3 && cwdRel[:3] == ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("workspace cwd %q is outside workspace root %q", cwd, root)
	}

	configName := filepath.Join(LockDirName, ConfigFileName)
	configDir, hasConfig, err := findUp(ctx, pathExists, cwd, root, configName)
	if err != nil {
		return nil, fmt.Errorf("workspace config detection: %w", err)
	}

	ws := &Workspace{
		Root: root,
		Cwd:  cwdRel,
	}
	if hasConfig {
		configDirRel, err := filepath.Rel(root, configDir)
		if err != nil {
			return nil, fmt.Errorf("workspace config directory: %w", err)
		}
		ws.ConfigDirectory = configDirRel
		ws.ConfigFile = filepath.Join(configDirRel, ConfigFileName)
		ws.HasConfig = true
	}
	return ws, nil
}

// findUp walks from curDirPath toward the filesystem root, checking for name.
// If stopDir is non-empty, the walk stops after checking stopDir.
func findUp(
	ctx context.Context,
	pathExists PathExistsFunc,
	curDirPath string,
	stopDir string,
	name string,
) (string, bool, error) {
	if stopDir != "" {
		stopDir = filepath.Clean(stopDir)
	}
	for {
		parentDir, exists, err := pathExists(ctx, filepath.Join(curDirPath, name))
		if err != nil {
			return "", false, fmt.Errorf("failed to stat %s: %w", name, err)
		}
		if exists {
			return parentDir, true, nil
		}

		if stopDir != "" && filepath.Clean(curDirPath) == stopDir {
			break
		}
		nextDirPath := filepath.Dir(curDirPath)
		if curDirPath == nextDirPath {
			break
		}
		curDirPath = nextDirPath
	}

	return "", false, nil
}
