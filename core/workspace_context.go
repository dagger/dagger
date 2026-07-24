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
// This is the Workspace-based counterpart to [EnvToContext]. It is threaded at
// LLM tool dispatch when the LLM is bound to a Workspace (via LLM.withWorkspace),
// letting the agent operate on its own (possibly overlaid) Workspace.
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

// workspaceClientContext switches ctx to the Workspace's owning client so that
// client-scoped resolvers — CurrentServedDeps, EnsureWorkspaceModules — resolve
// against the workspace's own served modules rather than whichever client is
// currently executing. Synthetic/value workspaces have no owning client, so ctx
// is returned unchanged and resolution falls back to the current client.
//
// This mirrors core/schema's withWorkspaceClientContext, reimplemented here so
// the LLM's schema derivation ([WorkspaceServedSchema]) needs no core→schema
// import.
func workspaceClientContext(ctx context.Context, ws *Workspace) (context.Context, error) {
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

// WorkspaceServedSchema derives the served GraphQL schema for a specific
// Workspace, independent of which client is currently executing. It switches to
// the workspace's owning client (so the served-module set reflects that
// workspace's dagger.toml / installed modules), forces the full module set to
// load — the LLM needs the whole schema, not whatever a prior request
// demand-loaded — and returns the built schema server.
//
// This is what makes the LLM's schema derive from its OWN Workspace (the one it
// was bound to via LLM.withWorkspace) rather than from the outer client's env.
// For the common case where the bound Workspace is the current client's
// workspace, the owning client is the current client, so this resolves to the
// same schema the CLI serves. For a value/synthetic Workspace (no owning client
// or config) it degrades gracefully to the current client's schema.
func WorkspaceServedSchema(ctx context.Context, ws dagql.ObjectResult[*Workspace]) (*dagql.Server, error) {
	wsCtx, err := WorkspaceServedContext(ctx, ws)
	if err != nil {
		return nil, err
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	deps, err := query.CurrentServedDeps(wsCtx)
	if err != nil {
		return nil, fmt.Errorf("workspace served deps: %w", err)
	}
	return deps.Schema(wsCtx)
}

// WorkspaceServedContext switches ctx to the Workspace's owning client and
// forces its served modules to load, returning the switched context. Under this
// context the client-scoped resolvers (CurrentServedDeps, currentTypeDefs,
// currentModule) see the workspace's OWN served schema — the same switch
// [WorkspaceServedSchema] makes for the schema server, exposed separately so
// callers that resolve those root fields directly (e.g. the LLM's inspect tool
// enumerating module entrypoints) resolve them against the same workspace.
func WorkspaceServedContext(ctx context.Context, ws dagql.ObjectResult[*Workspace]) (context.Context, error) {
	wsCtx, err := workspaceClientContext(ctx, ws.Self())
	if err != nil {
		return nil, err
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
