package entrypoint

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/hashutil"
)

// SourcesWorkspace returns the workspace to pass to an entrypoint's sources
// member. Its root is the module's context root and its working directory is
// the module directory, so a Directory built from the workspace root has the
// module at SourceRootSubpath, where the module context has it.
//
// The workspace reads the module's own source: the workspace that loaded it,
// the host checkout of a local module, or the tree of a Git or directory
// module. It is never the caller's workspace for a module from elsewhere.
func SourcesWorkspace(
	ctx context.Context,
	dag *dagql.Server,
	src *core.ModuleSource,
) (dagql.ObjectResult[*core.Workspace], error) {
	var workspace dagql.ObjectResult[*core.Workspace]
	cwd := cleanSubpath(src.SourceRootSubpath)
	asWorkspace := func(root dagql.ObjectResult[*core.Directory]) (dagql.ObjectResult[*core.Workspace], error) {
		err := dag.Select(ctx, root, &workspace, dagql.Selector{
			Field: "asWorkspace",
			Args:  []dagql.NamedInput{{Name: "cwd", Value: dagql.Opt(dagql.String(cwd))}},
		})
		return workspace, err
	}
	switch {
	case src.Workspace.Self() != nil:
		err := dag.Select(ctx, src.Workspace, &workspace, dagql.Selector{
			Field: "withWorkdir",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.String(cwd)}},
		})
		return workspace, err
	case src.Kind == core.ModuleSourceKindLocal:
		return localSourcesWorkspace(ctx, dag, src, cwd)
	case src.Kind == core.ModuleSourceKindGit:
		return asWorkspace(src.Git.UnfilteredContextDir)
	case src.Kind == core.ModuleSourceKindDir:
		return asWorkspace(src.DirSrc.OriginalContextDir)
	default:
		return workspace, fmt.Errorf("module source kind %q has no workspace", src.Kind)
	}
}

// localSourcesWorkspace returns a host workspace on the local module's context
// root, owned by the client that the engine reads that context through, so
// the sources member reads exactly where the module context comes from, also
// for a module outside the caller's workspace.
func localSourcesWorkspace(
	ctx context.Context,
	dag *dagql.Server,
	src *core.ModuleSource,
	cwd string,
) (dagql.ObjectResult[*core.Workspace], error) {
	var none dagql.ObjectResult[*core.Workspace]
	root, err := src.LocalContextDirectoryPath()
	if err != nil {
		return none, err
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return none, err
	}
	owner, err := query.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return none, fmt.Errorf("get module source client: %w", err)
	}
	ws := &core.Workspace{Cwd: cwd, ClientID: owner.ClientID}
	ws.SetHostPath(root)
	ws.SetSource(core.NewWorkspaceSourceClientLocal(root))

	// No field builds a host workspace on an arbitrary path, so the result is
	// synthetic and attached to the session for the nested client to load.
	args := []*dagql.ResultCallArg{
		{Name: "clientID", Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindString, StringValue: owner.ClientID}},
		{Name: "root", Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindString, StringValue: root}},
		{Name: "cwd", Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindString, StringValue: cwd}},
	}
	inst, err := dagql.NewObjectResultForCall(ws, dag, &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "entrypoint_sources_workspace",
		Type:        dagql.NewResultCallType(ws.Type()),
		Args:        args,
	})
	if err != nil {
		return none, err
	}
	inst, err = inst.WithContentDigest(ctx, hashutil.HashStrings("entrypoint_sources_workspace", owner.ClientID, root, cwd))
	if err != nil {
		return none, err
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return none, err
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return none, err
	}
	attached, err := cache.AttachResult(ctx, clientMetadata.SessionID, dag, inst)
	if err != nil {
		return none, err
	}
	workspace, ok := attached.(dagql.ObjectResult[*core.Workspace])
	if !ok {
		return none, fmt.Errorf("attach entrypoint sources workspace: unexpected result %T", attached)
	}
	return workspace, nil
}
