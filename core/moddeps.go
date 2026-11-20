// DESIGN DOC: hack/designs/workspace-main-proxy.md

package core

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
)

const ModuleName = "daggercore"

var TypesToIgnoreForModuleIntrospection = []string{"Host"}

var FieldsToIgnoreForModuleIntrospection = []string{
	"Query.currentWorkspace",
	"Query.engineVolume",
	"Query.sshfsVolume",
	// Only a session's main client may name it.
	"Query.setSessionTitle",
	"Address.volume",
}

type coreSchemaForker interface {
	ForkSchema(context.Context, *Query, call.View) (*dagql.Server, error)
}

type modDepEntry struct {
	mod  Mod
	opts InstallOpts
}

// SchemaBuilder lazily constructs a dagql server from a set of modules with
// per-module install policy. It is used both for a module's own dependency
// graph and for the set of modules served to a client session.
//
// A built server is kept only by a client-owned builder: one that lives no
// longer than the client or session that built it, such as a
// SchemaBuilderMemo entry or the builder a client runtime serves from. Any
// other builder, notably a Module's Deps, sits on cached results that outlive
// the session, so its schema is taken from the current session's memo
// instead and lives and dies with that session.
type SchemaBuilder struct {
	// root is the query value installed into derived schema servers. Query only
	// carries the engine Server facade; runtime selection and authority come
	// from the held ClientScope in each execution context, never from a pointer
	// captured by the builder.
	root    *Query
	entries []modDepEntry

	// clientOwned marks a builder that may keep the server it builds. It is
	// never copied: every derived builder starts unowned.
	clientOwned bool
	// shared marks the builder a session memo keeps for every client of the
	// session (SchemaBuilderMemo.sharedBuilder): its root is the memo's
	// neutral root, and it builds its server itself. It is never copied.
	shared bool

	lazilyLoadedServer *dagql.Server
	loadSchemaErr      error
	loadSchemaFailed   atomic.Bool
	loadSchemaLock     sync.Mutex
}

func NewSchemaBuilder(root *Query, mods []Mod) *SchemaBuilder {
	entries := make([]modDepEntry, len(mods))
	for i, m := range mods {
		entries[i] = modDepEntry{mod: m}
	}
	return &SchemaBuilder{
		root:    root,
		entries: entries,
	}
}

func (b *SchemaBuilder) Clone() *SchemaBuilder {
	if b == nil {
		return nil
	}
	return &SchemaBuilder{
		root:    b.root,
		entries: slices.Clone(b.entries),
	}
}

// ClientOwned returns a copy of b that keeps the server it builds. Only a
// client's runtime may hold it, and never on a value that can outlive the
// client.
func (b *SchemaBuilder) ClientOwned() *SchemaBuilder {
	cp := b.Clone()
	cp.clientOwned = true
	return cp
}

func (b *SchemaBuilder) WithRoot(root *Query) *SchemaBuilder {
	cp := b.Clone()
	cp.root = root
	return cp
}

func (b *SchemaBuilder) WithCoreMod(mod Mod) *SchemaBuilder {
	cp := b.Clone()
	for i, entry := range cp.entries {
		if entry.mod.Name() == ModuleName {
			cp.entries[i].mod = mod
		}
	}
	return cp
}

func (b *SchemaBuilder) Prepend(mods ...Mod) *SchemaBuilder {
	extra := make([]modDepEntry, len(mods))
	for i, m := range mods {
		extra[i] = modDepEntry{mod: m}
	}
	return &SchemaBuilder{
		root:    b.root,
		entries: append(extra, b.entries...),
	}
}

func (b *SchemaBuilder) Append(mods ...Mod) *SchemaBuilder {
	extra := make([]modDepEntry, len(mods))
	for i, m := range mods {
		extra[i] = modDepEntry{mod: m}
	}
	return &SchemaBuilder{
		root:    b.root,
		entries: append(slices.Clone(b.entries), extra...),
	}
}

// SchemaBuilderMemo shares one builder, and so one built schema server, per
// module set. Loading a handle resolves the module set its value references
// (Query.ModDepsForCall) on every load; without a memo, each load would build
// a server and reinstall every module. ModDepsForCall also backs the server's
// resultServerForCall hook, so the memo serves cold recipe loads with module
// provenance, ObjectTypeAndServerForID, and persisted-envelope decoding too.
//
// The engine keeps one memo per client and drops it with the client's other
// heavy state. It also keeps one memo per session for the schemas of builders
// that are not client-owned, such as a Module's Deps (see
// SchemaBuilder.Schema). The session's memo is shared by its clients,
// including the nested client of every function call, and dropped when the
// session ends.
//
// A session can live for hours (an interactive agent's does), and its module
// sets keep changing: every reload of a module is a new Module result, and so
// a new module set. So every table of a memo is a bounded LRU: an entry costs
// up to a forked core schema plus its modules' types (hundreds of KiB), and
// one unused for a while is rebuilt on its next use for a few ms (a core fork
// and module installs). A server, builder or entry evicted while a caller
// holds it stays valid for that caller: eviction only stops sharing it, and
// the garbage collector frees it once nothing holds it. Schema servers are
// already built to be dropped (module resolvers and classes hold their server
// weakly), and the bounds sit well above the working set of the workloads the
// memo was made for (see maxMemoizedSchemaBuilders). A rebuild after an
// eviction counts as RepeatedOnSameRoot in SchemaBuilds.
//
// A built server is an immutable type registry; the dagql result cache is
// engine-wide and never held by a server, so sharing one shares no results.
// A session's memo also shares one server per module set across all of its
// clients, built on a neutral root (see sharedBuilder): each client's own
// builders, and the entries of its client memo, serve that server.
type SchemaBuilderMemo struct {
	mu       sync.Mutex
	builders *lru.Cache[string, *SchemaBuilder]

	// canonicalArgTypes reuses immutable argument metadata across the schemas
	// of nested clients. It never shares their query roots or installed fields.
	canonicalArgTypes *lru.Cache[string, *canonicalArgType]
	modTypes          *lru.Cache[modTypeMemoKey, *modTypeMemoEntry]
	gitClients        *lru.Cache[declaredGitClientsKey, *declaredGitClientsEntry]
	// moduleServers holds the client's standalone server for each module
	// whose artifacts it discovers, evaluates or checks
	// (dagqlServerForModule).
	moduleServers *lru.Cache[string, *moduleServerEntry]
	// builtModuleSets holds what this session built a schema server for, by
	// module set, and builtServers how many it holds (countSchemaBuild). It
	// keeps keys only, a few hundred bytes each, and stops recording at
	// maxTrackedModuleSets.
	builtModuleSets map[string]*sessionBuilds
	builtServers    int
	// workspaceModules holds the modules installed in a value workspace, by
	// its recipe digest (see MCP.loadBase).
	workspaceModules *lru.Cache[string, *InstalledWorkspaceModules]
	// neutralRoots holds the root the memo's shared builders build on, one
	// per engine Server facade (see sharedBuilder).
	neutralRoots map[Server]*Query
}

const (
	// maxMemoizedSchemaBuilders bounds a memo's builders, each with the
	// schema server it built, and maxMemoizedModuleServers its standalone
	// module servers. A memo serves a few distinct module sets at a time: the
	// handles a client loads (an agent's conversation references the modules
	// it was composed with), the dependency schemas of the modules a session
	// loads (those that depend on core alone share one), one server per
	// module a listing visits.
	maxMemoizedSchemaBuilders = 256
	maxMemoizedModuleServers  = 256
	// maxMemoizedTypes bounds the module types and argument types resolved
	// for module schemas: a few per function of each module installed. Each
	// is small, but keeps its Module result alive.
	maxMemoizedTypes = 8192
	// maxMemoizedGitClients bounds the git clients read per module tree: a
	// short list each.
	maxMemoizedGitClients = 1024
	// maxMemoizedWorkspaces bounds the modules loaded per value workspace:
	// each keeps every Module result the workspace installs, and an agent's
	// workspace is a new value at every edit. Every agent of a session asks
	// for its own workspace's modules at each step, so the bound sits well
	// above the agents a session runs at once (a staff of dozens): fewer
	// would reload, and rebuild the failing modules of, every agent in turn.
	maxMemoizedWorkspaces = 256
)

func NewSchemaBuilderMemo() *SchemaBuilderMemo {
	return &SchemaBuilderMemo{
		builders:          newMemoTable[string, *SchemaBuilder](maxMemoizedSchemaBuilders),
		canonicalArgTypes: newMemoTable[string, *canonicalArgType](maxMemoizedTypes),
		modTypes:          newMemoTable[modTypeMemoKey, *modTypeMemoEntry](maxMemoizedTypes),
		gitClients:        newMemoTable[declaredGitClientsKey, *declaredGitClientsEntry](maxMemoizedGitClients),
		moduleServers:     newMemoTable[string, *moduleServerEntry](maxMemoizedModuleServers),
		workspaceModules:  newMemoTable[string, *InstalledWorkspaceModules](maxMemoizedWorkspaces),
	}
}

func newMemoTable[K comparable, V any](size int) *lru.Cache[K, V] {
	table, err := lru.New[K, V](size)
	if err != nil {
		panic(err) // only for a size that is not positive
	}
	return table
}

// memoEntry returns table's entry for key, adding an empty one if there is
// none, for the caller to fill under the entry's own lock. An entry evicted
// while a caller fills it is still filled for that caller.
func memoEntry[K comparable, E any](m *SchemaBuilderMemo, table *lru.Cache[K, *E], key K) *E {
	m.mu.Lock()
	defer m.mu.Unlock()
	if entry, ok := table.Get(key); ok {
		return entry
	}
	entry := new(E)
	table.Add(key, entry)
	return entry
}

// WorkspaceModules returns the modules installed in the workspace key names,
// loading them with load the first time. A failed load is not kept. Two
// callers may load the same key at once; the last load is kept. A nil memo
// loads every time.
func (m *SchemaBuilderMemo) WorkspaceModules(key string, load func() (*InstalledWorkspaceModules, error)) (*InstalledWorkspaceModules, error) {
	if m == nil {
		return load()
	}
	if cached, ok := m.workspaceModules.Get(key); ok {
		return cached, nil
	}
	// Loading builds schemas through this memo, so it runs unlocked.
	loaded, err := load()
	if err != nil {
		return nil, err
	}
	m.workspaceModules.Add(key, loaded)
	return loaded, nil
}

// Get returns the client-owned builder memoized for exactly b's root and
// entries, memoizing a client-owned copy of b if there is none. b itself is
// never memoized, since the caller may keep it on a value that outlives the
// client. A nil memo, or a module set that cannot be keyed, returns b.
//
// A builder whose schema failed to load is replaced rather than returned, so
// a failed build is retried by the next load instead of sticking.
func (m *SchemaBuilderMemo) Get(b *SchemaBuilder) *SchemaBuilder {
	if m == nil || b == nil {
		return b
	}
	key, ok := b.memoKey()
	if !ok {
		return b
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if cached, ok := m.builders.Get(key); ok && !cached.loadSchemaFailed.Load() {
		return cached
	}
	owned := b.ClientOwned()
	m.builders.Add(key, owned)
	return owned
}

// memoKey identifies b's root and entries: user modules by attached result,
// core by its schema base and view (SchemaASTCacheKey), anything else by
// instance. Entry order and install options are part of the key, since both
// shape the built schema.
//
// Core is keyed by content because every module load wraps it in a CoreMod
// of its own (CoreMod.WithView): keyed by instance, each reload of a module
// built another server for its unchanged dependency schema. A CoreMod is its
// base and view and nothing else, so two with the same key build the same
// schema.
func (b *SchemaBuilder) memoKey() (string, bool) {
	entries, ok := schemaEntriesKey(b.entries, true)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("r%p;%s", b.root, entries), true
}

// sharedBuilder returns the builder this memo shares among all of its
// clients for b's module set, whatever b's root, or nil when b's schema may
// depend on the client that builds it. A session memo hands it out, so one
// server serves the set to every client of the session: each function call's
// nested client, each agent tool call, each handle load.
//
// Why a server built for one client can serve another: a built server holds
// its root only as the receiver of Query fields, and a client's root is
// core.NewRoot of the engine's one Server facade, whose methods all find the
// client from the context. A root's own state is the client's cache volumes
// and approved host histories, read through CurrentQuery(ctx), which is
// always the calling client's root, never a server's; and ConstructorArgs,
// which no client root has: `with` sets them on a clone that queries chain
// from. So the shared builder builds on a neutral root of the same facade,
// as the core schema's views (CoreSchemaBase.viewState) already do. That
// root is marked shared, so reading a client's state from it fails
// (ErrSharedRootClientState) instead of reading none.
//
// What a client may still put in a schema is what its context gives the
// install: only expanding a module's .env user defaults, against the host of
// the building client, does that. A set with such defaults, or with a
// module the memo can't vouch for, is never shared (clientIndependentSchema).
func (m *SchemaBuilderMemo) sharedBuilder(b *SchemaBuilder) *SchemaBuilder {
	if m == nil || b == nil || b.root == nil || !clientIndependentSchema(b.entries) {
		return nil
	}
	facade := b.root.Server
	if facade == nil || reflect.ValueOf(facade).Kind() != reflect.Pointer {
		return nil
	}
	entries, ok := schemaEntriesKey(b.entries, true)
	if !ok {
		return nil
	}
	key := fmt.Sprintf("s%p;%s", facade, entries)
	m.mu.Lock()
	defer m.mu.Unlock()
	if cached, ok := m.builders.Get(key); ok && !cached.loadSchemaFailed.Load() {
		return cached
	}
	root, ok := m.neutralRoots[facade]
	if !ok {
		if m.neutralRoots == nil {
			m.neutralRoots = map[Server]*Query{}
		}
		root = newSharedRoot(facade)
		m.neutralRoots[facade] = root
	}
	shared := &SchemaBuilder{
		root:        root,
		entries:     slices.Clone(b.entries),
		clientOwned: true,
		shared:      true,
	}
	m.builders.Add(key, shared)
	return shared
}

// clientIndependentSchema reports whether installing entries builds the same
// schema for every client: they are user modules, whose installs depend only
// on their results, and core modules identified by content, and no user
// module has .env user defaults, which installing expands against the
// building client's host (ModuleFunction.mergeUserDefaultsTypeDefs).
func clientIndependentSchema(entries []modDepEntry) bool {
	for _, entry := range entries {
		switch mod := entry.mod.(type) {
		case *userMod:
			self := mod.res.Self()
			if self == nil {
				return false
			}
			if source := self.GetSource(); source != nil && source.UserDefaults != nil && len(source.UserDefaults.Environ) > 0 {
				return false
			}
		case coreSchemaForker:
			// Core metadata varies by view, which its key covers.
			if stable, ok := mod.(interface{ SchemaASTCacheKey() string }); !ok || stable.SchemaASTCacheKey() == "" {
				return false
			}
		default:
			// Other module implementations may install context-dependent metadata.
			return false
		}
	}
	return true
}

// schemaEntriesKey identifies entries: user modules by attached result, and
// other modules by instance, or, with byContent, by their SchemaASTCacheKey
// when they have one.
func schemaEntriesKey(entries []modDepEntry, byContent bool) (string, bool) {
	var key strings.Builder
	for _, e := range entries {
		switch mod := e.mod.(type) {
		case *userMod:
			id, err := mod.res.ID()
			if err != nil || id == nil || id.EngineResultID() == 0 {
				return "", false
			}
			fmt.Fprintf(&key, "m%d", id.EngineResultID())
		default:
			if byContent {
				if stable, ok := mod.(interface{ SchemaASTCacheKey() string }); ok {
					if identity := stable.SchemaASTCacheKey(); identity != "" {
						fmt.Fprintf(&key, "c%d:%s", len(identity), identity)
						break
					}
				}
			}
			if reflect.ValueOf(mod).Kind() != reflect.Pointer {
				return "", false
			}
			fmt.Fprintf(&key, "p%p", mod)
		}
		// Format the whole struct so a field added to InstallOpts later is
		// part of the key without anyone remembering to add it here.
		fmt.Fprintf(&key, ":%+v;", e.opts)
	}
	return key.String(), true
}

func (b *SchemaBuilder) With(mod Mod, opts InstallOpts) *SchemaBuilder {
	cp := b.Clone()
	for i, e := range cp.entries {
		if e.mod.Name() == mod.Name() {
			promoted := e.opts
			if promoted.SkipConstructor && !opts.SkipConstructor {
				promoted.SkipConstructor = false
			}
			if !promoted.Main && opts.Main {
				promoted.Main = true
			}
			cp.entries[i].opts = promoted
			return cp
		}
	}
	cp.entries = append(cp.entries, modDepEntry{mod: mod, opts: opts})
	return cp
}

// WithPrimaryMods returns b serving mods with their constructors, and without
// the primary entries named in removed (by LatestNamer.CLIName). An entry
// named as a module of mods serves that module instead, with its install
// options and its constructor; the rest of mods are appended. An entry whose
// module is the same as its replacement is kept as it is, so the result
// builds the same schema, and if that leaves every entry as it was, the
// result is b itself, with the server it may keep.
func (b *SchemaBuilder) WithPrimaryMods(mods []Mod, removed []string) *SchemaBuilder {
	cp := b.Clone()
	byName := make(map[string]Mod, len(mods))
	for _, mod := range mods {
		byName[mod.Name()] = mod
	}
	changed := false
	entries := cp.entries[:0]
	for _, e := range cp.entries {
		name := e.mod.Name()
		mod, named := byName[name]
		switch {
		case name == ModuleName:
		case named:
			if same, err := e.mod.Same(mod); err != nil || !same {
				e.mod, changed = mod, true
			}
			if e.opts.SkipConstructor {
				e.opts.SkipConstructor, changed = false, true
			}
			delete(byName, name)
		case !e.opts.SkipConstructor && slices.Contains(removed, LatestNamer.CLIName(name)):
			changed = true
			continue
		}
		entries = append(entries, e)
	}
	cp.entries = entries
	for _, mod := range mods {
		if _, ok := byName[mod.Name()]; ok {
			cp.entries = append(cp.entries, modDepEntry{mod: mod})
			delete(byName, mod.Name())
			changed = true
		}
	}
	if !changed {
		return b
	}
	return cp
}

func (b *SchemaBuilder) Lookup(name string) (Mod, bool) {
	if b == nil {
		return nil, false
	}
	for _, e := range b.entries {
		if e.mod.Name() == name {
			return e.mod, true
		}
	}
	return nil, false
}

// InstallOpts returns the install policy of the served module with the given
// name.
func (b *SchemaBuilder) InstallOpts(name string) (InstallOpts, bool) {
	if b == nil {
		return InstallOpts{}, false
	}
	for _, e := range b.entries {
		if e.mod.Name() == name {
			return e.opts, true
		}
	}
	return InstallOpts{}, false
}

func (b *SchemaBuilder) Mods() []Mod {
	if b == nil {
		return nil
	}
	mods := make([]Mod, len(b.entries))
	for i, e := range b.entries {
		mods[i] = e.mod
	}
	return mods
}

func (b *SchemaBuilder) PrimaryMods() []Mod {
	var mods []Mod
	for _, e := range b.entries {
		if !e.opts.SkipConstructor {
			mods = append(mods, e.mod)
		}
	}
	return mods
}

// MainMods returns the modules whose fields are exposed at the root.
func (b *SchemaBuilder) MainMods() []Mod {
	var mods []Mod
	for _, entry := range b.entries {
		if entry.opts.Main {
			mods = append(mods, entry.mod)
		}
	}
	return mods
}

func (b *SchemaBuilder) Schema(ctx context.Context) (*dagql.Server, error) {
	if err := engine.CheckSnapshotSharePreparation(ctx, "evaluate module schema"); err != nil {
		return nil, err
	}
	owner := b
	if !b.clientOwned {
		session := currentSessionSchemaBuilderMemo(ctx)
		if shared := session.sharedBuilder(b); shared != nil {
			owner = shared
		} else if owner = session.Get(b); !owner.clientOwned {
			// No session can hold this module set's server: build it for
			// this call alone rather than keep it on b.
			owner = b.Clone()
		}
	}
	srv, err := owner.lazilyLoadSchema(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load schema: %w", err)
	}
	return srv, nil
}

// currentSessionSchemaBuilderMemo returns the current session's memo, or nil
// when the context carries no client of a live session.
func currentSessionSchemaBuilderMemo(ctx context.Context) *SchemaBuilderMemo {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil
	}
	memo, err := query.SessionSchemaBuilderMemo(ctx)
	if err != nil {
		return nil
	}
	return memo
}

func (b *SchemaBuilder) SchemaIntrospectionJSONFile(ctx context.Context, hiddenTypes, hiddenFields []string) (dagql.Result[*File], error) {
	dag, err := b.Schema(ctx)
	if err != nil {
		return dagql.Result[*File]{}, err
	}
	return schemaJSONFileFromServer(ctx, dag, hiddenTypes, hiddenFields)
}

func (b *SchemaBuilder) SchemaIntrospectionJSONFileForModule(ctx context.Context) (dagql.Result[*File], error) {
	hiddenTypes, hiddenFields := moduleIntrospectionScrubConfig()
	return b.SchemaIntrospectionJSONFile(ctx, hiddenTypes, hiddenFields)
}

func moduleIntrospectionScrubConfig() ([]string, []string) {
	hiddenTypes := append([]string{}, TypesToIgnoreForModuleIntrospection...)
	for _, typed := range TypesHiddenFromModuleSDKs {
		hiddenTypes = append(hiddenTypes, typed.Type().Name())
	}
	return hiddenTypes, append([]string{}, FieldsToIgnoreForModuleIntrospection...)
}

func (b *SchemaBuilder) SchemaIntrospectionJSONFileForClient(ctx context.Context) (dagql.Result[*File], error) {
	return b.SchemaIntrospectionJSONFile(ctx, nil, nil)
}

func (b *SchemaBuilder) TypeDefs(ctx context.Context, dag *dagql.Server) (dagql.ObjectResultArray[*TypeDef], error) {
	if err := engine.CheckSnapshotSharePreparation(ctx, "build module type definitions"); err != nil {
		return nil, err
	}
	var typeDefs dagql.ObjectResultArray[*TypeDef]
	for _, e := range b.entries {
		modTypeDefs, err := e.mod.TypeDefs(ctx, dag)
		if err != nil {
			return nil, fmt.Errorf("failed to get type defs for module %q: %w", e.mod.Name(), err)
		}
		typeDefs = append(typeDefs, modTypeDefs...)
	}
	return typeDefs, nil
}

func (b *SchemaBuilder) ModTypeFor(ctx context.Context, typeDef *TypeDef) (ModType, bool, error) {
	for _, e := range b.entries {
		modType, ok, err := e.mod.ModTypeFor(ctx, typeDef, false)
		if err != nil {
			return nil, false, fmt.Errorf("failed to get type from mod %q: %w", e.mod.Name(), err)
		}
		if ok {
			return modType, true, nil
		}
	}
	return nil, false, nil
}

func (b *SchemaBuilder) lazilyLoadSchema(ctx context.Context) (loadedSchema *dagql.Server, rerr error) {
	b.loadSchemaLock.Lock()
	defer b.loadSchemaLock.Unlock()
	if b.lazilyLoadedServer != nil {
		return b.lazilyLoadedServer, nil
	}
	if b.loadSchemaErr != nil {
		return nil, b.loadSchemaErr
	}
	if !b.shared {
		// A client's own builder (its served modules, a handle load's module
		// set) serves the server its session shares for the set, and keeps
		// it: the session memo evicting it only stops sharing it. A failure
		// is the shared builder's, possibly another client's build that
		// this one waited on; the memo replaces a failed shared builder, so
		// keep none of it here and ask the memo again next time.
		if shared := currentSessionSchemaBuilderMemo(ctx).sharedBuilder(b); shared != nil {
			srv, err := shared.lazilyLoadSchema(ctx)
			if err != nil {
				return nil, err
			}
			b.lazilyLoadedServer = srv
			return srv, nil
		}
	}
	defer func() {
		if rerr != nil && ctx.Err() != nil {
			// This caller gave up, which says nothing about the schema.
			// Leave the builder unloaded so callers sharing it build
			// with their own contexts instead of inheriting the
			// cancellation. This applies to every builder, not only
			// memoized ones: a Module's Deps sits on a cached Module
			// result that later sessions reuse, and a client's served
			// schema builder outlives the request that first built it.
			return
		}
		b.lazilyLoadedServer = loadedSchema
		b.loadSchemaErr = rerr
		b.loadSchemaFailed.Store(rerr != nil)
	}()

	var nonMains, mains []modDepEntry
	for _, e := range b.entries {
		if e.opts.Main {
			mains = append(mains, e)
		} else {
			nonMains = append(nonMains, e)
		}
	}

	if len(mains) == 0 {
		mods := make([]modInstall, len(b.entries))
		for i, e := range b.entries {
			mods[i] = modInstall(e)
		}
		return buildSchema(ctx, b.root, mods)
	}

	innerMods := make([]modInstall, len(b.entries))
	for i, e := range b.entries {
		opts := e.opts
		opts.Main = false
		innerMods[i] = modInstall{mod: e.mod, opts: opts}
	}
	inner, err := buildSchema(ctx, b.root, innerMods)
	if err != nil {
		return nil, err
	}

	outerMods := make([]modInstall, 0, len(b.entries))
	for _, e := range nonMains {
		outerMods = append(outerMods, modInstall(e))
	}
	for _, e := range mains {
		outerMods = append(outerMods, modInstall(e))
	}
	outer, err := buildSchema(ctx, b.root, outerMods)
	if err != nil {
		return nil, err
	}
	outer.SetCanonical(inner)

	return outer, nil
}
