package workspace

import (
	"context"
	"fmt"
	"path/filepath"
)

const (
	// WorkspaceDirName is the name of the workspace directory.
	WorkspaceDirName = ".dagger"

	// ConfigFileName is the name of the workspace config file within .dagger/.
	ConfigFileName = "config.toml"

	// LockFileName is the name of the workspace lock file within .dagger/.
	LockFileName = "lock"

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

// PathExistsFunc checks whether a filesystem path exists.
// Returns the canonical parent directory and whether the path exists.
type PathExistsFunc func(ctx context.Context, path string) (parentDir string, exists bool, err error)

// Detect finds the workspace root and config from the given working directory.
//
// Uses a 3-step fallback chain:
//  1. FindUp .dagger/ → stat .dagger/config.toml → parse config, workspace root = parent of .dagger/
//  2. FindUp .git → workspace root = directory containing .git (empty workspace)
//  3. No .git → cwd is workspace root (empty workspace)
func Detect(
	ctx context.Context,
	pathExists PathExistsFunc,
	readFile func(ctx context.Context, path string) ([]byte, error),
	cwd string,
) (*Workspace, error) {
	// Single-pass find-up for workspace markers
	found, err := findUpAll(ctx, pathExists, cwd, WorkspaceDirName, ".git")
	if err != nil {
		return nil, fmt.Errorf("workspace detection: %w", err)
	}

	daggerDir, hasDaggerDir := found[WorkspaceDirName]
	gitDir, hasGit := found[".git"]

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

	// Step 1: .dagger/ found → look for config.toml
	if hasDaggerDir {
		configPath := filepath.Join(daggerDir, WorkspaceDirName, ConfigFileName)
		_, configExists, err := pathExists(ctx, configPath)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", configPath, err)
		}
		if configExists {
			data, err := readFile(ctx, configPath)
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", configPath, err)
			}

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
