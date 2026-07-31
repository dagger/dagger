package core

import (
	"context"

	"github.com/dagger/dagger/dagql"
)

type workspaceContextKey struct{}

// WorkspaceToContext binds a Workspace into the context so that module function
// execution resolves against it: Workspace-typed arguments are resolved from
// this Workspace rather than the ambient current workspace.
//
// This is the Workspace-based counterpart to [EnvToContext]. Group runs
// (GeneratorGroup, CheckGroup, UpGroup) thread the workspace they were rolled
// up from through it, so every leaf across the group's modules receives the
// same workspace — under the same dagql ID — rather than each leaf re-deriving
// a per-call equivalent that defeats (module, workspace)-keyed caching.
func WorkspaceToContext(ctx context.Context, ws dagql.ObjectResult[*Workspace]) context.Context {
	return context.WithValue(ctx, workspaceContextKey{}, ws)
}

// WorkspaceFromContext returns the Workspace bound into the context by
// [WorkspaceToContext], if any.
//
// Unlike [EnvFromContext] there is no server-side fallback: a Workspace only
// enters the context through an explicit in-process binding. Ordinary,
// non-bound calls return ok=false and continue resolving context the existing
// way (the module source / ambient workspace).
func WorkspaceFromContext(ctx context.Context) (dagql.ObjectResult[*Workspace], bool) {
	if ws, ok := ctx.Value(workspaceContextKey{}).(dagql.ObjectResult[*Workspace]); ok && ws.Self() != nil {
		return ws, true
	}
	return dagql.ObjectResult[*Workspace]{}, false
}
