package core

import (
	"context"
	"fmt"
	"sync"

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
//
// When the workspace carries a pending overlay that affects module loading —
// staged edits to a module's source, or to dagger.toml itself — the affected
// modules are re-resolved through the overlay and REPLACE their served
// counterparts in the derived schema (newly-configured ones are appended). The
// served set is a snapshot of the on-disk workspace taken at session start;
// without this, an agent that edits its own modules and recomposes
// (Workspace.agents) would compose the fresh module but serve tools from the
// stale schema. Callers that resolve client-scoped root fields directly under
// [WorkspaceServedContext] (e.g. currentTypeDefs introspection) still see the
// served snapshot; the overlay layering applies to the derived schema only.
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
	deps, err = workspaceOverlayServedDeps(wsCtx, ws, deps)
	if err != nil {
		return nil, fmt.Errorf("workspace overlay modules: %w", err)
	}
	return deps.Schema(wsCtx)
}

// overlayDepsCache memoizes the overlay-layered SchemaBuilder per (served deps
// instance, workspace value). The layered builder must be a stable instance so
// its own lazily-built schema server is reused across LLM steps: MCP.Server
// derives the schema on every tool listing and dispatch, and rebuilding it each
// time would re-install every module's typedefs per call. Keying on the served
// builder's pointer identity makes a change to the client's served set (which
// produces a new builder) miss naturally; keying on the workspace ID digest
// makes any further overlay edit miss naturally. Bounded: reaching the cap
// (many distinct overlay states composed in one engine's lifetime) clears the
// map, which only costs a rebuild on next use.
var overlayDepsCache = struct {
	sync.Mutex
	m map[string]*SchemaBuilder
}{m: map[string]*SchemaBuilder{}}

const overlayDepsCacheLimit = 64

// workspaceOverlayServedDeps layers the workspace overlay's re-resolved modules
// onto the served deps, replacing same-named entries. No overlay influence
// returns deps unchanged.
func workspaceOverlayServedDeps(ctx context.Context, ws dagql.ObjectResult[*Workspace], deps *SchemaBuilder) (*SchemaBuilder, error) {
	if _, ok := ws.Self().OverlayChanges(); !ok {
		return deps, nil
	}

	wsID, err := ws.ID()
	if err != nil {
		return nil, err
	}
	// A workspace loaded in this session may carry a handle-form ID (an engine
	// result handle) instead of a recipe; handles have no digest but their
	// result ID is just as unique to the value.
	var wsKey string
	if wsID.IsHandle() {
		wsKey = fmt.Sprintf("handle:%d", wsID.EngineResultID())
	} else {
		wsKey = wsID.Digest().String()
	}
	key := fmt.Sprintf("%p|%s", deps, wsKey)

	overlayDepsCache.Lock()
	cached, ok := overlayDepsCache.m[key]
	overlayDepsCache.Unlock()
	if ok {
		return cached, nil
	}

	overlayMods, err := WorkspaceOverlayModules(ctx, ws)
	if err != nil {
		return nil, err
	}
	layered := deps
	if len(overlayMods) > 0 {
		mods := make([]Mod, len(overlayMods))
		for i, mod := range overlayMods {
			mods[i] = NewUserMod(mod)
		}
		layered = deps.Replacing(mods...)
	}

	overlayDepsCache.Lock()
	if len(overlayDepsCache.m) >= overlayDepsCacheLimit {
		overlayDepsCache.m = map[string]*SchemaBuilder{}
	}
	overlayDepsCache.m[key] = layered
	overlayDepsCache.Unlock()
	return layered, nil
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
