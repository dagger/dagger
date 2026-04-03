# WHITEBOARD

## TODO
* Assess changeset merge decision to always use git path (removed `conflicts.IsEmpty()` no-git fast path), with specific focus on performance impact
   * Compare runtime/cost of old no-git path vs current always-git path in no-conflict workloads
   * Confirm whether correctness/cohesion benefits outweigh any measured regression and document outcome
* Sweep remaining raw `srv.Select(..., &ptr, ...)` / `.Self()` ownership boundaries after the current result-detach fixes
   * Confirm there are no more places where a releasable `*Directory` / `*File` / `*Container` pointer is being re-exposed after loading from dagql
   * In particular, keep checking for any helper that unwraps a selected result to a raw pointer and then returns or re-wraps that same pointer
* Remove internal `__immutableRef` schema API once and for all
   * Replace remaining stable-ID use cases with a cleaner non-internal API pattern in dagql/core
* Review the new HTTP implementation for clarity/cohesion
   * Current implementation is functional but confusing; do a low-priority cleanup pass
* Fix `query.__schemaJSONFile` implementation to avoid embedding megabytes of file contents in query args
   * Build/write via ref/snapshot path directly instead of passing huge inline string payloads through select args
* Clean up `cloneContainerForTerminal` usage
   * Find a cleaner container-child pattern for terminal/service callsites instead of special clone helper
* replacing CurrentOpOpts CauseCtx with trace.SpanContextFromContext seems sus, needs checking
* Reassess file mutator parent-passing + lazy-init shape (`WithName`/`WithTimestamps`/`Chown`/`WithReplaced`)
   * Current implementation passes parent object results through schema into core and appears correct in tests, but may not be the most cohesive long-term model.
   * Follow-up: revisit whether lazy-init/parent snapshot modeling can eliminate this explicit parent threading while preserving correctness for service-backed files.
* Assess whether we dropped any git lazyness (especially tree) and whether we should restore it
* Assess whether we really want persistent cache for every schema json file, that's probably a lot of files that are actually kinda sizable!

## Notes
* **THE DAGQL CACHE IS A SINGLETON CREATED ONCE AT ENGINE START AND IT LIVES FOR THE ENTIRE LIFETIME OF THE ENGINE.**
  * There is not a second DAGQL cache.
  * There is not a per-session DAGQL cache.
  * Result-call planning/runtime code should not be written as if cache identity were ambiguous.
  * If a code path needs the DAGQL cache, it should explicitly use or fetch the singleton cache rather than storing mutable cache backpointers on frame/helper structs.
* **CONTAINERD IS TRUSTED. IT IS IN OUR FULL CONTROL. IN THIS USE CASE IT IS NOT SHARED WITH OR DRIVEN BY ANY OTHER SYSTEM.**
  * We should be entirely willing to use containerd directly where it is the right substrate.
  * We should not duplicate state in dagql that containerd already stores correctly and authoritatively for us.
  * Dagql persistence should store only the Dagger-specific state that containerd does not already represent.
* For persistence, it's basically like an export. Don't try to store in-engine numeric ids or something, it's the whole DAG persisted in single-engine-agnostic manner. When loading from persisted disk, you are importing it (including e-graph union and stuff)
  * But also for now, let's be biased towards keeping everything in memory rather than trying to do fancy page out to disk

* **CRITICAL CACHE MODEL RULE: OVERLAPPING DIGESTS MEAN EQUALITY AND FULL INTERCHANGEABILITY.**
  * If two values share any digest / end up in the same digest-equivalence set, that is not merely "evidence" or "similarity"; it means they are the same value for dagql cache purposes and may be reused interchangeably.

* **CRITICAL OWNERSHIP RULE: NEVER RE-EXPOSE A RAW DAGQL-LOADED POINTER FOR A RELEASEABLE OBJECT.**
  * This is a crucial design constraint for the hard-cutover cache/snapshot model.
  * If a helper loads or selects a `Directory`, `File`, or `Container` from dagql, then returning the raw `*Directory` / `*File` / `*Container` pointer is dangerous unless that object is explicitly detached first.
  * The reason is concrete and non-negotiable: these objects own snapshot refs via `OnRelease`, so pointer aliasing can poison some other owner when one result is released.
  * Avoiding this entire class of bugs is crucial.
  * The preferred fix is to preserve `dagql.ObjectResult[*T]` all the way through whenever possible.
  * If a raw `*T` really must be returned, it must be a detached object with reopened/refreshed ownership of any releaseable snapshot state.
  * Internal `Value` shells embedded in larger objects are not public results and must not be handed back out directly as if they were.

* A lot of eval'ing of lazy stuff is just triggered inline now; would be nice if dagql cache scheduler knew about these and could do that in parallel for ya
   * This is partially a pre-existing condition though, so not a big deal yet. But will probably make a great optimization in the near-ish future

# Rebase

Current branch: `egraph` @ `4f4b8713b`
Upstream target: `upstream/main` @ `9db1734ca`
Merge base: `cae179bb9`
Incoming commits: `47`
Local-only commits: `287`

## Status
* This rebase is not a mechanical conflict-resolution exercise; the important runtime/cache commits need translation into the current dagql-owned ownership and cache model.
* For `a189dc9b2`, we kept the native runtime abstraction and builtin `dang` support, but explicitly did **not** bring over the old BuildKit custom-op caching design.
* `Module.Runtime` stays container-only state on `Module`, and `module.runtime` is nullable so native runtimes do not have to pretend they are containers.
* Old temporary module/result bookkeeping stays dead; no `DangEvalOp`, no `FunctionCall.Module`, no `Module.ResultID`, no BuildKit custom-op registration for Dang.
* Internal `dagger.json` files that already pin the external Dang SDK stay pinned; we did **not** hard-cut those to `"dang"`.

Likely tricky buckets:
* Runtime / cache / module generation changes:
  * `a189dc9b2` `add native Dang runtime (#12008)`
  * `7006d26d5` `feat(secretprovider): add Google Cloud Secret Manager support (#10510)`
  * `fb9776fd5` `Implement OIDC integration for vault secrets provider (#11929)`
  * `630e6c290` `Handle missing cached git auth secrets (#12021)`
  * `249a4a706` `feat: correctly pin the go pkg in module generation (#11826)`
  * `f5d96c785` `feat: directory.chown supports username/groupname (#12128)`
  * `f285b51a0` `feat: generate dependencies in their own files. (#11962)`
  * `db59252ad` `workspace: plumbing & compat (#11995)`
  * `946fe96bb` `feat: go toolchain tag support (#12896)`
  * `7a2a10e53` `fix: ensure image layer blobs are local before ContainerDagOp returns (#12861)`
* Lower-risk churn:
  * dependency bumps
  * docs updates
  * release prep/version bumps
  * CI/workflow small changes

Ordered incoming commits:
1. `db59252ad` `workspace: plumbing & compat (#11995)`
2. `83529b0cb` `bump tuist for new API + tmux/vim alt screen (#12874)`
3. `a626c862a` `test(dagger up): don't use a tty (#12895)`
4. `946fe96bb` `feat: go toolchain tag support (#12896)`
5. `fb5a380b5` `chore: add a test for dockerfile COPY --exclude (#12897)`
6. `7a2a10e53` `fix: ensure image layer blobs are local before ContainerDagOp returns (#12861)`
7. `9db1734ca` `add support to set secret arrays with local defaults (#12898)`

## a189dc9b2 add native Dang runtime
* Keep:
  * `ModuleRuntime` and `ContainerRuntime` as the execution abstraction
  * builtin `dang` parsing/loading in the SDK loader
  * native Dang evaluation directly in-process against the current session
  * `module.runtime` as a nullable field
  * `Module.Runtime` as container-only cached state
  * the current container exec path for container-backed runtimes
  * `ServeHTTPToNestedClient` on the core server interface
  * `Directory.Mount` as the missing exported helper needed by the native runtime
* Remove:
  * `DangEvalOp`
  * BuildKit custom-op registration / solver / LLB cache plumbing for Dang
  * old temporary module/result bookkeeping like `FunctionCall.Module`, `Module.ResultID`, and related scaffolding
  * `sdk/dang/dagger.json`
* Modify:
  * translate runtime loading and eager runtime sync to the new `ModuleRuntime` abstraction, but only persist/store container-backed runtimes
  * keep internal `dagger.json` files on the existing pinned external Dang SDK ref instead of hard-cutting them to `"dang"`
  * keep the native Dang helper logic in local SDK helper files instead of the old custom-op split
* Validation:
  * `go test ./core/sdk -run TestParseSDKName -count=1`
  * `go test ./core -run TestDoesNotExist -count=0`
  * `dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestWorkspace/TestWorkspaceArg'`

## f5d96c785 feat: directory.chown supports username/groupname
* Keep:
  * the feature itself: `Directory` and `File` ownership paths should accept user/group names in addition to numeric IDs
  * mounted-root lookup via `/etc/passwd` and `/etc/group` using the existing `findUID` / `findGID` helpers
  * exported numeric-only `ParseDirectoryOwner` / `ParseFileOwner` as pure parse helpers
* Remove:
  * the old numeric-only schema validation for `directory.chown` / `file.chown`
  * the stale “must be an ID” wording in the `Directory` / `File` owner docs
* Modify:
  * translate the feature into the current `Directory.WithDirectory`, `Directory.WithFile`, `Directory.Chown`, and `File.Chown` flows rather than replaying the old patch mechanically
  * resolve named ownership inside the mounted destination/root context, especially in the newer two-phase `WithFile` path
  * strengthen direct API coverage with explicit `Directory.Chown` and `File.Chown` lookup tests, while disregarding the stale `llbtodagger` `COPY --chown` coverage because it currently fails earlier on an unrelated `loadContainerFromID` recipe-ID issue
* Validation:
  * `dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestDirectory/TestChownLookup'`
  * `dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestFile/TestChownLookup'`
  * `dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestContainer/TestWith(File|Directory)Owner'`

## db59252ad workspace: plumbing & compat
* Summary:
  * Take the upstream workspace/session foundation and current-directory compatibility behavior.
  * Follow upstream's reduced toolchain footprint exactly:
    * keep toolchains where `db59252ad` still uses them
    * delete only our branch-local toolchain registry / projection / shadow-module machinery that upstream no longer has
  * Hard-cut `ModDeps` to `SchemaBuilder`, but preserve the newer substrate from our branch:
    * no per-server/session cache fields
    * keep `coreSchemaForker` / `CoreSchemaBase`
    * keep `Mod` interface extras like `Same`, `ResultCallModule`, and `ModuleResult`
    * keep typedefs as `dagql.ObjectResultArray[*TypeDef]`
  * Keep the post-`a189dc9b2` runtime/storage decisions:
    * `Module.Runtime` stays container-only cached state
    * the runtime abstraction is not stored arbitrarily on `Module`
    * no `Module.ResultID`
    * no BuildKit custom-op substrate

### Cross-file invariant
* The `SchemaBuilder` merge must preserve our wrapped-module model end to end.
* Concretely:
  * `SchemaBuilder` stores `[]core.Mod`, not raw `[]*core.Module`
  * whenever code needs module provenance or install semantics, it must go through `userMod` / `NewUserMod(...)` / `ModuleResult()`
  * typedefs stay `dagql.ObjectResult[*TypeDef]` all the way through
* Watchpoints where this matters most:
  * `core/schema_build.go`: build `ModuleObjectType` and interface extension metadata from `mod.ModuleResult()`
  * `core/object.go`: constructor/install/proxy code must keep using wrapped modules and `NewUserMod(obj.Module).ResultCallModule(ctx)`
  * `core/schema/workspace.go`: `currentWorkspacePrimaryModules()` must unwrap through `ModuleResult().Self()`
  * `engine/server/session_workspaces.go`: resolved modules stay as `dagql.ObjectResult[*core.Module]` until the `serveModule(...)` boundary
  * `core/schema/modulesource.go`: dependency loading and `asModule` return wrapped module results and append them to `SchemaBuilder` as `core.NewUserMod(...)`

### IDable Query and dagql Root
#### Incoming changes
* In upstream `db59252ad`, `Query` becomes stateful:
  * [core/query.go](/home/sipsma/repo/github.com/sipsma/dagger/core/query.go) adds `ConstructorArgs map[string]dagql.Input`
  * the intent is to store constructor arguments for an entrypoint module on the root `Query` object
* In upstream `db59252ad`, the dagql root becomes ID-able again:
  * [dagql/server.go](/home/sipsma/repo/github.com/sipsma/dagger/dagql/server.go) changes `NewServer(...)` so the root class is created with `ClassOpts[T]{}` instead of `NoIDs: true`
  * that means `Query` would become ID-able if we take that part mechanically
* Upstream does not add the `with(...)` field in `core/schema/query.go`.
  * It is installed dynamically in [core/object.go](/home/sipsma/repo/github.com/sipsma/dagger/core/object.go), inside `ModuleObject.installEntrypointMethods(...)`
  * this only happens when a module is installed as an entrypoint
* The entrypoint install path adds three kinds of root-level sugar on `Query`:
  * `with(...)`, when the constructor has arguments
  * proxy methods for each method on the module’s main object
  * proxy fields for each field on the module’s main object
* The `with(...)` field returns a cloned `Query` carrying `ConstructorArgs`.
  * that `Query` is then used as the receiver for chained proxy method/field selections
* The proxy methods/fields are not meant to be the real semantic execution path.
  * they desugar through `dag.Canonical()` into:
    * the real constructor call
    * then the real method/field call on the constructed object
* Upstream adds canonical-server support in [dagql/server.go](/home/sipsma/repo/github.com/sipsma/dagger/dagql/server.go):
  * `canonical *Server`
  * `Canonical()`
  * `SetCanonical(...)`
  * `Load(...)` / `LoadType(...)` delegate to the canonical server when present
* The reason for canonical routing is that the outer server contains Query sugar, while IDs and real constructor paths are supposed to resolve against the unsugared inner server.
* Upstream also marks the entrypoint proxy path as `DoNotCache`:
  * `with(...)`
  * proxy methods
  * proxy fields
* The surrounding feature flow that makes this relevant is:
  * [core/schema/module.go](/home/sipsma/repo/github.com/sipsma/dagger/core/schema/module.go) adds `entrypoint` to `module.serve(...)`
  * [engine/server/session_workspaces.go](/home/sipsma/repo/github.com/sipsma/dagger/engine/server/session_workspaces.go) uses that to install workspace-selected / blueprint / extra modules as entrypoints
* So the incoming upstream shape in this area is:
  * stateful `Query`
  * ID-able root
  * root-level entrypoint sugar on `Query`
  * canonical inner server underneath for the real call path

#### Translation requirements
* We are **not** preserving our current non-IDable root behavior here.
* The merge plan must explicitly do the following:
  * keep the upstream change that makes the dagql root ID-able again
  * remove the current `Query -> nil receiver ref` shortcut in [call_request_input.go](/home/sipsma/repo/github.com/sipsma/dagger/dagql/call_request_input.go#L33)
  * keep the upstream `with(...)` / proxy / canonical-server behavior working on top of an ID-able `Query`
* Concretely, that means:
  * in `dagql/server.go`, do **not** keep `NoIDs: true` on the root class
  * in `dagql/call_request_input.go`, delete the special case:

```go
if typ := res.Type(); typ != nil && typ.Name() == "Query" {
	return nil, nil
}
```

* Once those two changes are made, `Query` is no longer just harmless routing state.
  * chained calls off `Query.with(...)` will carry a real receiver reference
  * that means `ConstructorArgs` are now potentially part of the dependency/call graph story
* This creates a concrete implementation checkpoint during the merge:
  * after making `Query` ID-able and removing the nil-receiver shortcut, verify whether `Query` itself needs `dagql.HasDependencyResults` and/or `dagql.PersistedObject`
  * specifically for `ConstructorArgs` values that may contain IDs/results
  * do not assume the current `DoNotCache` marking is sufficient once `Query` itself becomes a real receiver in the graph

### engine/opts.go
* Take the upstream metadata additions exactly:

```go
type ExtraModule struct {
	Ref        string `json:"ref"`
	Name       string `json:"name,omitempty"`
	Entrypoint bool   `json:"entrypoint,omitempty"`
}

type ClientMetadata struct {
	...
	ExtraModules         []ExtraModule `json:"extra_modules,omitempty"`
	SkipWorkspaceModules bool          `json:"skip_workspace_modules,omitempty"`
	Workspace            *string       `json:"workspace,omitempty"`
}
```

* Keep every existing metadata field from our branch.
* This file remains serialization only.

### engine/client/client.go
* Add only the upstream client params:

```go
type Params struct {
	...
	SkipWorkspaceModules bool
	Workspace            *string
}
```

* Keep `Params.Module` as the CLI's current "load this module as entrypoint" knob.
* In `clientMetadata()`, take the upstream logic literally:

```go
if c.Module != "" {
	md.ExtraModules = []engine.ExtraModule{{Ref: c.Module, Entrypoint: true}}
	md.SkipWorkspaceModules = true
}
if c.SkipWorkspaceModules {
	md.SkipWorkspaceModules = true
}
if c.Workspace != nil {
	md.Workspace = c.Workspace
}
```

* Do not invent a new `Params.ExtraModules`; upstream does not need one here.

### core/query.go
* Update `Query` to carry constructor args for entrypoint proxying:

```go
type Query struct {
	Server

	CurrentEnv *call.ID

	ConstructorArgs map[string]dagql.Input
}
```

* Update `Clone()` to deep-copy `ConstructorArgs`.
* Extend the `Server` interface to the real target shape:

```go
type Server interface {
	ServeHTTPToNestedClient(http.ResponseWriter, *http.Request, *buildkit.ExecutionMetadata)
	ServeModule(ctx context.Context, mod dagql.ObjectResult[*Module], includeDependencies bool, entrypoint bool) error
	CurrentModule(context.Context) (dagql.ObjectResult[*Module], error)
	ModuleParent(context.Context) (dagql.ObjectResult[*Module], error)
	CurrentFunctionCall(context.Context) (*FunctionCall, error)
	CurrentWorkspace(context.Context) (*Workspace, error)
	CurrentServedDeps(context.Context) (*SchemaBuilder, error)
	DefaultDeps(context.Context) (*SchemaBuilder, error)
	...
}
```

* Keep the current `CurrentDagqlServer(ctx)` behavior that prefers a dagql server already attached to the context.
* `NewModule()` should return `&Module{Deps: NewSchemaBuilder(q, nil)}`.
* `ConstructorArgs` is not just metadata anymore once we accept the upstream ID-able root.
  * we are intentionally taking an ID-able `Query`
  * we are intentionally removing the current nil-receiver shortcut for `Query`
  * so this field must be treated as part of the real receiver/call-graph design, not as ignorable routing-only state
* Concretely, after those two changes land, we must audit whether `Query` needs:
  * `dagql.HasDependencyResults`
  * `dagql.PersistedObject`
  * `dagql.PersistedObjectDecoder`
  for `ConstructorArgs` values that can contain IDs/results.

### engine/server/session.go
* Extend `daggerClient` with the upstream workspace/module-loading state:

```go
type daggerClient struct {
	...
	pendingWorkspaceLoad bool
	workspaceMu          sync.Mutex
	workspaceLoaded      bool
	workspaceErr         error
	workspace            *core.Workspace

	pendingModules      []pendingModule
	pendingExtraModules []engine.ExtraModule
	modulesMu           sync.Mutex
	modulesLoaded       bool
	modulesErr          error

	deps        *core.SchemaBuilder
	defaultDeps *core.SchemaBuilder
}
```

* During client initialization:
  * keep current BuildKit / dagql bootstrapping
  * rename `core.NewModDeps(...)` to `core.NewSchemaBuilder(...)`
  * if this client should detect or inherit workspace binding, seed `pendingWorkspaceLoad = true`
  * if `clientMetadata.ExtraModules` is already present, seed `pendingExtraModules`

* Update the serving path so that request handling calls:

```go
if err := srv.ensureWorkspaceLoaded(ctx, client); err != nil { ... }
if err := srv.ensureModulesLoaded(ctx, client); err != nil { ... }
```

* Update `ServeModule` and the internal serving helper to carry install policy:

```go
func (srv *Server) ServeModule(
	ctx context.Context,
	mod dagql.ObjectResult[*core.Module],
	includeDependencies bool,
	entrypoint bool,
) error

func (srv *Server) serveModule(
	client *daggerClient,
	mod core.Mod,
	opts core.InstallOpts,
) error
```

* `serveModule(...)` should stop doing plain append and instead preserve policy:

```go
client.deps = client.deps.With(mod, opts)
```

* Public `ServeModule(...)` behavior:
  * serve the selected module with `Entrypoint: entrypoint`
  * if `includeDependencies` is set, serve direct deps with `SkipConstructor: true`

### engine/server/session_workspaces.go
* Add this file from upstream, but adapt it to our result-wrapper/module-storage model.
* Keep the high-level structure:

```go
func (srv *Server) CurrentWorkspace(ctx context.Context) (*core.Workspace, error)
func (srv *Server) ensureWorkspaceLoaded(ctx context.Context, client *daggerClient) error
func (srv *Server) inheritWorkspaceBinding(ctx context.Context, client *daggerClient) error
func (srv *Server) loadWorkspaceFromHost(ctx context.Context, client *daggerClient) error
func (srv *Server) loadWorkspaceFromHostPath(ctx context.Context, client *daggerClient, hostPath string) error
func (srv *Server) loadWorkspaceFromDeclaredRef(ctx context.Context, client *daggerClient, workspaceRef string) error
func (srv *Server) loadWorkspaceFromRemote(ctx context.Context, client *daggerClient, remoteRef string) error
func (srv *Server) detectAndLoadWorkspaceWithRootfs(...) error
func (srv *Server) ensureModulesLoaded(ctx context.Context, client *daggerClient) error
```

* Keep the upstream `pendingModule` shape, including toolchain compat fields:

```go
type pendingModule struct {
	Ref        string
	RefPin     string
	Name       string
	Entrypoint bool

	LegacyDefaultPath  bool
	ConfigDefaults     map[string]any
	DefaultsFromDotEnv bool
	ArgCustomizations  []*modules.ModuleConfigArgument

	LegacyCallerModuleDir string
	DisableFindUp         bool
}
```

* Keep the upstream legacy gathering flow inside `detectAndLoadWorkspaceWithRootfs(...)`:
  * `workspace.Detect(...)`
  * `workspace.ParseLegacyToolchains(...)`
  * `workspace.ParseLegacyBlueprint(...)`
  * implicit module near CWD
  * `client.pendingExtraModules`

* The concrete pending-module order should stay upstream-compatible:
  1. legacy toolchains
  2. legacy blueprint
  3. implicit root module near CWD
  4. extra modules from client metadata

* `buildCoreWorkspace(...)` needs one translation beyond upstream:
  * in addition to `Path`, `Address`, and `ClientID`, set config metadata by statting `.dagger/config.toml` through the same `statFS`

```go
configRelPath := filepath.Join(detected.Path, ".dagger", "config.toml")
_, hasConfig, err := core.StatFSExists(ctx, statFS, filepath.Join(detected.Root, configRelPath))
coreWS.HasConfig = hasConfig
coreWS.Initialized = hasConfig
if hasConfig {
	coreWS.ConfigPath = filepath.ToSlash(configRelPath)
}
```

* Use result wrappers, not raw `*core.Module`, in the resolve/serve pipeline:

```go
type resolvedServedModule struct {
	mod        dagql.ObjectResult[*core.Module]
	entrypoint bool
}

type resolvedModuleLoad struct {
	primary           dagql.ObjectResult[*core.Module]
	primaryEntrypoint bool
	related           []resolvedServedModule
}
```

* `resolveModuleSourceAsModule(...)` should return `dagql.ObjectResult[*core.Module]`.
* `serveAllResolvedModuleLoads(...)` should wrap user modules with `core.NewUserMod(...)` and pass `core.InstallOpts{Entrypoint: ...}` / `core.InstallOpts{SkipConstructor: true}`.
* Keep upstream related-module serving:
  * serve blueprint modules as `entrypoint: true`
  * serve toolchain modules as `entrypoint: false`

### core/workspace.go
* Replace our minimal `Workspace` struct with the richer upstream shape:
* Target shape:

```go
type Workspace struct {
	rootfs dagql.ObjectResult[*Directory]

	Path        string `field:"true" doc:"Workspace directory path relative to the workspace boundary."`
	Address     string `field:"true" doc:"Canonical Dagger address of the workspace directory."`
	Initialized bool   `field:"true" doc:"Whether .dagger/config.toml exists."`
	ConfigPath  string `field:"true" doc:"Path to config.toml relative to the workspace boundary (empty if not initialized)."`
	HasConfig   bool   `field:"true" doc:"Whether a config.toml file exists in the workspace."`
	ClientID    string `field:"true" doc:"The client ID that owns this workspace's host filesystem."`

	hostPath string
}
```

* Keep helper methods:

```go
func (ws *Workspace) Rootfs() dagql.ObjectResult[*Directory]
func (ws *Workspace) SetRootfs(r dagql.ObjectResult[*Directory])
func (ws *Workspace) HostPath() string
func (ws *Workspace) SetHostPath(p string)
```

* Add `dagql.HasDependencyResults` for `rootfs`:

```go
var _ dagql.HasDependencyResults = (*Workspace)(nil)

func (ws *Workspace) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error)
```

* `Workspace` also needs to implement persisted-object support.
  * Reason: `Workspace` can appear as a function-call input dependency via `loadWorkspaceArg(...)` in [modfunc.go](/home/sipsma/repo/github.com/sipsma/dagger/core/modfunc.go#L1062), so cached call graphs must not fail persistence/import just because a `Workspace` appeared in the dependency graph.
  * This is similar in spirit to host/path-backed inputs: some fields are session-affine, but the object still has to be representable in the persisted graph.

```go
var _ dagql.PersistedObject = (*Workspace)(nil)
var _ dagql.PersistedObjectDecoder = (*Workspace)(nil)

type persistedWorkspace struct {
	Path        string `json:"path,omitempty"`
	Address     string `json:"address,omitempty"`
	Initialized bool   `json:"initialized,omitempty"`
	ConfigPath  string `json:"configPath,omitempty"`
	HasConfig   bool   `json:"hasConfig,omitempty"`
	ClientID    string `json:"clientID,omitempty"`
	HostPath    string `json:"hostPath,omitempty"`

	RootfsResultID uint64 `json:"rootfsResultID,omitempty"`
}

func (ws *Workspace) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error)
func (*Workspace) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error)
```

* Encoding/decoding rules:
  * always persist scalar metadata:
    * `Path`
    * `Address`
    * `Initialized`
    * `ConfigPath`
    * `HasConfig`
    * `ClientID`
    * `HostPath`
  * if `rootfs` is set, persist it with `encodePersistedObjectRef(...)`
  * on decode, restore scalar metadata first, then reload `rootfs` via `loadPersistedObjectResultByResultID[*Directory](...)`

* Semantics to preserve:
  * remote workspace:
    * persisted metadata + `rootfs` should be functionally usable after reload
  * local workspace:
    * persisted metadata + `hostPath` + `ClientID` is enough to keep the dependency graph valid
    * host filesystem operations remain session-affine, which is acceptable; the important thing is that persisted call graphs do not fail to load

### core/workspace/detect.go and core/workspace/legacy.go
* Add these new helper files from upstream as the detection/compat substrate.

#### core/workspace/detect.go
* Add this new package file essentially as upstream:

```go
package workspace

type Workspace struct {
	Root string
	Path string
}

type PathExistsFunc func(ctx context.Context, path string) (parentDir string, exists bool, err error)

func Detect(
	ctx context.Context,
	pathExists PathExistsFunc,
	readFile func(ctx context.Context, path string) ([]byte, error),
	cwd string,
) (*Workspace, error)

func findUpAll(
	ctx context.Context,
	pathExists PathExistsFunc,
	curDirPath string,
	names ...string,
) (map[string]string, error)
```

* Keep the two-step detection behavior:
  * `.git` found => boundary is git root, `Path` is `Rel(gitRoot, cwd)`
  * otherwise => boundary/root is `cwd`, `Path = "."`

#### core/workspace/legacy.go
* Add this new package file essentially as upstream and keep its toolchain support intact:

```go
package workspace

type LegacyToolchain struct {
	Name           string
	Source         string
	Pin            string
	ConfigDefaults map[string]any
	Customizations []*modules.ModuleConfigArgument
}

type LegacyBlueprint struct {
	Name   string
	Source string
	Pin    string
}

func ParseLegacyBlueprint(data []byte) (*LegacyBlueprint, error)
func ParseLegacyToolchains(data []byte) ([]LegacyToolchain, error)
func parseLegacyConfig(data []byte) (*modules.ModuleConfig, error)
func extractConfigDefaults(customizations []*modules.ModuleConfigArgument) map[string]any
func cloneCustomizations(customizations []*modules.ModuleConfigArgument) []*modules.ModuleConfigArgument
```

* This file is where we follow upstream on toolchains rather than inventing a new policy.

### core/schema/workspace.go
* Replace the current experimental workspace schema with the upstream richer behavior, but adapt it to **current** dagql APIs.
* Install shape should be:

```go
dagql.Fields[*core.Query]{
	dagql.Func("currentWorkspace", s.currentWorkspace).
		WithInput(dagql.PerCallInput).
		Doc("Detect and return the current workspace."),
}.Install(srv)

dagql.Fields[*core.Workspace]{
	dagql.NodeFunc("directory", s.directory).
		WithInput(dagql.PerClientInput).
		Doc(...),
	dagql.NodeFunc("file", s.file).
		WithInput(dagql.PerClientInput).
		Doc(...),
	dagql.NodeFunc("findUp", s.findUp).
		WithInput(dagql.PerClientInput).
		Doc(...),
	dagql.Func("checks", s.checks).
		Doc(...),
	dagql.Func("generators", s.generators).
		Doc(...),
}.Install(srv)
```

* `currentWorkspace` should become:

```go
func (s *workspaceSchema) currentWorkspace(
	ctx context.Context,
	parent *core.Query,
	_ struct{},
) (*core.Workspace, error) {
	return parent.Server.CurrentWorkspace(ctx)
}
```

* Keep `withWorkspaceClientContext(...)` from the current branch; it is still the right bridge for local workspaces passed into module functions.

* Add the upstream helper flow in our current resolver/input style:

```go
func (s *workspaceSchema) resolveRootfs(
	ctx context.Context,
	ws *core.Workspace,
	resolvedPath string,
	filter core.CopyFilter,
	gitignore bool,
) (dagql.ObjectResult[*core.Directory], error)

func resolveWorkspacePath(pathArg, workspacePath string) string
func currentWorkspacePrimaryModules(ctx context.Context) ([]*core.Module, error)
func toolchainIgnorePatterns(
	mods []*core.Module,
	getPatterns func(*modules.ModuleConfigDependency) []string,
) map[string][]string
```

* `resolveRootfs(...)` behavior:
  * local workspace:
    * use `withWorkspaceClientContext`
    * resolve against `ws.HostPath()`
    * select `host.directory(...)`
  * remote workspace:
    * start from `ws.Rootfs()`
    * `directory(path: ...)` into subdirs
    * apply include/exclude filtering by re-wrapping through `directory().withDirectory(...)`

* Keep the upstream absolute-vs-relative input contract:
  * relative args resolve from `ws.Path`
  * absolute args resolve from the workspace boundary
* But preserve the current branch's `findUp` return contract:
  * return a workspace-boundary-relative path like `a/b/file.txt`
  * do **not** switch to the upstream leading-`/` output shape
  * this matches the current public tests in `core/integration/workspace_test.go`

* Keep the upstream check/generator helpers and toolchain-aware check filtering:
  * `currentWorkspacePrimaryModules`
  * `toolchainIgnorePatterns`
  * `filterNodesByExclude`
  * `reparentWorkspaceTreeRoot`
  * `matchWorkspaceInclude`
  * `matchSingleModuleInclude`
* The one adaptation needed is in `currentWorkspacePrimaryModules`:
  * our `SchemaBuilder.PrimaryMods()` returns `[]core.Mod` wrappers, not raw `*core.Module`
  * use `mod.ModuleResult().Self()` rather than casting directly to `*core.Module`

### dagql/server.go
* Add only the canonical-server support from upstream:

```go
type Server struct {
	...
	canonical *Server
}

func (s *Server) Canonical() *Server {
	if s.canonical != nil {
		return s.canonical
	}
	return s
}

func (s *Server) SetCanonical(canonical *Server) {
	s.canonical = canonical
}
```

* Keep our current constructor/cache model:
  * `dagql.NewServer(root)`
  * no per-server cache state
  * no cache attachment helper layer

### core/moddeps.go -> core/schema_builder.go
* Rename the file and type as a hard cut:
  * `core/moddeps.go` -> `core/schema_builder.go`
  * `type ModDeps` -> `type SchemaBuilder`
* Target shape:

```go
type schemaEntry struct {
	mod  Mod
	opts InstallOpts
}

type SchemaBuilder struct {
	root *Query

	entries []schemaEntry

	lazilyLoadedServer *dagql.Server
	loadSchemaErr      error
	loadSchemaLock     sync.Mutex
}

func NewSchemaBuilder(root *Query, mods []Mod) *SchemaBuilder
func (b *SchemaBuilder) Clone() *SchemaBuilder
func (b *SchemaBuilder) WithRoot(root *Query) *SchemaBuilder
func (b *SchemaBuilder) Append(mods ...Mod) *SchemaBuilder
func (b *SchemaBuilder) With(mod Mod, opts InstallOpts) *SchemaBuilder
func (b *SchemaBuilder) Lookup(name string) (Mod, bool)
func (b *SchemaBuilder) Mods() []Mod
func (b *SchemaBuilder) PrimaryMods() []Mod
func (b *SchemaBuilder) Server(ctx context.Context) (*dagql.Server, error)
func (b *SchemaBuilder) SchemaIntrospectionJSONFile(ctx context.Context, hiddenTypes []string) (dagql.Result[*File], error)
func (b *SchemaBuilder) SchemaIntrospectionJSONFileForModule(ctx context.Context) (dagql.Result[*File], error)
func (b *SchemaBuilder) SchemaIntrospectionJSONFileForClient(ctx context.Context) (dagql.Result[*File], error)
func (b *SchemaBuilder) TypeDefs(ctx context.Context, dag *dagql.Server) (dagql.ObjectResultArray[*TypeDef], error)
func (b *SchemaBuilder) ModTypeFor(ctx context.Context, typeDef *TypeDef) (ModType, bool, error)
```

* Preserve the current branch's substantive behavior:
  * `WithRoot(root *Query)`
  * `TypeDefs(...)` returning `dagql.ObjectResultArray[*TypeDef]`
  * `coreSchemaForker` support
  * current interface-extension wiring

* Take from upstream:
  * per-entry `InstallOpts`
  * `PrimaryMods()`
  * `With(mod, opts)` with promotion rules for `SkipConstructor` / `Entrypoint`
  * inner/outer server split when any entry has `Entrypoint`

* `Server(ctx)` should build through our current dagql substrate:
  * `coreSchemaForker.ForkSchema(ctx, root, view)` when available
  * otherwise `dagql.NewServer(root)` plus current introspection install
  * no per-server cache attachment path

### core/schema_build.go
* Add this file as the shared schema-construction helper.
* Target shape:

```go
type schemaInstall struct {
	mod  Mod
	opts InstallOpts
}

func buildSchema(
	ctx context.Context,
	root *Query,
	installs []schemaInstall,
) (*dagql.Server, error)

func schemaJSONFileFromServer(
	ctx context.Context,
	dag *dagql.Server,
	hiddenTypes []string,
) (dagql.Result[*File], error)
```

* `buildSchema(...)` must be adapted to our current world, not copied verbatim from upstream:
  * resolve the view exactly once from installed modules
  * use `coreSchemaForker` when present
  * install each module with its `InstallOpts`
  * collect object/interface typedefs through the current `dagql.ObjectResultArray[*TypeDef]` API
  * build `ModuleObjectType{mod: mod.ModuleResult()}` and `InterfaceType{mod: mod.ModuleResult()}` so interface extension remains result-wrapper-aware

### core/schema_codegen.go
* Add only the pure introspection conversion helpers:

```go
func DagqlToCodegenType(...)
func DagqlToCodegenDirective(...)
func DagqlToCodegenDirectiveArg(...)
func DagqlToCodegenDirectiveDef(...)
func DagqlToCodegenField(...)
func DagqlToCodegenInputValue(...)
func DagqlToCodegenEnumValue(...)
func DagqlToCodegenTypeRef(...)
```

* This file only converts `dagql/introspection` types into `cmd/codegen/introspection` types.
* It does **not** touch our `core.TypeDef` graph and therefore does **not** regress the ObjectResult-based typedef work.

### core/schema/query.go
* Keep the current `querySchema` structure:

```go
type querySchema struct{}
```

* Keep the current `__schemaJSONFile` registration style:

```go
dagql.NodeFunc("__schemaJSONFile", s.schemaJSONFile).
	IsPersistable().
	WithInput(dagql.CurrentSchemaInput)
```

* Keep the current `schemaJSONArgs`:

```go
type schemaJSONArgs struct {
	HiddenTypes []string `default:"[]"`
}
```

* Replace the local `dagqlToCodegen*` helpers with calls to the new exported `core.DagqlToCodegen*` helpers.
* Keep the current persistable `__schemaJSONFile` implementation shape; do not rewrite it into a different execution path as part of this merge.

### core/schema/coremod.go
* Keep the current `CoreSchemaBase` / `coreSchemaViewState` architecture.
* Keep the current retained `dagql.ObjectResultArray[*core.TypeDef]` caches and per-view maps.
* Update only what is required for the new substrate:

```go
func (m *CoreMod) Install(ctx context.Context, dag *dagql.Server, opts ...core.InstallOpts) error
```

* Do **not** adopt upstream's raw `CoreMod{Dag}` rewrite.
* Do **not** add `core/typedef_from_schema.go` in this merge.

### core/module.go
* Keep our current module storage model, but rename deps and add the upstream workspace-compat fields.
* Target `Module` shape:

```go
type Module struct {
	Source        dagql.Nullable[dagql.ObjectResult[*ModuleSource]]
	ContextSource dagql.Nullable[dagql.ObjectResult[*ModuleSource]]

	NameField    string
	OriginalName string
	SDKConfig    *SDKConfig

	Deps *SchemaBuilder

	Runtime dagql.Nullable[dagql.ObjectResult[*Container]]

	Description   string
	ObjectDefs    dagql.ObjectResultArray[*TypeDef]
	InterfaceDefs dagql.ObjectResultArray[*TypeDef]
	EnumDefs      dagql.ObjectResultArray[*TypeDef]

	LegacyDefaultPath  bool
	WorkspaceConfig    map[string]any
	DefaultsFromDotEnv bool

	persistedResultID uint64
	IncludeSelfInDeps bool

	DisableDefaultFunctionCaching bool
}
```

* Remove the branch-local toolchain-only fields:
  * `IsToolchain`
  * `Toolchains *ToolchainRegistry`
  * `isToolchainModule(...)`

* Add upstream install policy near the `Mod` interface:

```go
type InstallOpts struct {
	SkipConstructor bool
	Entrypoint      bool
}

type Mod interface {
	Name() string
	Same(Mod) (bool, error)
	View() (call.View, bool)
	Install(ctx context.Context, dag *dagql.Server, opts ...InstallOpts) error
	ModTypeFor(ctx context.Context, typeDef *TypeDef, checkDirectDeps bool) (ModType, bool, error)
	TypeDefs(ctx context.Context, dag *dagql.Server) (dagql.ObjectResultArray[*TypeDef], error)
	GetSource() *ModuleSource
	ResultCallModule(context.Context) (*dagql.ResultCallModule, error)
	ModuleResult() dagql.ObjectResult[*Module]
}
```

* Keep the current branch's interface extras. Do not regress to upstream's smaller `Mod` interface.
* Update the persisted payload to match the new real state:

```go
type persistedModulePayload struct {
	SourceResultID        uint64   `json:"sourceResultID,omitempty"`
	ContextSourceResultID uint64   `json:"contextSourceResultID,omitempty"`
	RuntimeResultID       uint64   `json:"runtimeResultID,omitempty"`
	DepModuleResultIDs    []uint64 `json:"depModuleResultIDs,omitempty"`
	IncludeSelfInDeps     bool     `json:"includeSelfInDeps,omitempty"`

	NameField    string
	OriginalName string
	SDKConfig    *SDKConfig
	Description  string

	ObjectDefResultIDs    []uint64
	InterfaceDefResultIDs []uint64
	EnumDefResultIDs      []uint64

	LegacyDefaultPath  bool
	WorkspaceConfig    map[string]any
	DefaultsFromDotEnv bool

	DisableDefaultFunctionCaching bool
}
```

* Keep current source/context/runtime/typedef attachment and decode logic.
* Do **not** add upstream `Module.Runtime ModuleRuntime` or `Module.ResultID`.
* Do **not** import upstream `ContentDigestCacheKey` / `GetModuleFromContentDigest` / `CacheModuleByContentDigest`; keep the current implementation-scoped module flow instead.

### core/object.go
* Remove the branch-local toolchain registry path:
  * delete the `ToolchainRegistry` proxy branch from `functions(ctx, dag)`
* Update `ModuleObject.Install(...)` to accept `InstallOpts`:

```go
func (obj *ModuleObject) Install(ctx context.Context, dag *dagql.Server, opts ...InstallOpts) error
```

* Keep the current wrapper/provenance substrate:
  * `obj.Module` remains `dagql.ObjectResult[*Module]`
  * constructor/module provenance still comes from `NewUserMod(obj.Module).ResultCallModule(ctx)`

* Add the upstream entrypoint proxy behavior, adapted to the current result-wrapper world:
  * `installConstructor(...)` skips installation when `SkipConstructor`
  * `installEntrypointMethods(...)` installs:
    * `Query.with(...)`
    * method proxies
    * field proxies
  * proxies call through `dag.Canonical()` and read constructor args from `Query.ConstructorArgs`

```go
func (obj *ModuleObject) installEntrypointMethods(ctx context.Context, dag *dagql.Server) error
```

### core/modulesource.go
* Keep the current persisted/lazy-SDK machinery.
* Remove only the branch-local toolchain projection fields and their persistence:

```go
// delete from ModuleSource:
ToolchainContextSource dagql.Nullable[dagql.ObjectResult[*ModuleSource]]
ToolchainConfigIndex   int
ToolchainProjection    bool
```

* Keep:
  * `ConfigToolchains`
  * `Toolchains`
  * `ConfigBlueprint`
  * `Blueprint`
  * `ContextDirectory`
  * `UserDefaults`
  * all current persisted-object encoding/decoding for dependencies, blueprint, and toolchain result IDs

* Keep the current digest story:
  * `SourceImplementationDigest(ctx)`
  * `moduleSourceDigest(...)`
  * `ImplementationScopedModuleSource(...)`
* Do **not** import upstream's stored `Digest string` field or old `CalcDigest`/`ContentCacheScope` helpers.

* Rename all SDK/deps signatures from `*ModDeps` to `*SchemaBuilder`.

### core/schema/modulesource.go
* This is the biggest manual reconciliation hotspot.
* Keep the public authoring surface that already aligns with upstream:
  * `withBlueprint`
  * `withToolchains`
  * `withUpdateToolchains`
  * `withoutToolchains`
  * `withUpdateBlueprint`
  * `withoutBlueprint`

* Delete the branch-local toolchain projection/integration machinery:
  * `_asToolchain`
  * `toolchainContext`
  * `extractToolchainModules`
  * `addToolchainFieldsToObject`
  * `mergeToolchainsWithSDK`
  * `createShadowModuleForToolchains`
  * `integrateToolchains`

* Rewrite `moduleSourceAsModule(...)` to the upstream compat shape while keeping our current module storage model:

```go
func (s *moduleSourceSchema) moduleSourceAsModule(
	ctx context.Context,
	src dagql.ObjectResult[*core.ModuleSource],
	args struct {
		ForceDefaultFunctionCaching bool   `internal:"true" default:"false"`
		LegacyDefaultPath           bool   `internal:"true" default:"false"`
		LegacyArgCustomizationsJSON string `internal:"true" default:""`
		LegacyNameOverride          string `internal:"true" default:""`
		LegacyWorkspaceConfigJSON   string `internal:"true" default:""`
		LegacyDefaultsFromDotEnv    bool   `internal:"true" default:"false"`
	},
) (dagql.ObjectResult[*core.Module], error)
```

* Concrete body plan:
  * keep the current engine-version compatibility checks
  * keep `ForceDefaultFunctionCaching`
  * keep `ContextSource`
  * if `src.Blueprint` is set:
    * use the blueprint as the execution source
    * keep the original `src` as `ContextSource`
    * copy original `Toolchains` onto the blueprint source before dependency loading, matching upstream behavior
  * build the base module inline:

```go
mod := &core.Module{
	Source:                        dagql.NonNull(execSrc),
	ContextSource:                 dagql.NonNull(contextSrc),
	NameField:                     originalSrc.Self().ModuleName,
	OriginalName:                  execSrc.Self().ModuleOriginalName,
	SDKConfig:                     execSrc.Self().SDK,
	DisableDefaultFunctionCaching: execSrc.Self().DisableDefaultFunctionCaching,
}
mod.Deps, err = s.loadDependencyModules(ctx, execSrc)
```

  * apply compat settings before module install:
    * `LegacyNameOverride`
    * `LegacyDefaultPath`
    * `LegacyWorkspaceConfigJSON`
    * `LegacyDefaultsFromDotEnv`
  * initialize via existing helpers:
    * SDK present => existing `runModuleDefInSDK(...)`
    * no SDK => existing `createStubModule(...)`
  * after typedefs exist, apply:
    * `mod.ApplyWorkspaceDefaultsToTypeDefs()`
    * `mod.ApplyLegacyCustomizationsToTypeDefs(customizations)`

* `loadDependencyModules(...)` should return `*core.SchemaBuilder` and load both dependencies and toolchains via plain `asModule`:

```go
func (s *moduleSourceSchema) loadDependencyModules(
	ctx context.Context,
	src dagql.ObjectResult[*core.ModuleSource],
) (*core.SchemaBuilder, error)
```

* Concrete behavior:
  * load `src.Dependencies` with `asModule`
  * load `src.Toolchains` with `asModule`
  * do **not** call `_asToolchain`
  * start from `query.DefaultDeps(ctx)`
  * replace the core view for `src.EngineVersion`
  * append dependency and toolchain modules as `core.NewUserMod(...)`

* Keep the current implementation-scoped helpers:
  * `_implementationScoped`
  * `ImplementationScopedModuleSource(...)`
  * `moduleSourceDigest(...)`
* Do **not** switch these sites to upstream's `GetModuleFromContentDigest` helpers.

### core/schema/module.go
* Keep our current richer module schema surface and current `module.runtime` nullable behavior.
* Take only the workspace-entrypoint-serving change from upstream:

```go
func (s *moduleSchema) moduleServe(ctx context.Context, mod dagql.ObjectResult[*core.Module], args struct {
	IncludeDependencies dagql.Optional[dagql.Boolean]
	Entrypoint          dagql.Optional[dagql.Boolean]
}) (dagql.Nullable[core.Void], error)
```

* Pass that through to the new `Server.ServeModule(..., entrypoint bool)` signature.
* Keep the current `currentTypeDefs(returnAllTypes)` implementation and its ObjectResult-based closure expansion.
* Do **not** replace it with upstream's raw `hideCore` variant.

### core/sdk.go and core/sdk/*
* Hard-cut all SDK interfaces and implementations from `*ModDeps` to `*SchemaBuilder`.
* Keep the current runtime model unchanged:
  * `ModuleRuntime`
  * `ContainerRuntime`
  * native Dang runtime
* Update:
  * `core/sdk.go`
  * `core/sdk/dang_sdk.go`
  * `core/sdk/go_sdk.go`
  * `core/sdk/module.go`
  * `core/sdk/module_runtime.go`
  * `core/sdk/module_typedefs.go`
  * `core/sdk/module_code_generator.go`
  * `core/sdk/module_client_generator.go`

### core/modfunc.go
* Mostly keep the current branch behavior.
* Rename deps/runtime loading to `*SchemaBuilder`.
* Keep the current workspace argument injection path:
  * `loadWorkspaceArg(...)`
  * current mounted `/.daggermod` output read
* Keep the current constructor/default precedence behavior:
  * `WorkspaceConfig`
  * `DefaultsFromDotEnv`
  * `LegacyDefaultPath`
* Do not reintroduce any `Module.ResultID` or selector-based output-file loading.

### core/modules/config.go
* Follow upstream exactly here; this file is already aligned.
* Keep the reduced-but-still-present toolchain config model:

```go
type ModuleConfig struct {
	Blueprint  *ModuleConfigDependency   `json:"blueprint,omitempty"`
	Toolchains []*ModuleConfigDependency `json:"toolchains,omitempty"`
	...
}

type ModuleConfigDependency struct {
	Name             string
	Source           string
	Pin              string
	Customizations   []*ModuleConfigArgument `json:"customizations,omitempty"`
	IgnoreChecks     []string                `json:"ignoreChecks,omitempty"`
	IgnoreGenerators []string                `json:"ignoreGenerators,omitempty"`
}
```

### cmd/dagger/*
* Reconcile the CLI after the engine/core foundation is settled.
* The upstream files to translate are:
  * `cmd/dagger/module.go`
  * `cmd/dagger/module_inspect.go`
  * `cmd/dagger/call.go`
  * `cmd/dagger/functions.go`
  * `cmd/dagger/checks.go`
  * `cmd/dagger/generators.go`
  * `cmd/dagger/mcp.go`
  * `cmd/dagger/session.go`
  * `cmd/dagger/shell.go`
  * `cmd/dagger/shell_commands.go`
  * `cmd/dagger/shell_completion.go`
  * `cmd/dagger/shell_exec.go`
  * `cmd/dagger/shell_fs.go`
  * `cmd/dagger/shell_help.go`
* Keep upstream workspace/session behavior.
* Keep the reduced upstream toolchain behavior where it still exists.
* Delete only references to our branch-local toolchain registry / shadow-module / projection model.

### tests / validation
* Keep the upstream new workspace/session/legacy coverage:
  * `core/integration/workspace_test.go`
  * `core/schema/workspace_test.go`
  * `engine/server/session_test.go`
  * `core/workspace/detect_test.go`
  * `core/workspace/legacy_test.go`
  * `core/module_legacy_test.go`
  * `core/host_test.go`
* Keep toolchain coverage that still matches upstream's reduced model.
* Only rewrite/delete tests that specifically assert the branch-local toolchain registry / shadow-module behavior.
* Generated SDK/docs/static artifacts are last.

### Validation
* First-pass focused validation after the reconciliation:

```bash
go test ./core/workspace -run 'TestDetect|TestParseLegacy'
```

```bash
go test ./engine/server -run 'Test.*Workspace'
```

```bash
dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestWorkspace'
```

```bash
dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestLegacy|TestModule'
```

* And one toolchain-oriented smoke test after the toolchain translation is settled, using whatever current integration coverage still matches upstream's reduced toolchain behavior.

* The point of these is:
  * `TestWorkspace` validates the workspace/session foundation directly
  * `TestModule` catches the compat/runtime fallout
  * `TestLegacy` catches the legacy `dagger.json` adapter behavior after the workspace/toolchain compat translation
