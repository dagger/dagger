package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RequestFromPaths loads only the explicitly selected Go files beneath root.
//
// The filesystem is confined to the Go helper boundary. Rust receives only the
// normalized Output protocol, while source digests independently bind these bytes
// to the reviewed authority selection.
func RequestFromPaths(root string, selected []string, versionLiteral string) (Request, error) {
	canonicalRoot, err := filepath.Abs(root)
	if err != nil {
		return Request{}, fmt.Errorf("resolve root: %w", err)
	}
	canonicalRoot, err = filepath.EvalSymlinks(canonicalRoot)
	if err != nil {
		return Request{}, fmt.Errorf("resolve root symlinks: %w", err)
	}

	files := map[string]SourceFile{}
	for _, path := range selected {
		if err := validateSelectedPath(path); err != nil {
			return Request{}, err
		}
		absolute := filepath.Join(canonicalRoot, filepath.FromSlash(path))
		if err := collectGoFiles(canonicalRoot, absolute, files); err != nil {
			return Request{}, err
		}
	}
	if len(files) == 0 {
		return Request{}, fmt.Errorf("selected paths contain no Go source files")
	}

	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	request := Request{FormatVersion: formatVersion, VersionLiteralName: versionLiteral}
	for _, path := range paths {
		request.Files = append(request.Files, files[path])
	}
	return request, nil
}

func validateSelectedPath(path string) error {
	if path == "" || path == "." || filepath.IsAbs(path) || filepath.ToSlash(filepath.Clean(path)) != path {
		return fmt.Errorf("non-canonical selected path %q", path)
	}
	for _, component := range strings.Split(path, "/") {
		if component == ".." {
			return fmt.Errorf("selected path escapes root: %q", path)
		}
	}
	return nil
}

func collectGoFiles(root, selected string, files map[string]SourceFile) error {
	return filepath.WalkDir(selected, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk selected path: %w", walkErr)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("selected source traverses symlink %q", path)
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("make source path relative: %w", err)
		}
		logical := filepath.ToSlash(relative)
		if strings.HasPrefix(logical, "../") || logical == ".." {
			return fmt.Errorf("selected source escapes root: %q", logical)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", logical, err)
		}
		files[logical] = SourceFile{Path: logical, Content: string(content)}
		return nil
	})
}
