// DESIGN DOC: hack/designs/entrypoint-proxy.md

package core

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

const (
	modMetaDirPath    = "/.daggermod"
	modMetaOutputPath = "output.json"
	modMetaErrorPath  = "error"

	ModuleName = "daggercore"
)

var TypesToIgnoreForModuleIntrospection = []string{"Host"}

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
	root    *Query
	entries []modDepEntry

	lazilyLoadedServer *dagql.Server
	loadSchemaErr      error
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

func (b *SchemaBuilder) Schema(ctx context.Context) (*dagql.Server, error) {
	srv, err := b.lazilyLoadSchema(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load schema: %w", err)
	}
	return srv, nil
}

func (b *SchemaBuilder) SchemaIntrospectionJSONFile(ctx context.Context, hiddenTypes []string) (dagql.Result[*File], error) {
	dag, err := b.Schema(ctx)
	if err != nil {
		return dagql.Result[*File]{}, err
	}
	return schemaJSONFileFromServer(ctx, dag, hiddenTypes)
}

func (b *SchemaBuilder) SchemaIntrospectionJSONFileForModule(ctx context.Context) (dagql.Result[*File], error) {
	hiddenTypes := append([]string{}, TypesToIgnoreForModuleIntrospection...)
	for _, typed := range TypesHiddenFromModuleSDKs {
		hiddenTypes = append(hiddenTypes, typed.Type().Name())
	}
	return b.SchemaIntrospectionJSONFile(ctx, hiddenTypes)
}

func (b *SchemaBuilder) SchemaIntrospectionJSONFileForClient(ctx context.Context) (dagql.Result[*File], error) {
	return b.SchemaIntrospectionJSONFile(ctx, []string{})
}

func (b *SchemaBuilder) TypeDefs(ctx context.Context, dag *dagql.Server) (dagql.ObjectResultArray[*TypeDef], error) {
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
		b.lazilyLoadedServer = loadedSchema
		b.loadSchemaErr = rerr
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
