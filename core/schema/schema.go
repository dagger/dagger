package schema

import (
	"fmt"
	"sort"
	"sync"

	"github.com/containerd/containerd/content"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/tracing"
	"github.com/dagger/graphql"
	tools "github.com/dagger/graphql-go-tools"
	"github.com/moby/buildkit/util/leaseutil"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

type InitializeArgs struct {
	BuildkitClient *buildkit.Client
	Platform       specs.Platform
	ProgSockPath   string
	OCIStore       content.Store
	LeaseManager   *leaseutil.Manager
	Auth           *auth.RegistryAuthProvider
	Secrets        *core.SecretStore
}

func New(params InitializeArgs) (*MergedSchemas, error) {
	merged := &MergedSchemas{
		bk:              params.BuildkitClient,
		platform:        params.Platform,
		progSockPath:    params.ProgSockPath,
		auth:            params.Auth,
		secrets:         params.Secrets,
		separateSchemas: map[string]ExecutableSchema{},
	}
	host := core.NewHost()
	buildCache := core.NewCacheMap[uint64, *core.Container]()
	err := merged.addSchemas(
		&querySchema{merged},
		&directorySchema{merged, host, buildCache},
		&fileSchema{merged, host},
		&gitSchema{merged},
		&containerSchema{
			merged,
			host,
			params.OCIStore,
			params.LeaseManager,
			buildCache,
			core.NewCacheMap[uint64, *specs.Descriptor](),
		},
		&cacheSchema{merged},
		&secretSchema{merged},
		&hostSchema{merged, host},
		&projectSchema{merged},
		&httpSchema{merged},
		&platformSchema{merged},
		&socketSchema{merged, host},
	)
	if err != nil {
		return nil, err
	}
	return merged, nil
}

type MergedSchemas struct {
	bk           *buildkit.Client
	platform     specs.Platform
	progSockPath string
	auth         *auth.RegistryAuthProvider
	secrets      *core.SecretStore

	schemaMu        sync.RWMutex
	separateSchemas map[string]ExecutableSchema
	mergedSchema    ExecutableSchema
	compiledSchema  *graphql.Schema
}

func (s *MergedSchemas) Schema() *graphql.Schema {
	s.schemaMu.RLock()
	defer s.schemaMu.RUnlock()
	return s.compiledSchema
}

func (s *MergedSchemas) addSchemas(schemasToAdd ...ExecutableSchema) error {
	s.schemaMu.Lock()
	defer s.schemaMu.Unlock()

	// make a copy of the current schemas
	separateSchemas := map[string]ExecutableSchema{}
	for k, v := range s.separateSchemas {
		separateSchemas[k] = v
	}

	// add in new schemas, recursively adding dependencies
	var newSchemas []ExecutableSchema
	var addOne func(newSchema ExecutableSchema)
	addOne = func(newSchema ExecutableSchema) {
		// Skip adding schema if it has already been added, higher callers
		// are expected to handle checks that schemas with the same name are
		// actually equivalent
		_, ok := separateSchemas[newSchema.Name()]
		if ok {
			return
		}

		newSchemas = append(newSchemas, newSchema)
		separateSchemas[newSchema.Name()] = newSchema
		for _, dep := range newSchema.Dependencies() {
			// TODO:(sipsma) guard against infinite recursion
			addOne(dep)
		}
	}
	for _, schemaToAdd := range schemasToAdd {
		addOne(schemaToAdd)
	}
	if len(newSchemas) == 0 {
		return nil
	}
	sort.Slice(newSchemas, func(i, j int) bool {
		return newSchemas[i].Name() < newSchemas[j].Name()
	})

	// merge existing and new schemas together
	merged, err := mergeExecutableSchemas(s.mergedSchema, newSchemas...)
	if err != nil {
		return err
	}

	compiled, err := compile(merged)
	if err != nil {
		return err
	}

	s.separateSchemas = separateSchemas
	s.mergedSchema = merged
	s.compiledSchema = compiled
	return nil
}

func (s *MergedSchemas) resolvers() Resolvers {
	s.schemaMu.Lock()
	defer s.schemaMu.Unlock()
	return s.mergedSchema.Resolvers()
}

type ExecutableSchema interface {
	Name() string
	Schema() string
	Resolvers() Resolvers
	Dependencies() []ExecutableSchema
}

type StaticSchemaParams struct {
	Name         string
	Schema       string
	Resolvers    Resolvers
	Dependencies []ExecutableSchema
}

func StaticSchema(p StaticSchemaParams) ExecutableSchema {
	return &staticSchema{p}
}

var _ ExecutableSchema = &staticSchema{}

type staticSchema struct {
	StaticSchemaParams
}

func (s *staticSchema) Name() string {
	return s.StaticSchemaParams.Name
}

func (s *staticSchema) Schema() string {
	return s.StaticSchemaParams.Schema
}

func (s *staticSchema) Resolvers() Resolvers {
	return s.StaticSchemaParams.Resolvers
}

func (s *staticSchema) Dependencies() []ExecutableSchema {
	return s.StaticSchemaParams.Dependencies
}

func mergeExecutableSchemas(existingSchema ExecutableSchema, newSchemas ...ExecutableSchema) (ExecutableSchema, error) {
	mergedSchema := StaticSchemaParams{Resolvers: make(Resolvers)}
	if existingSchema != nil {
		mergedSchema.Name = existingSchema.Name()
		mergedSchema.Schema = existingSchema.Schema()
		mergedSchema.Resolvers = existingSchema.Resolvers()
		mergedSchema.Dependencies = existingSchema.Dependencies()
	}
	for _, newSchema := range newSchemas {
		mergedSchema.Schema += newSchema.Schema() + "\n"
		for name, resolver := range newSchema.Resolvers() {
			switch resolver := resolver.(type) {
			case FieldResolvers:
				existing, alreadyExisted := mergedSchema.Resolvers[name]
				if !alreadyExisted {
					existing = resolver
				}
				existingObject, ok := existing.(FieldResolvers)
				if !ok {
					return nil, fmt.Errorf("unexpected resolver type %T", existing)
				}
				for fieldName, fieldResolveFn := range resolver.Fields() {
					if alreadyExisted {
						// check for field conflicts if we are merging more fields into the existing object
						if _, ok := existingObject.Fields()[fieldName]; ok {
							return nil, fmt.Errorf("conflict on type %q field %q: %w", name, fieldName, ErrMergeFieldConflict)
						}
					}
					existingObject.SetField(fieldName, fieldResolveFn)
				}
				mergedSchema.Resolvers[name] = existingObject
			case ScalarResolver:
				if existing, ok := mergedSchema.Resolvers[name]; ok {
					if _, ok := existing.(ScalarResolver); !ok {
						return nil, fmt.Errorf("conflict on type %q: %w", name, ErrMergeTypeConflict)
					}
					return nil, fmt.Errorf("conflict on type %q: %w", name, ErrMergeScalarConflict)
				}
				mergedSchema.Resolvers[name] = resolver
			default:
				return nil, fmt.Errorf("unexpected resolver type %T", resolver)
			}
		}
	}

	// gqlparser has actual validation and errors, unlike the graphql-go library
	_, err := gqlparser.LoadSchema(&ast.Source{Input: mergedSchema.Schema})
	if err != nil {
		return nil, fmt.Errorf("schema validation failed: %w\n%s", err, mergedSchema.Schema)
	}

	return StaticSchema(mergedSchema), nil
}

func compile(s ExecutableSchema) (*graphql.Schema, error) {
	typeResolvers := tools.ResolverMap{}
	for name, resolver := range s.Resolvers() {
		switch resolver := resolver.(type) {
		case FieldResolvers:
			obj := &tools.ObjectResolver{
				Fields: tools.FieldResolveMap{},
			}
			typeResolvers[name] = obj
			for fieldName, fn := range resolver.Fields() {
				obj.Fields[fieldName] = &tools.FieldResolve{
					Resolve: fn,
				}
			}
		case ScalarResolver:
			typeResolvers[name] = &tools.ScalarResolver{
				Serialize:    resolver.Serialize,
				ParseValue:   resolver.ParseValue,
				ParseLiteral: resolver.ParseLiteral,
			}
		default:
			panic(resolver)
		}
	}

	schema, err := tools.MakeExecutableSchema(tools.ExecutableSchema{
		TypeDefs:  s.Schema(),
		Resolvers: typeResolvers,
		SchemaDirectives: tools.SchemaDirectiveVisitorMap{
			"deprecated": &tools.SchemaDirectiveVisitor{
				VisitFieldDefinition: func(p tools.VisitFieldDefinitionParams) error {
					reason := "No longer supported"
					if r, ok := p.Args["reason"].(string); ok {
						reason = r
					}
					p.Config.DeprecationReason = reason
					return nil
				},
			},
		},
		Extensions: []graphql.Extension{&tracing.GraphQLTracer{}},
	})
	if err != nil {
		return nil, err
	}

	return &schema, nil
}
