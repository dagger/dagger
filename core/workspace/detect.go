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

// Workspace represents a detected workspace boundary.
type Workspace struct {
	// Root is the outer filesystem boundary (git root, or cwd if no git).
	Root string

	// Path is the workspace location relative to Root (e.g., "apps/frontend" or ".").
	Path string

	// Cwd is the workspace detection start location relative to Path. Empty means
	// detection started at the workspace path.
	Cwd string

	// Initialized is true if .dagger/config.toml was found.
	Initialized bool
}

// PathExistsFunc checks whether a filesystem path exists.
// Returns the canonical parent directory and whether the path exists.
type PathExistsFunc func(ctx context.Context, path string) (parentDir string, exists bool, err error)

// Detect finds the workspace boundary from the given working directory.
//
// Uses a 3-step fallback chain:
//  1. FindUp .dagger/config.toml → workspace root = parent of .dagger/
//  2. FindUp .git → workspace root = directory containing .git
//  3. No .git → cwd is workspace root
func Detect(
	ctx context.Context,
	pathExists PathExistsFunc,
	_ func(ctx context.Context, path string) ([]byte, error),
	cwd string,
) (*Workspace, error) {
	configPath := filepath.Join(LockDirName, ConfigFileName)
	found, err := findUpAll(ctx, pathExists, cwd, configPath, ".git")
	if err != nil {
		return nil, fmt.Errorf("workspace detection: %w", err)
	}

	configDir, hasConfig := found[configPath]
	gitDir, hasGit := found[".git"]

	sandboxFor := func(workspaceDir string) string {
		if hasGit {
			return gitDir
		}
		return workspaceDir
	}

	relPath := func(sandboxRoot, workspaceDir string) string {
		rel, err := filepath.Rel(sandboxRoot, workspaceDir)
		if err != nil {
			return "."
		}
		return rel
	}
	relCwd := func(workspaceDir string) string {
		rel, err := filepath.Rel(workspaceDir, cwd)
		if err != nil || rel == "." {
			return ""
		}
		return rel
	}

	// Step 1: config.toml found → workspace = parent of .dagger, sandbox = git root if present
	if hasConfig {
		workspaceDir := filepath.Dir(configDir)
		sandbox := sandboxFor(workspaceDir)
		return &Workspace{
			Root:        sandbox,
			Path:        relPath(sandbox, workspaceDir),
			Cwd:         relCwd(workspaceDir),
			Initialized: true,
		}, nil
	}

	// Step 2: .git found → workspace = git root, cwd = detection start
	if hasGit {
		return &Workspace{
			Root: gitDir,
			Path: ".",
			Cwd:  relCwd(gitDir),
		}, nil
	}

	// Step 3: nothing found → cwd is both workspace and sandbox root
	return &Workspace{
		Root: cwd,
		Path: ".",
		Cwd:  "",
	}, nil
}

// findUpAll walks from curDirPath toward the filesystem root, checking for each
// sought name. Returns a map of name → parent directory for each name found.
func findUpAll(
	ctx context.Context,
	pathExists PathExistsFunc,
	curDirPath string,
	names ...string,
) (map[string]string, error) {
	sought := make(map[string]struct{}, len(names))
	for _, n := range names {
		sought[n] = struct{}{}
	}

	found := make(map[string]string, len(sought))
	for {
		for name := range sought {
			parentDir, exists, err := pathExists(ctx, filepath.Join(curDirPath, name))
			if err != nil {
				return nil, fmt.Errorf("failed to stat %s: %w", name, err)
			}
			if exists {
				delete(sought, name)
				found[name] = parentDir
			}
		}

		if len(sought) == 0 {
			break
		}

		nextDirPath := filepath.Dir(curDirPath)
		if curDirPath == nextDirPath {
			break
		}
		curDirPath = nextDirPath
	}

	return found, nil
}
