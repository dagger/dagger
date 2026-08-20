package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
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

// WorkspaceServedSchema derives the served GraphQL schema for a specific
// Workspace. Client-local workspaces stamp their owner's metadata for host
// routing, while runtime-backed schema operations remain authorized by the
// caller's held ClientScope. Module-bearing value workspaces instead start from
// the default core schema and load their complete configured module set from
// their own tree. The LLM needs the whole schema, not whatever a prior request
// demand-loaded.
//
// For the common case where the bound Workspace belongs to the calling client,
// this resolves to the same schema the CLI serves. An ordinary Directory-backed
// synthetic Workspace remains intentionally module-less and degrades to the
// current client's schema.
//
// When a client-local workspace carries a pending overlay that affects module
// loading — staged edits to a module's source, or to dagger.toml itself — the
// affected modules are re-resolved through the overlay and REPLACE their served
// counterparts in the derived schema (newly-configured ones are appended).
// Module-bearing values use the same layering path for their complete configured
// set, with default core dependencies as the base so no caller-local modules can
// leak in. The client-local served set is a snapshot of the on-disk workspace
// taken at session start; without replacement, an agent that edits its own
// modules and recomposes (Workspace.agents) would compose the fresh module but
// serve tools from the stale schema. Callers that resolve client-scoped root
// fields directly under [WorkspaceServedContext] (e.g. currentTypeDefs
// introspection) still see the served snapshot; tree layering applies to the
// derived schema only.
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
	deps, err = workspaceOverlayServedDeps(wsCtx, ws, deps)
	if err != nil {
		return nil, fmt.Errorf("workspace overlay modules: %w", err)
	}
	return deps.Schema(wsCtx)
}

// A layered SchemaBuilder must be stable so its lazily-built schema server is
// reused across LLM tool listings and dispatches. The cache lives on the
// builder's root Query rather than globally: both the builder and its lazy
// server retain that root, so global storage would make historical client
// schema graphs independently reachable. The base-builder pointer distinguishes
// served-deps snapshots; the workspace ID distinguishes overlay states. Keeping
// the cache on Query also preserves reuse across fresh DefaultDeps clones.
const workspaceSchemaCacheLimit = 64

type workspaceSchemaCacheKey struct {
	builder   *schemaBuilderIdentity
	workspace string
}

func newWorkspaceSchemaCacheKey(deps *SchemaBuilder, workspace string) workspaceSchemaCacheKey {
	return workspaceSchemaCacheKey{
		builder:   deps.workspaceSchemaIdentity(),
		workspace: workspace,
	}
}

func cachedWorkspaceSchema(deps *SchemaBuilder, key workspaceSchemaCacheKey) (*SchemaBuilder, bool) {
	if deps == nil || deps.root == nil {
		return nil, false
	}
	root := deps.root
	root.workspaceSchemaCacheMu.Lock()
	defer root.workspaceSchemaCacheMu.Unlock()
	cached, ok := root.workspaceSchemaCache[key]
	return cached, ok
}

func cacheWorkspaceSchema(deps *SchemaBuilder, key workspaceSchemaCacheKey, layered *SchemaBuilder) {
	if deps == nil || deps.root == nil {
		return
	}
	root := deps.root
	root.workspaceSchemaCacheMu.Lock()
	defer root.workspaceSchemaCacheMu.Unlock()
	if len(root.workspaceSchemaCache) >= workspaceSchemaCacheLimit {
		root.workspaceSchemaCache = map[workspaceSchemaCacheKey]*SchemaBuilder{}
	}
	if root.workspaceSchemaCache == nil {
		root.workspaceSchemaCache = map[workspaceSchemaCacheKey]*SchemaBuilder{}
	}
	root.workspaceSchemaCache[key] = layered
}

// workspaceOverlayServedDeps layers modules resolved from the workspace tree
// onto the base deps. For client-local overlays these replace only affected
// served modules; for module-bearing values they are the complete configured
// set layered onto default core deps. No tree influence returns deps unchanged.
// If a module cannot load, the whole tree layer is discarded and the base deps
// are retained. Explicit Workspace.agents recomposition remains strict; this
// fallback only keeps an already-composed LLM's repair tools callable.
func workspaceOverlayServedDeps(ctx context.Context, ws dagql.ObjectResult[*Workspace], deps *SchemaBuilder) (*SchemaBuilder, error) {
	if _, ok := ws.Self().OverlayChanges(); !ok && !ws.Self().IsModuleBearingValue() {
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
	key := newWorkspaceSchemaCacheKey(deps, wsKey)

	if cached, ok := cachedWorkspaceSchema(deps, key); ok {
		return cached, nil
	}

	overlayMods, err := WorkspaceOverlayModules(ctx, ws)
	if err != nil {
		// Keep the last-known-good served schema usable when a staged edit makes
		// one of the agent's own tool modules invalid. The workspace ID keys this
		// fallback in the cache below, so repairing the file produces a new ID and
		// retries overlay loading instead of poisoning the rest of the session.
		slog.SpanLogger(ctx, InstrumentationLibrary).Warn(
			"failed to load workspace overlay modules; using served modules for recovery",
			"error", err,
		)
		overlayMods = nil
	}
	layered := deps
	if len(overlayMods) > 0 {
		mods := make([]Mod, len(overlayMods))
		for i, mod := range overlayMods {
			mods[i] = NewUserMod(mod)
		}
		layered = deps.Replacing(mods...)
	}

	cacheWorkspaceSchema(deps, key, layered)
	return layered, nil
}

// WorkspaceServedContext stamps client-local workspace owner metadata for host
// routing and forces the caller's leased runtime to load its served modules.
// It never treats owner metadata as authority to execute against another
// runtime. Module-bearing values have no owner metadata or served snapshot, so
// their schema starts from default deps and loads modules from the value tree in
// [WorkspaceServedSchema].
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
