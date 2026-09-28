// DESIGN DOC: hack/designs/entrypoint-proxy.md

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
type SchemaBuilder struct {
	// root is the query value installed into derived schema servers. Query only
	// carries the engine Server facade; runtime selection and authority come
	// from the held ClientScope in each execution context, never from a pointer
	// captured by the builder.
	root    *Query
	entries []modDepEntry

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

func (b *SchemaBuilder) WithRoot(root *Query) *SchemaBuilder {
	cp := b.Clone()
	cp.root = root
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
// a server and reinstall every module.
//
// The engine keeps one memo per client and drops it with the client's other
// heavy state, so its entries live exactly as long as the client that loaded
// them. It is unbounded: a client loads handles through few distinct module
// sets, and an entry costs roughly one forked core schema plus its modules'
// types.
type SchemaBuilderMemo struct {
	mu       sync.Mutex
	builders map[string]*SchemaBuilder
}

func NewSchemaBuilderMemo() *SchemaBuilderMemo {
	return &SchemaBuilderMemo{builders: map[string]*SchemaBuilder{}}
}

// Get returns the builder memoized for exactly b's root and entries, or
// memoizes b. A nil memo returns b.
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
	m.builders[key] = b
	return b
}

// memoKey identifies b's root and entries: user modules by attached result,
// anything else (core) by instance. Entry order and install options are part
// of the key, since both shape the built schema.
func (b *SchemaBuilder) memoKey() (string, bool) {
	var key strings.Builder
	fmt.Fprintf(&key, "r%p;", b.root)
	for _, e := range b.entries {
		switch mod := e.mod.(type) {
		case *userMod:
			id, err := mod.res.ID()
			if err != nil || id == nil || id.EngineResultID() == 0 {
				return "", false
			}
			fmt.Fprintf(&key, "m%d", id.EngineResultID())
		default:
			if reflect.ValueOf(mod).Kind() != reflect.Pointer {
				return "", false
			}
			fmt.Fprintf(&key, "p%p", mod)
		}
		fmt.Fprintf(&key, ":%t:%t;", e.opts.SkipConstructor, e.opts.Entrypoint)
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
			if !promoted.Entrypoint && opts.Entrypoint {
				promoted.Entrypoint = true
			}
			cp.entries[i].opts = promoted
			return cp
		}
	}
	cp.entries = append(cp.entries, modDepEntry{mod: mod, opts: opts})
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

// EntrypointMods returns the modules whose fields are exposed at the root.
func (b *SchemaBuilder) EntrypointMods() []Mod {
	var mods []Mod
	for _, entry := range b.entries {
		if entry.opts.Entrypoint {
			mods = append(mods, entry.mod)
		}
	}
	return mods
}

func (b *SchemaBuilder) Schema(ctx context.Context) (*dagql.Server, error) {
	if err := engine.CheckSnapshotSharePreparation(ctx, "evaluate module schema"); err != nil {
		return nil, err
	}
	srv, err := b.lazilyLoadSchema(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load schema: %w", err)
	}
	return srv, nil
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
			// cancellation.
			return
		}
		b.lazilyLoadedServer = loadedSchema
		b.loadSchemaErr = rerr
		b.loadSchemaFailed.Store(rerr != nil)
	}()

	var nonEntrypoints, entrypoints []modDepEntry
	for _, e := range b.entries {
		if e.opts.Entrypoint {
			entrypoints = append(entrypoints, e)
		} else {
			nonEntrypoints = append(nonEntrypoints, e)
		}
	}

	if len(entrypoints) == 0 {
		mods := make([]modInstall, len(b.entries))
		for i, e := range b.entries {
			mods[i] = modInstall(e)
		}
		return buildSchema(ctx, b.root, mods)
	}

	innerMods := make([]modInstall, len(b.entries))
	for i, e := range b.entries {
		opts := e.opts
		opts.Entrypoint = false
		innerMods[i] = modInstall{mod: e.mod, opts: opts}
	}
	inner, err := buildSchema(ctx, b.root, innerMods)
	if err != nil {
		return nil, err
	}

	outerMods := make([]modInstall, 0, len(b.entries))
	for _, e := range nonEntrypoints {
		outerMods = append(outerMods, modInstall(e))
	}
	for _, e := range entrypoints {
		outerMods = append(outerMods, modInstall(e))
	}
	outer, err := buildSchema(ctx, b.root, outerMods)
	if err != nil {
		return nil, err
	}
	outer.SetCanonical(inner)

	return outer, nil
}
