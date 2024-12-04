package client

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// LexicalRelativePath computes a relative path between the current working directory
// and modPath without relying on runtime.GOOS to estimate OS-specific separators. This is necessary as the code
// runs inside a Linux container, but the user might have specified a Windows-style modPath.
func LexicalRelativePath(cwdPath, modPath string) (string, error) {
	cwdPath = normalizePath(cwdPath)
	modPath = normalizePath(modPath)

	cwdDrive := GetDrive(cwdPath)
	modDrive := GetDrive(modPath)
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
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		path = "/"
	}
	return path
}

// GetDrive extracts the drive letter or UNC share from a path.
func GetDrive(path string) string {
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

// Getwd returns the current working directory, but handles case-insensitive filesystems (i.e. MacOS defaults)
// and returns the path with the casing as it appears when doing list dir syscalls.
// For example, on a case-insensitive filesystem, you can do "cd /FoO/bAr" and os.Getwd will return "/FoO/bAr",
// but if you do "ls /" you may see "fOO" and if you do "ls /fOO" you may see "BAR", which creates inconsistent
// paths depending on if you are using Getwd or walking the filesystem.
func Getwd() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if runtime.GOOS != "darwin" {
		return cwd, nil
	}

	// it's possible to have case-sensitive filesystems on MacOS but not sure how to check, so
	// just assume we're on a case-insensitive filesystem
	split := strings.Split(cwd, "/")
	fixedCwd := "/"
	for _, part := range split {
		if part == "" {
			continue
		}
		dirEnts, err := os.ReadDir(fixedCwd)
		if err != nil {
			return "", err
		}
		foundMatch := false
		for _, dirEnt := range dirEnts {
			if strings.EqualFold(part, dirEnt.Name()) {
				fixedCwd = filepath.Join(fixedCwd, dirEnt.Name())
				foundMatch = true
				break
			}
		}
		if !foundMatch {
			return "", fmt.Errorf("could not find matching directory entry for %s in %s", part, fixedCwd)
		}
	}

	return filepath.Clean(fixedCwd), nil
}

// Abs returns an absolute representation of path, but handles case-insensitive filesystems as described in the comment
// on Getwd for the case where the path is relative and the cwd needs to be obtained
func Abs(path string) (string, error) {
	if runtime.GOOS != "darwin" {
		return filepath.Abs(path)
	}

	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	cwd, err := Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, path), nil
}
