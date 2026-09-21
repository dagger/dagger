// Package entrypoint holds the parts of module entrypoint loading that every
// entrypoint driver shares: resolving entrypoint.source to a module source,
// encoding the arguments of the ModuleEntrypoint interface, and turning the
// type definitions an entrypoint returns into a module.
//
// The one driver lives next to it: dang evaluates the entrypoint source as a
// Dang program.
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

	workspace, err := Workspace(ctx, dag, src)
	if err != nil {
		return dagql.ObjectResult[*core.ModuleSource]{}, dagql.ObjectResult[*core.Workspace]{}, err
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

// Workspace returns the workspace to pass to a module entrypoint: a synthetic
// workspace built from the module's own loaded context, with its working
// directory at the module's directory.
//
// The engine already treats the workspace cwd as the module's scope when it
// hands a workspace to an SDK, so an entrypoint can find the module it serves
// through relative workspace paths, without a path generated into it. It can
// read above the module with an absolute workspace path, but only within the
// module's own context.
//
// The workspace is the module's, not the caller's, and it is built the same way
// whichever source the module was loaded from. A module that wants the caller's
// workspace declares a Workspace argument on a function, which the caller
// supplies.
//
// The root is the module's context. A git source gets its repository at the
// pinned commit and a directory source the directory it was created from. A
// local source, or any source loaded from a workspace, gets the context the
// engine loaded for it: the manifest, the module's own files, and the paths its
// includes name. Its host tree is the tree the caller runs in, which is not the
// module's context, so the loaded files are what it gets.
func Workspace(
	ctx context.Context,
	dag *dagql.Server,
	src dagql.ObjectResult[*core.ModuleSource],
) (dagql.ObjectResult[*core.Workspace], error) {
	var workspace dagql.ObjectResult[*core.Workspace]
	if src.Self() == nil {
		return workspace, fmt.Errorf("module entrypoint workspace: module source is not set")
	}
	// A local module the caller's config declares clients for gets those too.
	if scoped, ok, err := callerScopedWorkspace(ctx, dag, src.Self()); err != nil {
		return workspace, fmt.Errorf("module entrypoint workspace: %w", err)
	} else if ok {
		return scoped, nil
	}
	root, err := sourceContextDirectory(src.Self())
	if err != nil {
		return workspace, fmt.Errorf("module entrypoint workspace: %w", err)
	}
	subpath := cleanSubpath(src.Self().SourceRootSubpath)
	if err := dag.Select(ctx, root, &workspace, dagql.Selector{
		Field: "asWorkspace",
		Args:  []dagql.NamedInput{{Name: "cwd", Value: dagql.String(subpath)}},
	}); err != nil {
		return workspace, fmt.Errorf("module entrypoint workspace at %q: %w", subpath, err)
	}
	return workspace, nil
}

// sourceContextDirectory returns the directory the entrypoint's workspace is
// rooted at. SourceRootSubpath is the module's directory within it.
//
// A source loaded from a workspace, and a local source, use the context the
// engine loaded: the manifest, the module's own files, and what its includes
// name. That is the loaded ContextDirectory, so a module that needs a file
// above its directory, such as the go.mod of a nested Go root, declares it in
// its includes. The full host tree is not used: it can be the caller's
// workspace.
//
// A git source uses the whole repository at the pinned commit, and a directory
// source the directory it was created from. Both are the module's own, so the
// include filter is not needed and files above the module stay visible. The
// git tree is content-addressed by its commit, so the workspace is too.
func sourceContextDirectory(src *core.ModuleSource) (dagql.ObjectResult[*core.Directory], error) {
	var dir dagql.ObjectResult[*core.Directory]
	if src.Workspace.Self() != nil {
		if src.ContextDirectory.Self() == nil {
			return dir, fmt.Errorf("workspace module source has no context directory")
		}
		return src.ContextDirectory, nil
	}
	switch src.Kind {
	case core.ModuleSourceKindGit:
		if src.Git == nil || src.Git.UnfilteredContextDir.Self() == nil {
			return dir, fmt.Errorf("git module source has no context directory")
		}
		return src.Git.UnfilteredContextDir, nil
	case core.ModuleSourceKindDir:
		if src.DirSrc == nil || src.DirSrc.OriginalContextDir.Self() == nil {
			return dir, fmt.Errorf("directory module source has no context directory")
		}
		return src.DirSrc.OriginalContextDir, nil
	case core.ModuleSourceKindLocal:
		if src.ContextDirectory.Self() == nil {
			return dir, fmt.Errorf("local module source has no context directory")
		}
		return src.ContextDirectory, nil
	default:
		return dir, fmt.Errorf("unsupported module source kind %q", src.Kind)
	}
}

func cleanSubpath(p string) string {
	if p == "" {
		return "."
	}
	return filepath.Clean(p)
}

// resolveSourceDirectory loads the directory named by entrypoint.source. A
// local path is relative to the directory that contains dagger-module.toml and
// is read from the module's own context directory, so it resolves the same way
// for local, git and directory module sources. A module reference resolves the
// way a dependency reference does. Any other value is an address that resolves
// to a Directory.
func resolveSourceDirectory(
	ctx context.Context,
	dag *dagql.Server,
	src dagql.ObjectResult[*core.ModuleSource],
	address string,
) (dagql.ObjectResult[*core.Directory], error) {
	var directory dagql.ObjectResult[*core.Directory]

	kind, err := classifySource(ctx, src, address)
	if err != nil {
		return directory, err
	}
	switch kind {
	case sourceKindAddress:
		err := dag.Select(ctx, dag.Root(), &directory,
			dagql.Selector{Field: "address", Args: []dagql.NamedInput{{Name: "value", Value: dagql.String(address)}}},
			dagql.Selector{Field: "directory"},
		)
		return directory, err
	case sourceKindModuleRef:
		return resolveModuleRefDirectory(ctx, dag, address)
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

// sourceKind is how entrypoint.source is resolved to a directory.
type sourceKind int

const (
	// sourceKindLocal is a path under the module directory.
	sourceKindLocal sourceKind = iota
	// sourceKindAddress is resolved by Address.directory: a module:function
	// address, or an scp-style git URL.
	sourceKindAddress
	// sourceKindModuleRef is a module reference such as
	// github.com/org/repo/dir@v1 or git://host/repo.git#main:dir. It resolves
	// the way a dependency reference resolves, so the same value names a
	// directory here and a module in runtime.source.
	sourceKindModuleRef
)

// classifySource decides how entrypoint.source resolves. An explicit path
// prefix or a dot-free value is local. A URL with a scheme, or a value the
// module reference parser recognizes as git, is a module reference. A value
// with a ":" but no scheme is an address, which covers module:function and
// scp-style git. An ambiguous value such as "example.com/repo" is local only
// when the path exists under the module directory, and a module reference
// otherwise.
func classifySource(
	ctx context.Context,
	src dagql.ObjectResult[*core.ModuleSource],
	source string,
) (sourceKind, error) {
	if strings.HasPrefix(source, ".") || strings.HasPrefix(source, "/") {
		return sourceKindLocal, nil
	}
	if strings.Contains(source, "://") {
		return sourceKindModuleRef, nil
	}
	if strings.Contains(source, ":") {
		return sourceKindAddress, nil
	}
	switch core.FastModuleSourceKindCheck(source, "") {
	case core.ModuleSourceKindLocal:
		return sourceKindLocal, nil
	case core.ModuleSourceKindGit:
		return sourceKindModuleRef, nil
	}
	if src.Self() == nil || src.Self().ContextDirectory.Self() == nil {
		return sourceKindModuleRef, nil
	}
	subpath, err := sourceSubpath(src.Self(), source)
	if err != nil {
		// not a usable local path, so it is a module reference
		return sourceKindModuleRef, nil //nolint:nilerr
	}
	contextDir := src.Self().ContextDirectory
	_, exists, err := core.StatFSExists(ctx, &core.DirectoryStatFS{Dir: contextDir}, subpath)
	if err != nil {
		return sourceKindLocal, fmt.Errorf("stat %q in module directory: %w", source, err)
	}
	if exists {
		return sourceKindLocal, nil
	}
	return sourceKindModuleRef, nil
}

// resolveModuleRefDirectory loads the directory a module reference names. It
// goes through the module source resolver so that the reference parses the
// way runtime.source and dependency references parse, and allowNotExists
// keeps it a plain directory: an entrypoint directory holds Dang files, not a
// module manifest, so nothing is parsed, loaded, or resolved beyond the tree.
//
// disableFindUp keeps the reference on the directory it names. An entrypoint
// directory usually sits below the manifest of the SDK that owns it. With
// find-up the resolver walks up to that manifest, loads the SDK module, and
// moves the source root to it, so the engine would evaluate the wrong Dang
// files.
func resolveModuleRefDirectory(
	ctx context.Context,
	dag *dagql.Server,
	ref string,
) (dagql.ObjectResult[*core.Directory], error) {
	var directory dagql.ObjectResult[*core.Directory]

	var modSrc dagql.ObjectResult[*core.ModuleSource]
	if err := dag.Select(ctx, dag.Root(), &modSrc, dagql.Selector{
		Field: "moduleSource",
		Args: []dagql.NamedInput{
			{Name: "refString", Value: dagql.String(ref)},
			{Name: "disableFindUp", Value: dagql.Boolean(true)},
			{Name: "allowNotExists", Value: dagql.Boolean(true)},
		},
	}); err != nil {
		return directory, err
	}

	contextDir := modSrc.Self().ContextDirectory
	if contextDir.Self() == nil {
		return directory, fmt.Errorf("module reference %q resolved to no directory", ref)
	}
	subpath := modSrc.Self().SourceRootSubpath
	if subpath == "" {
		subpath = "."
	}
	err := dag.Select(ctx, contextDir, &directory, dagql.Selector{
		Field: "directory",
		Args:  []dagql.NamedInput{{Name: "path", Value: dagql.String(subpath)}},
	})
	return directory, err
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
	defs := make([]*core.TypeDef, len(typeDefs))
	for i, typeDef := range typeDefs {
		defs[i] = typeDef.Self()
	}
	return validateConstructors(defs)
}

func validateConstructors(typeDefs []*core.TypeDef) error {
	var constructors []string
	for i, typeDef := range typeDefs {
		if typeDef == nil {
			return fmt.Errorf("module entrypoint type %d is null", i)
		}
		if typeDef.Kind != core.TypeDefKindObject {
			continue
		}
		// TypeDef.withKind sets the kind alone, so an entrypoint can return an
		// object type that carries no object definition.
		if !typeDef.AsObject.Valid || typeDef.AsObject.Value.Self() == nil {
			return fmt.Errorf("module entrypoint type %d has kind %s but defines no object", i, typeDef.Kind)
		}
		object := typeDef.AsObject.Value.Self()
		if !object.Constructor.Valid {
			continue
		}
		constructors = append(constructors, object.OriginalName)
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
