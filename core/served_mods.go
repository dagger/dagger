package core

import (
	"context"
	"sync"

	"github.com/dagger/dagger/dagql"
)

// ServedMods is the set of modules served to a client session. Unlike
// ModDeps (which represents a module's dependency graph), ServedMods
// tracks per-module install policy: whether a module's constructor
// should appear on the Query root or just its types.
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
	mod             Mod
	skipConstructor bool
}

func NewServedMods(root *Query) *ServedMods {
	return &ServedMods{root: root}
}

// Add adds a module. If skipConstructor is false, the module's constructor
// will be installed on the Query root; otherwise only its types are
// installed for schema resolution.
//
// If the module is already present, it is not added again — but if it was
// previously added with skipConstructor=true and is now added with
// skipConstructor=false, it is promoted to include its constructor.
func (s *ServedMods) Add(mod Mod, skipConstructor bool) {
	for i, e := range s.entries {
		if e.mod.Name() == mod.Name() {
			// Promote from type-only to full if needed.
			if e.skipConstructor && !skipConstructor {
				s.entries[i].skipConstructor = false
				s.invalidateCache()
			}
			return
		}
	}
	s.entries = append(s.entries, servedModEntry{
		mod:             mod,
		skipConstructor: skipConstructor,
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
			opts: InstallOpts{SkipConstructor: e.skipConstructor},
		}
	}

	dag, schemaJSONFile, err := buildSchema(ctx, s.root, mods, nil)
	if err != nil {
		return nil, loadedSchemaJSONFile, err
	}
	return dag, schemaJSONFile, nil
}
