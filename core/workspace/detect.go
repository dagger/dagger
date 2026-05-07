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

// Workspace represents a detected workspace boundary and selected files within
// it. ConfigFile and LockFile are deliberately separate: config.toml may be
// absent or projected from compat dagger.json, while the lockfile is a local
// writable binding selected from the workspace tree.
type Workspace struct {
	// Root is the workspace boundary: the detected git root.
	Root string

	// Cwd is the detection start location relative to Root. "." means Root.
	Cwd string

	// ConfigFile is the selected native config.toml path relative to Root.
	// Empty means no native workspace config exists.
	ConfigFile string

	// LockFile is the selected lockfile path relative to Root. It is the nearest
	// existing .dagger/lock from Cwd up to Root, or the selected .dagger fallback
	// directory when none exists.
	LockFile string
}

// PathExistsFunc checks whether a filesystem path exists.
// Returns the canonical parent directory and whether the path exists.
type PathExistsFunc func(ctx context.Context, path string) (parentDir string, exists bool, err error)

// Detect finds the workspace boundary and selected workspace files from the
// given working directory.
//
// Workspace root detection finds up to .git. If no git root is found, there is
// no workspace; callers should treat the nil workspace as a normal no-workspace
// condition, not an error.
//
// After the boundary is known, ConfigFile is the nearest .dagger/config.toml
// walking upward from cwd, stopping at the workspace root. LockFile is the
// nearest existing .dagger/lock, or the nearest .dagger/lock write target when
// no lock exists yet.
func Detect(
	ctx context.Context,
	pathExists PathExistsFunc,
	cwd string,
) (*Workspace, error) {
	gitDir, hasGit, err := findUp(ctx, pathExists, cwd, "", ".git")
	if err != nil {
		return nil, fmt.Errorf("workspace detection: %w", err)
	}
	if !hasGit {
		return nil, nil
	}

	return DetectInRoot(ctx, pathExists, cwd, gitDir)
}

// DetectInRoot detects the workspace cwd and selected files within an already
// known workspace root. This is used for remote workspaces, where the cloned git
// tree root is already the boundary even when .git is not present in the tree.
func DetectInRoot(
	ctx context.Context,
	pathExists PathExistsFunc,
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
	configDir, foundConfigFile, err := findUp(ctx, pathExists, cwd, root, configName)
	if err != nil {
		return nil, fmt.Errorf("workspace config detection: %w", err)
	}
	configFile := ""
	if foundConfigFile {
		configDirRel, err := filepath.Rel(root, configDir)
		if err != nil {
			return nil, fmt.Errorf("workspace config directory: %w", err)
		}
		configFile = cleanRelPath(filepath.Join(configDirRel, ConfigFileName))
	}

	lockName := filepath.Join(LockDirName, LockFileName)
	lockDir, foundLockFile, err := findUp(ctx, pathExists, cwd, root, lockName)
	if err != nil {
		return nil, fmt.Errorf("workspace lock detection: %w", err)
	}
	lockFile := ""
	if foundLockFile {
		lockDirRel, err := filepath.Rel(root, lockDir)
		if err != nil {
			return nil, fmt.Errorf("workspace lock directory: %w", err)
		}
		lockFile = filepath.Join(lockDirRel, LockFileName)
	} else {
		lockDirRel, err := lockFileFallbackDir(ctx, pathExists, cwd, root)
		if err != nil {
			return nil, err
		}
		lockFile = filepath.Join(lockDirRel, LockFileName)
	}

	return &Workspace{
		Root:       root,
		Cwd:        cwdRel,
		ConfigFile: configFile,
		LockFile:   cleanRelPath(lockFile),
	}, nil
}

func lockFileFallbackDir(
	ctx context.Context,
	pathExists PathExistsFunc,
	cwd string,
	root string,
) (string, error) {
	daggerParentDir, hasDaggerDir, err := findUp(ctx, pathExists, cwd, root, LockDirName)
	if err != nil {
		return "", fmt.Errorf("workspace .dagger detection: %w", err)
	}
	if !hasDaggerDir {
		return LockDirName, nil
	}
	daggerDirRel, err := filepath.Rel(root, filepath.Join(daggerParentDir, LockDirName))
	if err != nil {
		return "", fmt.Errorf("workspace .dagger directory: %w", err)
	}
	return cleanRelPath(daggerDirRel), nil
}

func cleanRelPath(p string) string {
	if p == "" || p == "." {
		return "."
	}
	return filepath.Clean(p)
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
