package client

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// LexicalRelativePath computes a relative path between the current working directory
// and modPath without relying on runtime.GOOS to estimate OS-specific separators. This is necessary as the code
// runs inside a Linux container, but the user might have specified a Windows-style modPath.
func LexicalRelativePath(cwdPath, modPath string) (string, error) {
	cwdPath = normalizePath(cwdPath)
	modPath = normalizePath(modPath)

	cwdDrive := getDrive(cwdPath)
	modDrive := getDrive(modPath)
	if cwdDrive != modDrive {
		return "", fmt.Errorf("cannot make paths on different drives relative: %s and %s", cwdDrive, modDrive)
	}

	// Remove drive letter for relative path calculation
	cwdPath = strings.TrimPrefix(cwdPath, cwdDrive)
	modPath = strings.TrimPrefix(modPath, modDrive)

	relPath, err := filepath.Rel(cwdPath, modPath)
	if err != nil {
		return "", fmt.Errorf("failed to make path relative: %w", err)
	}

	return relPath, nil
}

// normalizePath converts all backslashes to forward slashes and removes trailing slashes.
// We can't use filepath.ToSlash() as this code always runs inside a Linux container.
func normalizePath(path string) string {
	path = filepath.Clean(path)
	path = strings.ReplaceAll(path, "\\", "/")
	return strings.TrimSuffix(path, "/")
}

// getDrive extracts the drive letter or UNC share from a path.
func getDrive(path string) string {
	// Check for drive letter (e.g., "C:")
	if len(path) >= 2 && path[1] == ':' {
		return strings.ToUpper(path[:2])
	}

	// Check for UNC path (e.g., "//server/share")
	if strings.HasPrefix(path, "//") {
		parts := strings.SplitN(path[2:], "/", 3)
		if len(parts) >= 2 {
			return "//" + parts[0] + "/" + parts[1]
		}
	}

	return ""
}

// ExpandHomeDir expands a given path to its absolute form, handling home directory
func ExpandHomeDir(homeDir string, path string) (string, error) {
	if homeDir == "" {
		return "", fmt.Errorf("homeDir is empty")
	}

	if path == "" {
		return path, nil
	}

	if path[0] != '~' {
		return path, nil
	}
	if len(path) > 1 && path[1] != '/' && path[1] != '\\' {
		return "", errors.New("cannot expand home directory")
	}

	return strings.Replace(path, "~", homeDir, 1), nil
}
