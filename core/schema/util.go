package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/introspection"
)

type SchemaResolvers interface {
	Install(*dagql.Server)
}

func Syncer[T core.Evaluatable]() dagql.Field[T] {
	return dagql.NodeFunc("sync", func(ctx context.Context, self dagql.ObjectResult[T], _ struct{}) (res dagql.Result[dagql.ID[T]], _ error) {
		_, err := self.Self().Evaluate(ctx)
		if err != nil {
			return res, err
		}
		id := dagql.NewID[T](self.ID())
		return dagql.NewResultForCurrentID(ctx, id)
	})
}

func collectInputsSlice[T dagql.Type](inputs []dagql.InputObject[T]) []T {
	ts := make([]T, len(inputs))
	for i, input := range inputs {
		ts[i] = input.Value
	}
	return ts
}

func collectIDObjectResults[T dagql.Typed](ctx context.Context, srv *dagql.Server, ids []dagql.ID[T]) ([]dagql.ObjectResult[T], error) {
	ts := make([]dagql.ObjectResult[T], len(ids))
	for i, id := range ids {
		inst, err := id.Load(ctx, srv)
		if err != nil {
			return nil, err
		}
		ts[i] = inst
	}
	return ts, nil
}

func asArrayInput[T any, I dagql.Input](ts []T, conv func(T) I) dagql.ArrayInput[I] {
	ins := make(dagql.ArrayInput[I], len(ts))
	for i, v := range ts {
		ins[i] = conv(v)
	}
	return ins
}

func SchemaIntrospectionJSON(ctx context.Context, dag *dagql.Server) (json.RawMessage, error) {
	data, err := dag.Query(ctx, introspection.Query, nil)
	if err != nil {
		return nil, fmt.Errorf("introspection query failed: %w", err)
	}
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal introspection result: %w", err)
	}
	return json.RawMessage(jsonBytes), nil
}

func ptr[T any](v T) *T {
	return &v
}

var AllVersion = core.AllVersion

type BeforeVersion = core.BeforeVersion
type AfterVersion = core.AfterVersion

// Extract the gitignore patterns from its file content and return them as relative paths
// based on the given parent directory.
// The parent directory is required to match the gitignore patterns relative to
// the context directory since the given content may be a .gitignore file from a subdirectory
// of that context.
//
// Example:
//   - contextDir = "/my-project/foo"
//   - .gitignoreDir = "/my-project/foo/bar/.gitignore"
//   - parentDir = "/my-project/foo/bar" -> so **/node_modules becomes my-project/foo/bar/**/node_modules
//
// Pattern additional formatting is based on https://git-scm.com/docs/gitignore so it can
// be correctly applied with further dagger include/exclude filter patterns.
//
// Here are the rules of that filter/formatting:
//
//   - We ignore empty lines and comments (starting with `#`) (`\#` isn't ignored).
//
//   - If there is a separator at the beginning or middle (or both) of the pattern, then the pattern is relative.
//     Otherwise, the pattern is recursive.
//     If pattern is already starting with **, not change needed.
//     If the pattern starts with *, it's not recursive but only matches the directory itself.
//
//   - If a pattern is negative exclusion (starts with `!`) or targets directory only
//     (ends with `/`), we treat is as a regular path then readd the exclusion to make
//     sure the recusive pattern is applied if needed.
func extractGitIgnorePatterns(gitIgnoreContent string, parentDir string) []string {
	ignorePatterns := []string{}

	// Split gitignore files by line
	ignorePatternsLines := strings.Split(string(gitIgnoreContent), "\n")

	for _, linePattern := range ignorePatternsLines {
		// ignore comments, negatives and empty lines
		if strings.HasPrefix(linePattern, "#") || linePattern == "" {
			continue
		}

		// Save if the pattern is a directory only or negate so we can work with the path
		// only and reconstruct it later
		isDirOnly := strings.HasSuffix(linePattern, "/")
		isNegate := strings.HasPrefix(linePattern, "!")
		pattern := strings.TrimPrefix(strings.TrimSuffix(linePattern, "/"), "!")

		// Based on https://git-scm.com/docs/gitignore
		// If there is a separator at the beginning or middle (or both) of the pattern, then the pattern is relative.
		// Otherwise, the pattern is recursive.
		// If pattern is already starting with **, not change needed
		// If the pattern starts with *, it's not recursive but only matches the directory itself.
		if !strings.Contains(pattern, "/") &&
			!strings.HasPrefix(pattern, "**") &&
			!strings.HasPrefix(pattern, "*") {
			pattern = "**/" + pattern
		}

		// Rebase the pattern based on the relative path from the context.
		relativePattern := filepath.Join(parentDir, pattern)

		// Reconstruct the pattern with negative or directory only pattern
		if isNegate {
			relativePattern = "!" + relativePattern
		}
		if isDirOnly {
			relativePattern = relativePattern + "/"
		}

		ignorePatterns = append(ignorePatterns, relativePattern)
	}

	return ignorePatterns
}

// Load git ignore patterns in the current directory and all its children
// It assumes that the given `dir` only contains `.gitignore` files and directories that may
// contain `.gitignore` files.
func loadGitIgnoreInDirectory(ctx context.Context, dir *core.Directory, parentDir string) ([]string, error) {
	result := []string{}
	name := dir.Dir

	entries, err := dir.Entries(ctx, ".")
	if err != nil {
		return nil, fmt.Errorf("failed to list files in directory %s: %w", dir.Dir, err)
	}

	for _, entry := range entries {
		if entry == ".gitignore" {
			file, err := dir.File(ctx, entry)
			if err != nil {
				return nil, fmt.Errorf("failed to get file %s: %w", entry, err)
			}

			content, err := file.Contents(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed read file git ignore %s: %w", entry, err)
			}

			parentPath := filepath.Join(parentDir, name)

			result = append(result, extractGitIgnorePatterns(string(content), parentPath)...)
			continue
		}

		subDir, err := dir.Directory(ctx, entry)
		if err != nil {
			return nil, fmt.Errorf("failed to get directory %s: %w", entry, err)
		}

		subDirResult, err := loadGitIgnoreInDirectory(ctx, subDir, filepath.Join(parentDir, name))
		if err != nil {
			return nil, fmt.Errorf("failed to get directory git ignore %s: %w", entry, err)
		}

		result = append(result, subDirResult...)
	}

	return result, nil
}
