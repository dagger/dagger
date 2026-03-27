package core

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/dagger/dagger/dagql"
)

const (
	modMetaDirPath    = "/.daggermod"
	modMetaOutputPath = "output.json"
	modMetaErrorPath  = "error"

	ModuleName = "daggercore"
)

var TypesToIgnoreForModuleIntrospection = []string{"Host"}

// ModDeps represents a set of module dependencies with per-module install
// policy. It is used both for a module's own dependency graph and for the
// set of modules served to a client session.
//
// ModDeps is immutable: all builder methods return a new instance. Schema
// and introspection results are lazily computed and cached on first access.
type ModDeps struct {
	root    *Query
	entries []modDepEntry

	// lazy schema cache — computed once per instance, never invalidated
	lazilyLoadedSchema         *dagql.Server
	lazilyLoadedSchemaJSONFile dagql.Result[*File]
	lazilyLoadedInner          *dagql.Server
	loadSchemaErr              error
	loadSchemaLock             sync.Mutex
}

type modDepEntry struct {
	mod  Mod
	opts InstallOpts
}

// NewModDeps creates a ModDeps from a list of modules, each installed with
// default (zero) InstallOpts.
func NewModDeps(root *Query, mods []Mod) *ModDeps {
	entries := make([]modDepEntry, len(mods))
	for i, m := range mods {
		entries[i] = modDepEntry{mod: m}
	}
	return &ModDeps{
		root:    root,
		entries: entries,
	}
}

// Clone returns a shallow copy with the same entries.
func (d *ModDeps) Clone() *ModDeps {
	return &ModDeps{
		root:    d.root,
		entries: slices.Clone(d.entries),
	}
}

// Prepend returns a new ModDeps with the given modules (default opts)
// inserted before the existing entries.
func (d *ModDeps) Prepend(mods ...Mod) *ModDeps {
	extra := make([]modDepEntry, len(mods))
	for i, m := range mods {
		extra[i] = modDepEntry{mod: m}
	}
	return &ModDeps{
		root:    d.root,
		entries: append(extra, d.entries...),
	}
}

// Append returns a new ModDeps with the given modules (default opts)
// appended after the existing entries.
func (d *ModDeps) Append(mods ...Mod) *ModDeps {
	extra := make([]modDepEntry, len(mods))
	for i, m := range mods {
		extra[i] = modDepEntry{mod: m}
	}
	return &ModDeps{
		root:    d.root,
		entries: append(slices.Clone(d.entries), extra...),
	}
}

// With returns a new ModDeps that includes the given module with the
// specified install options. If the module is already present (by name),
// it is not duplicated — but its options are promoted to the less
// restrictive combination of old and new.
func (d *ModDeps) With(mod Mod, opts InstallOpts) *ModDeps {
	cp := &ModDeps{
		root:    d.root,
		entries: slices.Clone(d.entries),
	}
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

// Lookup returns the module with the given name, if present.
func (d *ModDeps) Lookup(name string) (Mod, bool) {
	for _, e := range d.entries {
		if e.mod.Name() == name {
			return e.mod, true
		}
	}
	return nil, false
}

// Mods returns the list of all modules (regardless of install policy).
func (d *ModDeps) Mods() []Mod {
	mods := make([]Mod, len(d.entries))
	for i, e := range d.entries {
		mods[i] = e.mod
	}
	return mods
}

// PrimaryMods returns only the modules whose constructors should appear
// on the Query root (i.e. those not installed with SkipConstructor).
func (d *ModDeps) PrimaryMods() []Mod {
	var mods []Mod
	for _, e := range d.entries {
		if !e.opts.SkipConstructor {
			mods = append(mods, e.mod)
		}
	}
	return mods
}

// Schema builds and caches the combined outer (client-facing) schema for
// all modules. When any module has Entrypoint set, entrypoint proxy fields
// are installed on Query.
func (d *ModDeps) Schema(ctx context.Context) (*dagql.Server, error) {
	srv, _, err := d.lazilyLoadSchema(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load schema: %w", err)
	}
	dagqlCache, err := d.root.Cache(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cache: %w", err)
	}
	return srv.WithCache(dagqlCache), nil
}

// Server returns the inner (canonical) server used for ID loading. This
// server has no entrypoint proxies, so IDs are always evaluated against a
// clean schema where no proxy can shadow a core field. When no module has
// Entrypoint set, the inner and outer servers are the same.
func (d *ModDeps) Server(ctx context.Context) (*dagql.Server, error) {
	_, _, err := d.lazilyLoadSchema(ctx)
	if err != nil {
		return nil, err
	}
	return d.lazilyLoadedInner, nil
}

// SchemaJSONFile returns the introspection JSON file for the schema.
func (d *ModDeps) SchemaJSONFile(ctx context.Context) (dagql.Result[*File], error) {
	_, schemaJSONFile, err := d.lazilyLoadSchema(ctx)
	return schemaJSONFile, err
}

// SchemaIntrospectionJSONFile returns an introspection JSON file for the
// schema, optionally hiding the given types. This is used by SDK codegen.
func (d *ModDeps) SchemaIntrospectionJSONFile(ctx context.Context, hiddenTypes []string) (dagql.Result[*File], error) {
	if len(hiddenTypes) == 0 {
		return d.SchemaJSONFile(ctx)
	}
	// Hidden types require a separate JSON file built from the same schema.
	dag, err := d.Schema(ctx)
	if err != nil {
		return dagql.Result[*File]{}, err
	}
	return schemaJSONFileFromServer(ctx, dag, hiddenTypes)
}

// SchemaIntrospectionJSONFileForModule returns an introspection JSON file
// with types hidden that should not be exposed to module SDKs.
func (d *ModDeps) SchemaIntrospectionJSONFileForModule(ctx context.Context) (dagql.Result[*File], error) {
	return d.SchemaIntrospectionJSONFile(ctx, TypesToIgnoreForModuleIntrospection)
}

// TypeDefs returns type definitions for all modules by introspecting the
// combined schema. Directives in the schema carry module metadata
// (SourceModuleName, defaultPath, etc.), so no merging step is required.
func (d *ModDeps) TypeDefs(ctx context.Context, dag *dagql.Server) ([]*TypeDef, error) {
	return TypeDefsFromSchema(dag, nil)
}

// ModTypeFor searches the modules for the given type def, returning the
// ModType if found. This does not recurse to transitive dependencies; it
// only returns types directly exposed by the schema of the top-level deps.
func (d *ModDeps) ModTypeFor(ctx context.Context, typeDef *TypeDef) (ModType, bool, error) {
	for _, e := range d.entries {
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

func (d *ModDeps) lazilyLoadSchema(ctx context.Context) (
	loadedSchema *dagql.Server,
	loadedSchemaJSONFile dagql.Result[*File],
	rerr error,
) {
	d.loadSchemaLock.Lock()
	defer d.loadSchemaLock.Unlock()
	if d.lazilyLoadedSchema != nil {
		return d.lazilyLoadedSchema, d.lazilyLoadedSchemaJSONFile, nil
	}
	if d.loadSchemaErr != nil {
		return nil, loadedSchemaJSONFile, d.loadSchemaErr
	}
	defer func() {
		d.lazilyLoadedSchema = loadedSchema
		d.lazilyLoadedInner = loadedSchema // default: inner == outer
		d.lazilyLoadedSchemaJSONFile = loadedSchemaJSONFile
		d.loadSchemaErr = rerr
	}()

	// Check if any entry has Entrypoint set.
	var nonEntrypoints, entrypoints []modDepEntry
	for _, e := range d.entries {
		if e.opts.Entrypoint {
			entrypoints = append(entrypoints, e)
		} else {
			nonEntrypoints = append(nonEntrypoints, e)
		}
	}

	if len(entrypoints) == 0 {
		// No entrypoints — single server suffices (inner == outer).
		mods := make([]modInstall, len(d.entries))
		for i, e := range d.entries {
			mods[i] = modInstall(e)
		}
		dag, schemaJSONFile, err := buildSchema(ctx, d.root, mods, nil)
		if err != nil {
			return nil, loadedSchemaJSONFile, err
		}
		return dag, schemaJSONFile, nil
	}

	// Build inner server: all modules with Entrypoint forced to false.
	innerMods := make([]modInstall, len(d.entries))
	for i, e := range d.entries {
		opts := e.opts
		opts.Entrypoint = false
		innerMods[i] = modInstall{mod: e.mod, opts: opts}
	}
	inner, _, err := buildSchema(ctx, d.root, innerMods, nil)
	if err != nil {
		return nil, loadedSchemaJSONFile, err
	}

	// Build outer server: all modules with real Entrypoint flags.
	outerMods := make([]modInstall, 0, len(d.entries))
	for _, e := range nonEntrypoints {
		outerMods = append(outerMods, modInstall(e))
	}
	for _, e := range entrypoints {
		outerMods = append(outerMods, modInstall(e))
	}
	outer, schemaJSONFile, err := buildSchema(ctx, d.root, outerMods, nil)
	if err != nil {
		return nil, loadedSchemaJSONFile, err
	}

	// Wire up delegation: outer server delegates ID loading to inner server,
	// and proxy resolvers use the inner server for Select calls.
	outer.IDLoader = inner.Load
	outer.Inner = inner

	// Override the default: inner is the canonical server for ID loading.
	defer func() {
		d.lazilyLoadedInner = inner
	}()

	return outer, schemaJSONFile, nil
}
