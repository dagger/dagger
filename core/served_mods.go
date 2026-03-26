package core

import (
	"context"
	"fmt"
	"sync"

	"github.com/dagger/dagger/dagql"
)

// ServedMods is the set of modules served to a client session. Unlike
// ModDeps (which represents a module's dependency graph), ServedMods
// tracks per-module install policy: whether a module's constructor
// should appear on the Query root, and whether its main-object
// methods should be proxied there as an entrypoint.
type ServedMods struct {
	root    *Query
	entries []servedModEntry

	// lazy schema cache — outer (client-facing) server with entrypoint proxies
	lazilyLoadedSchema         *dagql.Server
	lazilyLoadedSchemaJSONFile dagql.Result[*File]
	// lazy inner (canonical) server — no entrypoint proxies, used for ID loading
	lazilyLoadedInner *dagql.Server
	loadSchemaErr     error
	loadSchemaLock    sync.Mutex
}

type servedModEntry struct {
	mod  Mod
	opts InstallOpts
}

func NewServedMods(root *Query) *ServedMods {
	return &ServedMods{root: root}
}

// Add adds a module with the given install options.
//
// If the module is already present, it is not added again — but if it was
// previously added with a more restrictive install policy, it is promoted to
// the less restrictive one.
func (s *ServedMods) Add(mod Mod, opts InstallOpts) {
	for i, e := range s.entries {
		if e.mod.Name() == mod.Name() {
			promoted := e.opts
			if promoted.SkipConstructor && !opts.SkipConstructor {
				promoted.SkipConstructor = false
			}
			if !promoted.Entrypoint && opts.Entrypoint {
				promoted.Entrypoint = true
			}
			if promoted != e.opts {
				s.entries[i].opts = promoted
				s.invalidateCache()
			}
			return
		}
	}
	s.entries = append(s.entries, servedModEntry{
		mod:  mod,
		opts: opts,
	})
	s.invalidateCache()
}

// Lookup returns the module with the given name, if present.
func (s *ServedMods) Lookup(name string) (Mod, bool) {
	for _, e := range s.entries {
		if e.mod.Name() == name {
			return e.mod, true
		}
	}
	return nil, false
}

// Mods returns the list of all served modules (regardless of install policy).
func (s *ServedMods) Mods() []Mod {
	mods := make([]Mod, len(s.entries))
	for i, e := range s.entries {
		mods[i] = e.mod
	}
	return mods
}

// PrimaryMods returns only the modules that were directly loaded (not
// dependency-only modules). These are the modules whose constructors
// appear on the Query root.
func (s *ServedMods) PrimaryMods() []Mod {
	var mods []Mod
	for _, e := range s.entries {
		if !e.opts.SkipConstructor {
			mods = append(mods, e.mod)
		}
	}
	return mods
}

// TypeDefs returns type definitions for all served modules by introspecting
// the combined schema. This is the single source of truth: directives in the
// schema carry module metadata (SourceModuleName, defaultPath, etc.), so no
// merging step is required.
func (s *ServedMods) TypeDefs(ctx context.Context, dag *dagql.Server) ([]*TypeDef, error) {
	// Include all types — no filter.
	return TypeDefsFromSchema(dag, nil)
}

// Schema builds and caches the combined outer (client-facing) schema for all
// served modules. This server includes entrypoint proxy fields on Query.
func (s *ServedMods) Schema(ctx context.Context) (*dagql.Server, error) {
	srv, _, err := s.lazilyLoadSchema(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load schema: %w", err)
	}
	dagqlCache, err := s.root.Cache(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cache: %w", err)
	}
	return srv.WithCache(dagqlCache), nil
}

// SchemaJSONFile returns the introspection JSON file for the schema.
func (s *ServedMods) SchemaJSONFile(ctx context.Context) (dagql.Result[*File], error) {
	_, schemaJSONFile, err := s.lazilyLoadSchema(ctx)
	return schemaJSONFile, err
}

// SchemaIntrospectionJSONFile returns an introspection JSON file for the
// schema, optionally hiding the given types. This is used by SDK codegen.
func (s *ServedMods) SchemaIntrospectionJSONFile(ctx context.Context, hiddenTypes []string) (dagql.Result[*File], error) {
	if len(hiddenTypes) == 0 {
		return s.SchemaJSONFile(ctx)
	}
	// Hidden types require a separate JSON file built from the same schema.
	dag, err := s.Schema(ctx)
	if err != nil {
		return dagql.Result[*File]{}, err
	}
	return schemaJSONFileFromServer(ctx, dag, hiddenTypes)
}

// SchemaIntrospectionJSONFileForModule returns an introspection JSON file with
// types hidden that should not be exposed to module SDKs.
func (s *ServedMods) SchemaIntrospectionJSONFileForModule(ctx context.Context) (dagql.Result[*File], error) {
	return s.SchemaIntrospectionJSONFile(ctx, TypesToIgnoreForModuleIntrospection)
}

// ModTypeFor searches the served modules for the given type def, returning the
// ModType if found.
func (s *ServedMods) ModTypeFor(ctx context.Context, typeDef *TypeDef) (ModType, bool, error) {
	for _, e := range s.entries {
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

// Server returns the inner (canonical) server used for ID loading. This server
// has no entrypoint proxies, so IDs are always evaluated against a clean schema
// where no proxy can shadow a core field. When no module has Entrypoint set,
// the inner and outer servers are the same.
func (s *ServedMods) Server(ctx context.Context) (*dagql.Server, error) {
	_, _, err := s.lazilyLoadSchema(ctx)
	if err != nil {
		return nil, err
	}
	return s.lazilyLoadedInner, nil
}

func (s *ServedMods) invalidateCache() {
	s.lazilyLoadedSchema = nil
	s.lazilyLoadedInner = nil
	s.lazilyLoadedSchemaJSONFile = dagql.Result[*File]{}
	s.loadSchemaErr = nil
}

func (s *ServedMods) lazilyLoadSchema(ctx context.Context) (
	loadedSchema *dagql.Server,
	loadedSchemaJSONFile dagql.Result[*File],
	rerr error,
) {
	s.loadSchemaLock.Lock()
	defer s.loadSchemaLock.Unlock()
	if s.lazilyLoadedSchema != nil {
		return s.lazilyLoadedSchema, s.lazilyLoadedSchemaJSONFile, nil
	}
	if s.loadSchemaErr != nil {
		return nil, loadedSchemaJSONFile, s.loadSchemaErr
	}
	defer func() {
		s.lazilyLoadedSchema = loadedSchema
		s.lazilyLoadedInner = loadedSchema // default: inner == outer
		s.lazilyLoadedSchemaJSONFile = loadedSchemaJSONFile
		s.loadSchemaErr = rerr
	}()

	// Check if any entry has Entrypoint set.
	hasEntrypoint := false
	for _, e := range s.entries {
		if e.opts.Entrypoint {
			hasEntrypoint = true
			break
		}
	}

	if !hasEntrypoint {
		// No entrypoints — single server suffices (inner == outer).
		mods := make([]modInstall, len(s.entries))
		for i, e := range s.entries {
			mods[i] = modInstall(e)
		}
		dag, schemaJSONFile, err := buildSchema(ctx, s.root, mods, nil)
		if err != nil {
			return nil, loadedSchemaJSONFile, err
		}
		return dag, schemaJSONFile, nil
	}

	// Build inner server: all modules with Entrypoint forced to false.
	innerMods := make([]modInstall, len(s.entries))
	for i, e := range s.entries {
		opts := e.opts
		opts.Entrypoint = false
		innerMods[i] = modInstall{mod: e.mod, opts: opts}
	}
	inner, _, err := buildSchema(ctx, s.root, innerMods, nil)
	if err != nil {
		return nil, loadedSchemaJSONFile, err
	}

	// Build outer server: all modules with real Entrypoint flags.
	outerMods := make([]modInstall, len(s.entries))
	for i, e := range s.entries {
		outerMods[i] = modInstall(e)
	}
	outer, schemaJSONFile, err := buildSchema(ctx, s.root, outerMods, nil)
	if err != nil {
		return nil, loadedSchemaJSONFile, err
	}

	// Wire up delegation: outer server delegates ID loading to inner server,
	// and proxy resolvers use the inner server for Select calls.
	outer.IDLoader = inner.Load
	outer.Inner = inner

	// Override the default: inner is the canonical server for ID loading.
	defer func() {
		s.lazilyLoadedInner = inner
	}()

	return outer, schemaJSONFile, nil
}
