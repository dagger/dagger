package workspace

import (
	"context"
	"fmt"
	"path/filepath"
)

const (
	// ModuleConfigFileName is the module config filename.
	ModuleConfigFileName = "dagger.json"
)

// Workspace represents a detected workspace boundary.
type Workspace struct {
	// Root is the outer filesystem boundary (git root, or cwd if no git).
	Root string

	// Path is the workspace location relative to Root (e.g., "apps/frontend" or ".").
	Path string
}

// PathExistsFunc checks whether a filesystem path exists.
// Returns the canonical parent directory and whether the path exists.
type PathExistsFunc func(ctx context.Context, path string) (parentDir string, exists bool, err error)

// Detect finds the workspace boundary from the given working directory.
//
// Uses a 2-step fallback chain:
//  1. FindUp .git → workspace root = directory containing .git
//  2. No .git → cwd is workspace root
func Detect(
	ctx context.Context,
	pathExists PathExistsFunc,
	_ func(ctx context.Context, path string) ([]byte, error),
	cwd string,
) (*Workspace, error) {
	found, err := findUpAll(ctx, pathExists, cwd, ".git")
	if err != nil {
		return nil, fmt.Errorf("workspace detection: %w", err)
	}

	gitDir, hasGit := found[".git"]

	relPath := func(sandboxRoot, workspaceDir string) string {
		rel, err := filepath.Rel(sandboxRoot, workspaceDir)
		if err != nil {
			return "."
		}
		return rel
	}

	// Step 1: .git found → workspace = CWD, sandbox = git root
	if hasGit {
		return &Workspace{
			Root: gitDir,
			Path: relPath(gitDir, cwd),
		}, nil
	}

	// Step 2: nothing found → cwd is both workspace and sandbox root
	return &Workspace{
		Root: cwd,
		Path: ".",
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
