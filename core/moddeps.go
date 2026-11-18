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
// heavy state, so its entries live exactly as long as the client that loaded
// them. It is unbounded by design: entries are only created for module sets
// the client actually loads through, that count grows with distinct module
// results (e.g. one per reload in a dev loop) rather than with load volume,
// and an entry costs roughly one forked core schema plus its modules' types.
//
// The engine also keeps one memo per session for the schemas of builders that
// are not client-owned, such as a Module's Deps (see SchemaBuilder.Schema).
// It is shared by the session's clients, including the nested client of every
// function call, and dropped when the session ends; its entries grow with the
// distinct module sets the session uses.
//
// A built server is an immutable type registry; the dagql result cache is
// engine-wide and never held by a server, so sharing one shares no results.
type SchemaBuilderMemo struct {
	mu       sync.Mutex
	builders map[string]*SchemaBuilder

	// canonicalArgTypes reuses immutable argument metadata across the schemas
	// of nested clients. It never shares their query roots or installed fields.
	canonicalArgTypes map[string]*canonicalArgType
	modTypes          map[modTypeMemoKey]*modTypeMemoEntry
	gitClients        map[declaredGitClientsKey]*declaredGitClientsEntry
	// moduleServers holds the client's standalone server for each module
	// whose artifacts it discovers, evaluates or checks
	// (dagqlServerForModule).
	moduleServers map[string]*moduleServerEntry
	// builtModuleSets holds what this session built a schema server for, by
	// module set, and builtServers how many it holds (countSchemaBuild).
	builtModuleSets map[string]*sessionBuilds
	builtServers    int
	// workspaceModules holds the modules installed in a value workspace, by
	// its recipe digest (see MCP.loadBase).
	workspaceModules map[string]*InstalledWorkspaceModules
}

// WorkspaceModules returns the modules installed in the workspace key names,
// loading them with load the first time. A failed load is not kept. Two
// callers may load the same key at once; the last load is kept. A nil memo
// loads every time.
func (m *SchemaBuilderMemo) WorkspaceModules(key string, load func() (*InstalledWorkspaceModules, error)) (*InstalledWorkspaceModules, error) {
	if m == nil {
		return load()
	}
	m.mu.Lock()
	cached, ok := m.workspaceModules[key]
	m.mu.Unlock()
	if ok {
		return cached, nil
	}
	// Loading builds schemas through this memo, so it runs unlocked.
	loaded, err := load()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.workspaceModules == nil {
		m.workspaceModules = map[string]*InstalledWorkspaceModules{}
	}
	m.workspaceModules[key] = loaded
	m.mu.Unlock()
	return loaded, nil
}

func NewSchemaBuilderMemo() *SchemaBuilderMemo {
	return &SchemaBuilderMemo{builders: map[string]*SchemaBuilder{}}
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
	if cached := m.builders[key]; cached != nil && !cached.loadSchemaFailed.Load() {
		return cached
	}
	owned := b.ClientOwned()
	m.builders[key] = owned
	return owned
}

// memoKey identifies b's root and entries: user modules by attached result,
// anything else (core) by instance. Entry order and install options are part
// of the key, since both shape the built schema.
func (b *SchemaBuilder) memoKey() (string, bool) {
	entries, ok := schemaEntriesKey(b.entries, false)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("r%p;%s", b.root, entries), true
}

func schemaEntriesKey(entries []modDepEntry, metadataOnly bool) (string, bool) {
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
			if metadataOnly {
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
		owner = currentSessionSchemaBuilderMemo(ctx).Get(b)
		if !owner.clientOwned {
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
