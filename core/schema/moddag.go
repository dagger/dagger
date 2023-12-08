package schema

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/dagger/dagger/core"
	"github.com/opencontainers/go-digest"
)

const (
	modMetaDirPath    = "/.daggermod"
	modMetaOutputPath = "output.json"

	coreModuleName = "daggercore"
)

/*
Mod is a module in loaded into the server's DAG of modules; it's the vertex type of the DAG.
It's an interface so we can abstract over user modules and core and treat them the same.
*/
type Mod interface {
	// The name of the module
	Name() string

	// The digest of the module itself plus the recursive digests of the DAG it depends on
	DagDigest() digest.Digest

	// The direct dependencies of this module
	Dependencies() []Mod

	// The schema+resolvers exposed by this module (does not include dependencies)
	Schema(context.Context) ([]SchemaResolvers, error)

	// The introspection json for this module's schema
	SchemaIntrospectionJSON(context.Context) (string, error)

	// ModTypeFor returns the ModType for the given typedef based on this module's schema.
	// The returned type will have any namespacing already applied.
	// If checkDirectDeps is true, then its direct dependencies will also be checked.
	ModTypeFor(ctx context.Context, typeDef *core.TypeDef, checkDirectDeps bool) (ModType, bool, error)
}

/*
ModDeps represents a set of dependencies for a module or for a caller depending on a
particular set of modules to be served.
*/
type ModDeps struct {
	api       *APIServer
	mods      []Mod
	dagDigest digest.Digest

	// should not be read directly, call Schema and SchemaIntrospectionJSON instead
	lazilyLoadedSchema            *CompiledSchema
	lazilyLoadedIntrospectionJSON string
	loadSchemaErr                 error
	loadSchemaLock                sync.Mutex
}

func newModDeps(api *APIServer, mods []Mod) (*ModDeps, error) {
	seen := map[digest.Digest]struct{}{}
	finalMods := make([]Mod, 0, len(mods))
	for _, mod := range mods {
		dagDigest := mod.DagDigest()
		if _, ok := seen[dagDigest]; ok {
			continue
		}
		seen[dagDigest] = struct{}{}
		finalMods = append(finalMods, mod)
	}
	sort.Slice(finalMods, func(i, j int) bool {
		return finalMods[i].DagDigest().String() < finalMods[j].DagDigest().String()
	})
	dagDigests := make([]string, 0, len(finalMods))
	for _, mod := range finalMods {
		dagDigests = append(dagDigests, mod.DagDigest().String())
	}
	dagDigest := digest.FromString(strings.Join(dagDigests, " "))

	return &ModDeps{
		api:       api,
		mods:      mods,
		dagDigest: dagDigest,
	}, nil
}

// The digest of all the modules in the DAG
func (d *ModDeps) DagDigest() digest.Digest {
	return d.dagDigest
}

// The combined schema exposed by each mod in this set of dependencies
func (d *ModDeps) Schema(ctx context.Context) (*CompiledSchema, error) {
	schema, _, err := d.lazilyLoadSchema(ctx)
	if err != nil {
		return nil, err
	}
	return schema, nil
}

// The introspection json for combined schema exposed by each mod in this set of dependencies
func (d *ModDeps) SchemaIntrospectionJSON(ctx context.Context) (string, error) {
	_, introspectionJSON, err := d.lazilyLoadSchema(ctx)
	if err != nil {
		return "", err
	}
	return introspectionJSON, nil
}

func (d *ModDeps) lazilyLoadSchema(ctx context.Context) (loadedSchema *CompiledSchema, loadedIntrospectionJSON string, rerr error) {
	d.loadSchemaLock.Lock()
	defer d.loadSchemaLock.Unlock()
	if d.lazilyLoadedSchema != nil {
		return d.lazilyLoadedSchema, d.lazilyLoadedIntrospectionJSON, nil
	}
	if d.loadSchemaErr != nil {
		return nil, "", d.loadSchemaErr
	}
	defer func() {
		d.lazilyLoadedSchema = loadedSchema
		d.lazilyLoadedIntrospectionJSON = loadedIntrospectionJSON
		d.loadSchemaErr = rerr
	}()

	var schemas []SchemaResolvers
	modNames := make([]string, 0, len(d.mods)) // for debugging+error messages
	for _, mod := range d.mods {
		modSchemas, err := mod.Schema(ctx)
		if err != nil {
			return nil, "", fmt.Errorf("failed to get schema for module %q: %w", mod.Name(), err)
		}
		schemas = append(schemas, modSchemas...)
		modNames = append(modNames, mod.Name())
	}
	schema, err := mergeSchemaResolvers(schemas...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to merge schemas of %+v: %w", modNames, err)
	}
	introspectionJSON, err := schemaIntrospectionJSON(ctx, *schema.Compiled)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get schema introspection JSON: %w", err)
	}

	return schema, introspectionJSON, nil
}

// Search the deps for the given type def, returning the ModType if found. This does not recurse
// to transitive dependencies; it only returns types directly exposed by the schema of the top-level
// deps.
func (d *ModDeps) ModTypeFor(ctx context.Context, typeDef *core.TypeDef) (ModType, bool, error) {
	for _, mod := range d.mods {
		modType, ok, err := mod.ModTypeFor(ctx, typeDef, false)
		if err != nil {
			return nil, false, fmt.Errorf("failed to get type from mod %q: %w", mod.Name(), err)
		}
		if !ok {
			continue
		}
		return modType, true, nil
	}
	return nil, false, nil
}
