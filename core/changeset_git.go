package core

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// fileChanges categorizes files by how they changed between two directories.
type fileChanges struct {
	Added    []string
	Modified []string
	Removed  []string
}

// compareDirectories returns the file-level differences between two directories.
func compareDirectories(ctx context.Context, oldDir, newDir string) (fileChanges, error) {
	out, err := runGitDiff(ctx, oldDir, newDir)
	if err != nil {
		return fileChanges{}, err
	}
	return parseGitOutput(out, oldDir, newDir), nil
}

// directoriesAreIdentical returns true if both directories have identical content.
func directoriesAreIdentical(ctx context.Context, dir1, dir2 string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--no-index", "--quiet", dir1, dir2)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func runGitDiff(ctx context.Context, oldDir, newDir string) ([]byte, error) {
	// -z uses NUL delimiters, safe for filenames with spaces/newlines
	cmd := exec.CommandContext(ctx, "git", "diff", "--no-index", "--name-status", "-z", oldDir, newDir)
	out, err := cmd.Output()
	if err == nil {
		return out, nil
	}
	// git diff exits 1 when differences exist - that's not an error for us
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return out, nil
	}
	return nil, err
}

func parseGitOutput(out []byte, oldDir, newDir string) fileChanges {
	var changes fileChanges
	tokens := splitOnNul(out)

	for len(tokens) >= 2 {
		status := tokens[0][0]
		path := tokens[1]
		tokens = tokens[2:]

		switch status {
		case 'A':
			changes.Added = appendRelativePath(changes.Added, path, newDir)
		case 'D':
			changes.Removed = appendRelativePath(changes.Removed, path, oldDir)
		case 'M', 'T':
			changes.Modified = appendRelativePath(changes.Modified, path, oldDir)
		case 'R': // rename: old path removed, new path added
			if len(tokens) == 0 {
				continue
			}
			newPath := tokens[0]
			tokens = tokens[1:]
			changes.Removed = appendRelativePath(changes.Removed, path, oldDir)
			changes.Added = appendRelativePath(changes.Added, newPath, newDir)
		case 'C': // copy: only new path added (old still exists)
			if len(tokens) == 0 {
				continue
			}
			newPath := tokens[0]
			tokens = tokens[1:]
			changes.Added = appendRelativePath(changes.Added, newPath, newDir)
		}
	}
	return changes
}

func splitOnNul(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	parts := bytes.Split(data, []byte{0})
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) > 0 {
			result = append(result, string(p))
		}
	}
	return result
}

func appendRelativePath(paths []string, fullPath, baseDir string) []string {
	relative, found := strings.CutPrefix(fullPath, baseDir)
	if !found {
		return paths
	}
	relative = strings.TrimPrefix(relative, "/")
	if relative == "" {
		return paths
	}
	return append(paths, relative)
}

// listSubdirectories returns all subdirectory paths relative to root.
// Each path ends with "/" to distinguish from files.
func listSubdirectories(root string) ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() || path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		dirs = append(dirs, filepath.ToSlash(relative)+"/")
		return nil
	})
	return dirs, err
}

// diffStringSlices returns elements added to and removed from a slice.
func diffStringSlices(old, new []string) (added, removed []string) {
	oldSet := make(map[string]struct{}, len(old))
	for _, s := range old {
		oldSet[s] = struct{}{}
	}

	for _, s := range new {
		if _, exists := oldSet[s]; exists {
			delete(oldSet, s)
		} else {
			added = append(added, s)
		}
	}

	for s := range oldSet {
		removed = append(removed, s)
	}
	slices.Sort(removed)
	return
}

// collapseChildPaths removes paths that are children of directory paths.
// For example, if "foo/" is in the list, "foo/bar.txt" is removed.
func collapseChildPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}

	sorted := slices.Clone(paths)
	slices.Sort(sorted)

	var result []string
	var currentParent string

	for _, path := range sorted {
		if currentParent != "" && strings.HasPrefix(path, currentParent) {
			continue
		}
		result = append(result, path)
		if strings.HasSuffix(path, "/") {
			currentParent = path
		} else {
			currentParent = ""
		}
	}
	return result
}
