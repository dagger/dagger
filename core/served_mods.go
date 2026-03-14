package core

import (
	"context"
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

	// lazy schema cache
	lazilyLoadedSchema         *dagql.Server
	lazilyLoadedSchemaJSONFile dagql.Result[*File]
	loadSchemaErr              error
	loadSchemaLock             sync.Mutex
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

// ModDeps returns a ModDeps containing all served modules. This is useful
// for callers that need the dependency-graph API (TypeDefs, ModTypeFor, etc.)
// where constructor policy is irrelevant.
func (s *ServedMods) ModDeps() *ModDeps {
	return NewModDeps(s.root, s.Mods())
}

// Schema builds and caches the combined schema for all served modules.
func (s *ServedMods) Schema(ctx context.Context) (*dagql.Server, error) {
	srv, _, err := s.lazilyLoadSchema(ctx)
	return srv, err
}

func (s *ServedMods) invalidateCache() {
	s.lazilyLoadedSchema = nil
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
		s.lazilyLoadedSchemaJSONFile = loadedSchemaJSONFile
		s.loadSchemaErr = rerr
	}()

	mods := make([]modInstall, len(s.entries))
	for i, e := range s.entries {
		mods[i] = modInstall{
			mod:  e.mod,
			opts: e.opts,
		}
	}

	dag, schemaJSONFile, err := buildSchema(ctx, s.root, mods, nil)
	if err != nil {
		return nil, loadedSchemaJSONFile, err
	}
	return dag, schemaJSONFile, nil
}
