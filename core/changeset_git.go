package core

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// fileChanges categorizes files by how they changed between two directories.
type fileChanges struct {
	Added    []string
	Modified []string
	Removed  []string
	Renamed  map[string]string // newPath → oldPath
}

type lineChanges struct {
	Added   int
	Removed int
}

// compareDirectories returns the file-level differences between two directories.
// -z uses NUL delimiters so filenames with spaces/newlines are handled correctly.
func compareDirectories(ctx context.Context, oldDir, newDir string) (fileChanges, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--no-index", "--name-status", "-z", oldDir, newDir)
	out, err := cmd.Output()
	if err != nil {
		// git diff exits 1 when differences exist, which is not an error here.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return fileChanges{}, err
		}
	}
	return parseGitOutput(out, oldDir, newDir), nil
}

// compareDirectoriesNumStat returns per-file line-change counts between two directories.
func compareDirectoriesNumStat(ctx context.Context, oldDir, newDir string) (map[string]lineChanges, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--no-index", "--numstat", "-z", oldDir, newDir)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return nil, err
		}
	}
	return parseGitNumStatOutput(out, oldDir, newDir), nil
}

// directoriesAreIdentical returns true if both directories have identical content.
func directoriesAreIdentical(ctx context.Context, dir1, dir2 string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--no-index", "--quiet", dir1, dir2)
	// Avoid inheriting a caller cwd with a broken worktree .git file.
	cmd.Dir = dir1
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
		case 'R': // rename: consume extra destination token
			if len(tokens) == 0 {
				continue
			}
			newPath := tokens[0]
			tokens = tokens[1:]
			oldRel := relativeDiffPath(path, oldDir)
			newRel := relativeDiffPath(newPath, newDir)
			if oldRel != "" && newRel != "" {
				if changes.Renamed == nil {
					changes.Renamed = make(map[string]string)
				}
				changes.Renamed[newRel] = oldRel
			}
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

func parseGitNumStatOutput(out []byte, oldDir, newDir string) map[string]lineChanges {
	stats := make(map[string]lineChanges)
	tokens := splitOnNul(out)

	for len(tokens) > 0 {
		parts := strings.SplitN(tokens[0], "\t", 3)
		if len(parts) < 2 {
			tokens = tokens[1:]
			continue
		}

		added := parseNumStatCount(parts[0])
		removed := parseNumStatCount(parts[1])

		// git numstat -z has two formats:
		//   normal:  "added\tremoved\tpath\0"
		//   rename:  "added\tremoved\t\0oldpath\0newpath\0"
		var path string
		if len(parts) == 3 && parts[2] != "" {
			// Normal: path is inline in the first token.
			path = resolveDiffPath(parts[2], oldDir, newDir)
			tokens = tokens[1:]
		} else if len(tokens) >= 3 {
			// Rename/copy: old and new paths follow as separate tokens.
			oldPath, newPath := tokens[1], tokens[2]
			if newPath != "/dev/null" {
				path = relativeDiffPath(newPath, newDir)
			} else {
				path = relativeDiffPath(oldPath, oldDir)
			}
			tokens = tokens[3:]
		} else {
			tokens = tokens[1:]
			continue
		}

		if path != "" {
			stats[path] = lineChanges{Added: added, Removed: removed}
		}
	}

	return stats
}

// resolveDiffPath returns a relative path, trying newDir first then oldDir.
func resolveDiffPath(fullPath, oldDir, newDir string) string {
	if p := relativeDiffPath(fullPath, newDir); p != "" {
		return p
	}
	return relativeDiffPath(fullPath, oldDir)
}

func parseNumStatCount(raw string) int {
	n, _ := strconv.Atoi(raw) // "-" (binary) and bad data both → 0
	return n
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
	relative := relativeDiffPath(fullPath, baseDir)
	if relative == "" {
		return paths
	}
	return append(paths, relative)
}

func relativeDiffPath(fullPath, baseDir string) string {
	relative, err := filepath.Rel(baseDir, fullPath)
	if err != nil {
		return ""
	}

	relative = filepath.Clean(relative)
	if relative == "." || relative == "" {
		return ""
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ""
	}

	// Keep stable slash-separated paths in diff output.
	return filepath.ToSlash(relative)
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
