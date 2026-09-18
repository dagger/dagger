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
	"github.com/dagger/dagger/engine"
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

// Workspace returns the workspace to pass to a module entrypoint: a workspace
// rooted at the module's context, with its working directory at the module's
// directory.
//
// The engine already treats the workspace cwd as the module's scope when it
// hands a workspace to an SDK, so an entrypoint can find the module it serves
// through relative workspace paths, without a path generated into it. An
// entrypoint can still read above the module with an absolute workspace path.
//
// A source that carries a workspace is scoped within that workspace, because
// its subpath is relative to it. Workspace.moduleSource attaches one on a local
// or Git workspace. The current workspace can be a different tree, such as the
// one a module's process finds in its own container.
//
// A local source without an attached workspace is scoped within the current
// workspace when host paths place the module under that workspace's root. Any
// other source is in no workspace of the caller's, so the entrypoint gets a
// workspace built from the module's own source instead. It never gets the
// caller's workspace, whose files belong to someone else.
func Workspace(
	ctx context.Context,
	dag *dagql.Server,
	src dagql.ObjectResult[*core.ModuleSource],
) (dagql.ObjectResult[*core.Workspace], error) {
	var workspace dagql.ObjectResult[*core.Workspace]
	var subpath string
	if src.Self() != nil && src.Self().Workspace.Self() != nil {
		workspace = src.Self().Workspace
		subpath = cleanSubpath(src.Self().SourceRootSubpath)
	} else {
		if err := dag.Select(ctx, dag.Root(), &workspace, dagql.Selector{Field: "currentWorkspace"}); err != nil {
			return workspace, fmt.Errorf("get module workspace: %w", err)
		}
		var ok bool
		subpath, ok = moduleWorkspacePath(workspace.Self(), src.Self())
		if !ok {
			return sourceWorkspace(ctx, dag, src.Self())
		}
	}
	// withWorkdir takes a path relative to the workspace root, so reset first
	// in case the workspace already has a working directory.
	var scoped dagql.ObjectResult[*core.Workspace]
	if err := dag.Select(ctx, workspace, &scoped,
		dagql.Selector{Field: "withWorkdir", Args: []dagql.NamedInput{{Name: "path", Value: dagql.String(".")}}},
		dagql.Selector{Field: "withWorkdir", Args: []dagql.NamedInput{{Name: "path", Value: dagql.String(subpath)}}},
	); err != nil {
		return workspace, fmt.Errorf("set module entrypoint workspace directory %q: %w", subpath, err)
	}
	return scoped, nil
}

// moduleWorkspacePath returns the module directory relative to the workspace
// root, and whether the module is in the workspace at all.
//
// It applies to sources without an attached workspace. The engine's own module
// loader goes through Query.moduleSource, which attaches none. There
// SourceRootSubpath is relative to the source's context directory, so the path
// is derived from the host paths of the context directory and the workspace
// root. A git or directory source without a workspace has no place in the
// workspace.
//
// A rootless workspace, which the engine detects where it finds no workspace
// root, holds no files, so no module is in it.
func moduleWorkspacePath(ws *core.Workspace, src *core.ModuleSource) (string, bool) {
	if ws == nil || src == nil {
		return "", false
	}
	if src.Kind != core.ModuleSourceKindLocal || src.Local == nil {
		return "", false
	}
	if _, rootless := ws.BaseSource().(*core.WorkspaceSourceRootlessLocal); rootless {
		return "", false
	}
	root := ws.HostPath()
	if root == "" || src.Local.ContextDirectoryPath == "" {
		return "", false
	}
	moduleDir := filepath.Join(src.Local.ContextDirectoryPath, cleanSubpath(src.SourceRootSubpath))
	rel, err := filepath.Rel(root, moduleDir)
	if err != nil || !filepath.IsLocal(rel) {
		// The module is not under the workspace root.
		return "", false
	}
	return rel, true
}

// sourceWorkspace builds the workspace of a module that is in no workspace of
// the caller's. Its root is the module's full context directory and its
// working directory is the module's directory.
func sourceWorkspace(
	ctx context.Context,
	dag *dagql.Server,
	src *core.ModuleSource,
) (dagql.ObjectResult[*core.Workspace], error) {
	var workspace dagql.ObjectResult[*core.Workspace]
	if src == nil {
		return workspace, fmt.Errorf("module entrypoint workspace: module source is not set")
	}
	root, err := sourceContextDirectory(ctx, dag, src)
	if err != nil {
		return workspace, fmt.Errorf("module entrypoint workspace: %w", err)
	}
	subpath := cleanSubpath(src.SourceRootSubpath)
	if err := dag.Select(ctx, root, &workspace, dagql.Selector{
		Field: "asWorkspace",
		Args:  []dagql.NamedInput{{Name: "cwd", Value: dagql.String(subpath)}},
	}); err != nil {
		return workspace, fmt.Errorf("module entrypoint workspace at %q: %w", subpath, err)
	}
	return workspace, nil
}

// sourceContextDirectory returns the context directory of a module source
// without the include filter of its manifest. The filtered ContextDirectory
// holds only the manifest and the module's own files, so it would drop files
// above the module, such as the go.mod of a nested Go root.
//
// A git context is the whole repository at the pinned commit, content-addressed
// by that commit. A directory context is the directory the module source was
// created from. A local context is read from the host of the client that loaded
// the module, the way the module source itself is read.
func sourceContextDirectory(
	ctx context.Context,
	dag *dagql.Server,
	src *core.ModuleSource,
) (dagql.ObjectResult[*core.Directory], error) {
	var dir dagql.ObjectResult[*core.Directory]
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
		if src.Local == nil || src.Local.ContextDirectoryPath == "" {
			return dir, fmt.Errorf("local module source has no context directory")
		}
		query, err := core.CurrentQuery(ctx)
		if err != nil {
			return dir, err
		}
		clientMetadata, err := query.NonModuleParentClientMetadata(ctx)
		if err != nil {
			return dir, fmt.Errorf("get client metadata for local module source: %w", err)
		}
		ctx = engine.ContextWithClientMetadata(ctx, clientMetadata)
		if err := dag.Select(ctx, dag.Root(), &dir,
			dagql.Selector{Field: "host"},
			dagql.Selector{
				Field: "directory",
				Args:  []dagql.NamedInput{{Name: "path", Value: dagql.String(src.Local.ContextDirectoryPath)}},
			},
		); err != nil {
			return dir, fmt.Errorf("load local module context %q: %w", src.Local.ContextDirectoryPath, err)
		}
		// A worktree or submodule checkout has a .git pointer file at its root,
		// whose target is outside the context. Workspace reads drop it too.
		return core.DropRootGitPointerFile(ctx, dag, dir)
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
