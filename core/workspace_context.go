package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

type workspaceContextKey struct{}

// WorkspaceToContext binds a Workspace into the context so that module function
// execution resolves against it: contextual (+defaultPath) arguments and
// Workspace-typed arguments are resolved from this Workspace rather than the
// ambient current workspace.
//
// This is the Workspace-based counterpart to [EnvToContext]. Group runs
// (GeneratorGroup, CheckGroup, UpGroup) thread the workspace they were rolled
// up from through it, so every leaf across the group's modules receives the
// same workspace — under the same dagql ID — rather than each leaf re-deriving
// a per-call equivalent that defeats (module, workspace)-keyed caching.
//
// It is also threaded at LLM tool dispatch when the LLM is bound to
// a Workspace (via LLM.withWorkspace), letting the agent operate on its own
// (possibly overlaid) Workspace.
func WorkspaceToContext(ctx context.Context, ws dagql.ObjectResult[*Workspace]) context.Context {
	return context.WithValue(ctx, workspaceContextKey{}, ws)
}

// WorkspaceFromContext returns the Workspace bound into the context by
// [WorkspaceToContext], if any.
//
// Unlike [EnvFromContext] there is no server-side fallback: a Workspace only
// enters the context through an explicit in-process binding at tool dispatch.
// Ordinary, non-bound calls return ok=false and continue resolving context the
// existing way (an Env, or the module source).
func WorkspaceFromContext(ctx context.Context) (dagql.ObjectResult[*Workspace], bool) {
	if ws, ok := ctx.Value(workspaceContextKey{}).(dagql.ObjectResult[*Workspace]); ok && ws.Self() != nil {
		return ws, true
	}
	return dagql.ObjectResult[*Workspace]{}, false
}

// workspaceHostRoutingContext stamps the Workspace owner's immutable metadata
// onto ctx for record and host-routing operations. It deliberately does not
// replace the held ClientScope: metadata is not runtime execution authority, so
// schema/module operations continue to execute under the caller's leased scope.
// Synthetic/value workspaces have no owning client and leave ctx unchanged.
//
// This mirrors core/schema's withWorkspaceClientContext, reimplemented here so
// the LLM's schema derivation ([WorkspaceServedSchema]) needs no core→schema
// import.
func workspaceHostRoutingContext(ctx context.Context, ws *Workspace) (context.Context, error) {
	if ws.ClientID == "" {
		return ctx, nil
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	clientMetadata, err := query.SpecificClientMetadata(ctx, ws.ClientID)
	if err != nil {
		return nil, fmt.Errorf("workspace client metadata: %w", err)
	}
	return engine.ContextWithClientMetadata(ctx, clientMetadata), nil
}

// WorkspaceServedSchema returns the stable served GraphQL schema for a specific
// Workspace. Client-local workspaces stamp their owner's metadata for host
// routing, while runtime-backed schema operations remain authorized by the
// caller's held ClientScope. Module-bearing value workspaces have no served
// module snapshot and therefore use the default core schema.
//
// Pending workspace overlays are deliberately not resolved here. An LLM's
// bound object tools retain the schemas that defined them when the LLM was
// composed, so ordinary tool listing and dispatch must not compile staged module
// edits or silently adopt a new module version. Explicit Workspace.agents
// composition is the strict boundary that resolves overlays and returns a new
// LLM with newly bound defining schemas.
func WorkspaceServedSchema(ctx context.Context, ws dagql.ObjectResult[*Workspace]) (*dagql.Server, error) {
	wsCtx, err := WorkspaceServedContext(ctx, ws)
	if err != nil {
		return nil, err
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	var deps *SchemaBuilder
	if ws.Self().IsModuleBearingValue() {
		deps, err = query.DefaultDeps(wsCtx)
	} else {
		deps, err = query.CurrentServedDeps(wsCtx)
	}
	if err != nil {
		return nil, fmt.Errorf("workspace served deps: %w", err)
	}
	return deps.Schema(wsCtx)
}

// WorkspaceServedContext stamps client-local workspace owner metadata for host
// routing and forces the caller's leased runtime to load its served modules.
// It never treats owner metadata as authority to execute against another
// runtime. Module-bearing values have no owner metadata or served snapshot, so
// their tool bindings supply module schemas captured during composition.
func WorkspaceServedContext(ctx context.Context, ws dagql.ObjectResult[*Workspace]) (context.Context, error) {
	wsCtx, err := workspaceHostRoutingContext(ctx, ws.Self())
	if err != nil {
		return nil, err
	}
	if ws.Self().IsModuleBearingValue() {
		return wsCtx, nil
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := query.EnsureWorkspaceModules(wsCtx, nil, false); err != nil {
		return nil, fmt.Errorf("ensure workspace modules: %w", err)
	}
	return wsCtx, nil
}
