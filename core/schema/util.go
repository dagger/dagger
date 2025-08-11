package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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

// Return a list of paths to include .gitignore files rebased on parentPath from hostPath
//
// Example:
//
//   - parentPath = "/foo/bar"
//   - hostPath = "/foo/bar/baz"
//   - .gitignore files will be loaded from the following paths: [.gitignore, baz/**.gitignore]
//
// ---
//   - parentPath = "/"
//   - hostPath = "/foo/bar"
//   - .gitignore files will be loaded from the following paths: [.gitignore, foo/.gitignore, foo/bar/**.gitignore]
//
// We assume the hostPath is always a child of the parentPath and always absolute.
func getGitIgnoreIncludePaths(parentPath string, hostPath string) ([]string, error) {
	hostRelPathFromParent, err := filepath.Rel(parentPath, hostPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get relative path from parent: %w", err)
	}

	if hostRelPathFromParent == "." {
		return []string{"**/.gitignore"}, nil
	}

	pathParts := strings.Split(hostRelPathFromParent, string(os.PathSeparator))

	var paths []string
	for i := range pathParts {
		relativePath := filepath.Join(pathParts[:i]...)
		paths = append(paths, filepath.Join(relativePath, ".gitignore"))
	}

	recursivePattern := filepath.Join(hostRelPathFromParent, "**", ".gitignore")
	paths = append(paths, recursivePattern)

	return paths, nil
}
