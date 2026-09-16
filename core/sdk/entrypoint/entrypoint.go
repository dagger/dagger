// Package entrypoint holds the parts of module entrypoint loading that every
// entrypoint driver shares: resolving entrypoint.source to a module source,
// encoding the arguments of the ModuleEntrypoint interface, and turning the
// type definitions an entrypoint returns into a module.
//
// The drivers themselves live next to it: dang evaluates the entrypoint source
// as a Dang program, module loads it as a manifest version 2 module.
package entrypoint

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

// TypeName is the interface an entrypoint must implement.
const TypeName = "ModuleEntrypoint"

// ResolveSource resolves entrypoint.source to a module source for the
// entrypoint itself, and returns the workspace to pass to the entrypoint.
func ResolveSource(
	ctx context.Context,
	dag *dagql.Server,
	src dagql.ObjectResult[*core.ModuleSource],
) (dagql.ObjectResult[*core.ModuleSource], dagql.ObjectResult[*core.Workspace], error) {
	if src.Self().Entrypoint == nil {
		return dagql.ObjectResult[*core.ModuleSource]{}, dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module entrypoint is not configured")
	}

	var workspace dagql.ObjectResult[*core.Workspace]
	if err := dag.Select(ctx, dag.Root(), &workspace, dagql.Selector{Field: "currentWorkspace"}); err != nil {
		return dagql.ObjectResult[*core.ModuleSource]{}, dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("get module workspace: %w", err)
	}

	address := src.Self().Entrypoint.Source
	directory, err := resolveSourceDirectory(ctx, dag, src, address)
	if err != nil {
		return dagql.ObjectResult[*core.ModuleSource]{}, dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("resolve module entrypoint source %q: %w", address, err)
	}

	entrySrc := src.Self().Clone()
	entrySrc.ContextDirectory = directory
	entrySrc.SourceRootSubpath = "."
	entrySrc.SourceSubpath = "."
	entrySrc.IncludePaths = nil
	entrySrc.RebasedIncludePaths = nil
	entrySrc.ConfigDependencies = nil
	entrySrc.Dependencies = nil
	entrySrc.ConfigBlueprint = nil
	entrySrc.Blueprint = dagql.ObjectResult[*core.ModuleSource]{}
	entrySrc.ConfigToolchains = nil
	entrySrc.Toolchains = nil
	entrySrc.Local = nil
	entrySrc.Git = nil
	entrySrc.DirSrc = &core.DirModuleSource{OriginalContextDir: directory}
	entrySrc.Kind = core.ModuleSourceKindDir

	entrySrcResult, err := dagql.NewObjectResultForCurrentCall(ctx, dag, entrySrc)
	if err != nil {
		return dagql.ObjectResult[*core.ModuleSource]{}, dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("attach module entrypoint source: %w", err)
	}
	return entrySrcResult, workspace, nil
}

// resolveSourceDirectory loads the directory named by entrypoint.source. A
// local path is relative to the directory that contains dagger-module.toml and
// is read from the module's own context directory, so it resolves the same way
// for local, git and directory module sources. Any other value is an address
// that resolves to a Directory.
func resolveSourceDirectory(
	ctx context.Context,
	dag *dagql.Server,
	src dagql.ObjectResult[*core.ModuleSource],
	address string,
) (dagql.ObjectResult[*core.Directory], error) {
	var directory dagql.ObjectResult[*core.Directory]

	local, err := isLocalSource(ctx, src, address)
	if err != nil {
		return directory, err
	}
	if !local {
		err := dag.Select(ctx, dag.Root(), &directory,
			dagql.Selector{Field: "address", Args: []dagql.NamedInput{{Name: "value", Value: dagql.String(address)}}},
			dagql.Selector{Field: "directory"},
		)
		return directory, err
	}

	subpath, err := sourceSubpath(src.Self(), address)
	if err != nil {
		return directory, err
	}
	contextDir := src.Self().ContextDirectory
	if contextDir.Self() == nil {
		return directory, fmt.Errorf("module source has no context directory")
	}
	err = dag.Select(ctx, contextDir, &directory, dagql.Selector{
		Field: "directory",
		Args:  []dagql.NamedInput{{Name: "path", Value: dagql.String(subpath)}},
	})
	return directory, err
}

// isLocalSource reports whether source is a path relative to the module
// directory, as opposed to an address. It follows the same heuristic as module
// refs: an explicit path prefix or a dot-free value is local, a value with a
// ":" (a URL or a module:function address) is remote, and an ambiguous value
// such as "example.com/repo" is local only when the path exists under the
// module directory.
func isLocalSource(
	ctx context.Context,
	src dagql.ObjectResult[*core.ModuleSource],
	source string,
) (bool, error) {
	if strings.HasPrefix(source, ".") || strings.HasPrefix(source, "/") {
		return true, nil
	}
	if strings.Contains(source, ":") {
		// URL, scp-like git URL, or a module:function address
		return false, nil
	}
	switch core.FastModuleSourceKindCheck(source, "") {
	case core.ModuleSourceKindLocal:
		return true, nil
	case core.ModuleSourceKindGit:
		return false, nil
	}
	if src.Self() == nil || src.Self().ContextDirectory.Self() == nil {
		return false, nil
	}
	subpath, err := sourceSubpath(src.Self(), source)
	if err != nil {
		// not a usable local path, so treat it as an address
		return false, nil //nolint:nilerr
	}
	contextDir := src.Self().ContextDirectory
	_, exists, err := core.StatFSExists(ctx, &core.DirectoryStatFS{Dir: contextDir}, subpath)
	if err != nil {
		return false, fmt.Errorf("stat %q in module directory: %w", source, err)
	}
	return exists, nil
}

// sourceSubpath converts a module-relative entrypoint path into a path
// relative to the module source context directory.
func sourceSubpath(src *core.ModuleSource, source string) (string, error) {
	if filepath.IsAbs(source) {
		return "", fmt.Errorf("entrypoint source path %q must be relative to the module directory", source)
	}
	cleaned := filepath.Clean(source)
	if !filepath.IsLocal(cleaned) {
		return "", fmt.Errorf("entrypoint source path %q escapes the module directory", source)
	}
	rootSubpath := src.SourceRootSubpath
	if rootSubpath == "" {
		rootSubpath = "."
	}
	return filepath.Join(rootSubpath, cleaned), nil
}

// WorkspaceCallArg encodes the workspace argument shared by types and call.
func WorkspaceCallArg(workspace dagql.ObjectResult[*core.Workspace]) (*core.FunctionCallArgValue, error) {
	id, err := workspace.ID()
	if err != nil {
		return nil, fmt.Errorf("get module workspace ID: %w", err)
	}
	encoded, err := id.Encode()
	if err != nil {
		return nil, fmt.Errorf("encode module workspace ID: %w", err)
	}
	return StringCallArg("workspace", encoded), nil
}

// JSONCallArg builds an argument from an already encoded JSON value.
func JSONCallArg(name string, value core.JSON) *core.FunctionCallArgValue {
	return &core.FunctionCallArgValue{Name: name, Value: value}
}

// StringCallArg builds a JSON-encoded string argument.
func StringCallArg(name, value string) *core.FunctionCallArgValue {
	data, _ := json.Marshal(value)
	return JSONCallArg(name, core.JSON(data))
}

// JSONScalarCallArg builds a JSON scalar argument, which the call codec passes
// as a JSON-encoded string.
func JSONScalarCallArg(name string, value core.JSON) *core.FunctionCallArgValue {
	return StringCallArg(name, string(value))
}

// FunctionArgsJSON encodes call arguments as one JSON object, keyed by the
// original argument names.
func FunctionArgsJSON(args []*core.FunctionCallArgValue) (core.JSON, error) {
	values := make(map[string]json.RawMessage, len(args))
	for _, arg := range args {
		if arg == nil {
			continue
		}
		if !json.Valid(arg.Value) {
			return nil, fmt.Errorf("function argument %q is not valid JSON", arg.Name)
		}
		values[arg.Name] = json.RawMessage(arg.Value)
	}
	data, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("encode function arguments: %w", err)
	}
	return core.JSON(data), nil
}

// ValidateConstructors applies the engine's current limit of at most one
// object constructor per module.
func ValidateConstructors(typeDefs dagql.ObjectResultArray[*core.TypeDef]) error {
	var constructors []string
	for _, typeDef := range typeDefs {
		if typeDef.Self().Kind != core.TypeDefKindObject || !typeDef.Self().AsObject.Value.Self().Constructor.Valid {
			continue
		}
		constructors = append(constructors, typeDef.Self().AsObject.Value.Self().OriginalName)
	}
	if len(constructors) > 1 {
		return fmt.Errorf("multiple object constructors are not supported: %s", strings.Join(constructors, ", "))
	}
	return nil
}

// ModuleFromTypeDefs builds a module from the types an entrypoint returned.
func ModuleFromTypeDefs(
	ctx context.Context,
	dag *dagql.Server,
	typeDefs dagql.ObjectResultArray[*core.TypeDef],
) (dagql.ObjectResult[*core.Module], error) {
	selectors := []dagql.Selector{{Field: "module"}}
	for i, typeDef := range typeDefs {
		id, err := typeDef.ID()
		if err != nil {
			return dagql.ObjectResult[*core.Module]{}, fmt.Errorf("get module entrypoint type %d ID: %w", i, err)
		}
		typeDefID := dagql.NewID[*core.TypeDef](id)
		switch typeDef.Self().Kind {
		case core.TypeDefKindObject:
			selectors = append(selectors, dagql.Selector{Field: "withObject", Args: []dagql.NamedInput{{Name: "object", Value: typeDefID}}})
		case core.TypeDefKindInterface:
			selectors = append(selectors, dagql.Selector{Field: "withInterface", Args: []dagql.NamedInput{{Name: "iface", Value: typeDefID}}})
		case core.TypeDefKindEnum:
			selectors = append(selectors, dagql.Selector{Field: "withEnum", Args: []dagql.NamedInput{{Name: "enum", Value: typeDefID}}})
		default:
			return dagql.ObjectResult[*core.Module]{}, fmt.Errorf("module entrypoint type %d has unsupported kind %q", i, typeDef.Self().Kind)
		}
	}

	var module dagql.ObjectResult[*core.Module]
	if err := dag.Select(ctx, dag.Root(), &module, selectors...); err != nil {
		return module, fmt.Errorf("create module from entrypoint types: %w", err)
	}
	return module, nil
}
