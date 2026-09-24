package schema

import (
	"context"
	"fmt"
	"path"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

// moduleTreePath resolves a local address against a module's own tree: an
// absolute address from the tree's root, a relative one from the module's
// directory. The result is relative to the tree's root, and never leaves it.
func moduleTreePath(moduleDir, address string) (string, error) {
	var target string
	if path.IsAbs(address) {
		target = path.Clean(address)[1:]
	} else {
		target = path.Join(moduleDir, address)
	}
	if target == "" {
		target = "."
	}
	if !filepath.IsLocal(target) && target != "." {
		return "", fmt.Errorf("%q leaves the module's own tree", address)
	}
	return target, nil
}

// callerTreeModuleSource loads the module at a local address inside the tree
// the calling module was loaded from: its git repository at the pinned commit,
// the directory it was built from, or its context directory on the host.
func callerTreeModuleSource(
	ctx context.Context,
	dag *dagql.Server,
	caller dagql.ObjectResult[*core.Module],
	address string,
) (inst dagql.ObjectResult[*core.ModuleSource], _ error) {
	if !caller.Self().Source.Valid || caller.Self().Source.Value.Self() == nil {
		return inst, fmt.Errorf("module %q has no source to resolve %q in", caller.Self().Name(), address)
	}
	src := caller.Self().Source.Value.Self()
	target, err := moduleTreePath(src.SourceRootSubpath, address)
	if err != nil {
		return inst, err
	}
	switch src.Kind {
	case core.ModuleSourceKindGit:
		return directoryTreeModuleSource(ctx, dag, src.Git.UnfilteredContextDir, target)
	case core.ModuleSourceKindDir:
		return directoryTreeModuleSource(ctx, dag, src.DirSrc.OriginalContextDir, target)
	case core.ModuleSourceKindLocal:
		return hostTreeModuleSource(ctx, dag, src, target)
	default:
		return inst, fmt.Errorf("module %q has no tree to resolve %q in", caller.Self().Name(), address)
	}
}

func directoryTreeModuleSource(
	ctx context.Context,
	dag *dagql.Server,
	tree dagql.ObjectResult[*core.Directory],
	target string,
) (inst dagql.ObjectResult[*core.ModuleSource], _ error) {
	if tree.Self() == nil {
		return inst, fmt.Errorf("the calling module's tree is not loaded")
	}
	err := dag.Select(ctx, tree, &inst, dagql.Selector{
		Field: "asModuleSource",
		Args:  []dagql.NamedInput{{Name: "sourceRootPath", Value: dagql.String(target)}},
	})
	return inst, err
}

func hostTreeModuleSource(
	ctx context.Context,
	dag *dagql.Server,
	src *core.ModuleSource,
	target string,
) (inst dagql.ObjectResult[*core.ModuleSource], _ error) {
	root, err := src.LocalContextDirectoryPath()
	if err != nil {
		return inst, err
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	// The module's files are on the host of the client that loaded it, not of
	// the process calling serveModule.
	hostMetadata, err := query.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return inst, err
	}
	if err := dag.Select(engine.ContextWithClientMetadata(ctx, hostMetadata), dag.Root(), &inst, dagql.Selector{
		Field: "moduleSource",
		Args: []dagql.NamedInput{
			{Name: "refString", Value: dagql.String(filepath.Join(root, target))},
			{Name: "disableFindUp", Value: dagql.Boolean(true)},
			{Name: "requireKind", Value: dagql.Opt(core.ModuleSourceKindLocal)},
		},
	}); err != nil {
		return inst, err
	}
	loaded := inst.Self()
	rel, err := filepath.Rel(root, filepath.Join(loaded.Local.ContextDirectoryPath, loaded.SourceRootSubpath))
	if err != nil || (!filepath.IsLocal(rel) && rel != ".") {
		return inst, fmt.Errorf("%q leaves the module's own tree", target)
	}
	return inst, nil
}
