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
	"github.com/opencontainers/go-digest"
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
		bk:           params.BuildkitClient,
		platform:     params.Platform,
		progSockPath: params.ProgSockPath,
		auth:         params.Auth,
		secrets:      params.Secrets,
		ociStore:     params.OCIStore,
		leaseManager: params.LeaseManager,
		host:         core.NewHost(),

		buildCache:        core.NewCacheMap[uint64, *core.Container](),
		importCache:       core.NewCacheMap[uint64, *specs.Descriptor](),
		envCache:          core.NewEnvironmentCache(),
		installedEnvCache: core.NewCacheMap[uint64, *core.Environment](),

		envSchemas: map[digest.Digest]*envSchema{},
	}
	return merged, nil
}

type MergedSchemas struct {
	bk           *buildkit.Client
	platform     specs.Platform
	progSockPath string
	auth         *auth.RegistryAuthProvider
	secrets      *core.SecretStore
	ociStore     content.Store
	leaseManager *leaseutil.Manager
	host         *core.Host

	buildCache        *core.CacheMap[uint64, *core.Container]
	importCache       *core.CacheMap[uint64, *specs.Descriptor]
	envCache          *core.EnvironmentCache
	installedEnvCache *core.CacheMap[uint64, *core.Environment]

	mu sync.RWMutex
	// map of env digest -> schema presented to env
	// for the original client not in an env, digest is just ""
	envSchemas map[digest.Digest]*envSchema
}

// requires s.mu write lock held
func (s *MergedSchemas) initializeEnvSchema(envDigest digest.Digest) (*envSchema, error) {
	es := &envSchema{
		separateSchemas: map[string]ExecutableSchema{},
	}

	err := es.addSchemas(
		&querySchema{s},
		&directorySchema{s, s.host, s.buildCache},
		&fileSchema{s, s.host},
		&gitSchema{s},
		&containerSchema{
			s,
			s.host,
			s.ociStore,
			s.leaseManager,
			s.buildCache,
			s.importCache,
		},
		&cacheSchema{s},
		&secretSchema{s},
		&hostSchema{s, s.host},
		&environmentSchema{
			MergedSchemas:     s,
			envCache:          s.envCache,
			installedEnvCache: s.installedEnvCache,
		},
		&httpSchema{s},
		&platformSchema{s},
		&socketSchema{s, s.host},
	)
	if err != nil {
		return nil, err
	}

	s.envSchemas[envDigest] = es
	return es, nil
}

func (s *MergedSchemas) getEnvSchema(envDigest digest.Digest) (*envSchema, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	es, ok := s.envSchemas[envDigest]
	if !ok {
		var err error
		es, err = s.initializeEnvSchema(envDigest)
		if err != nil {
			return nil, err
		}
	}
	return es, nil
}

func (s *MergedSchemas) Schema(envDigest digest.Digest) (*graphql.Schema, error) {
	es, err := s.getEnvSchema(envDigest)
	if err != nil {
		return nil, err
	}
	return es.schema(), nil
}

func (s *MergedSchemas) addSchemas(envDigest digest.Digest, schemasToAdd ...ExecutableSchema) error {
	es, err := s.getEnvSchema(envDigest)
	if err != nil {
		return err
	}
	return es.addSchemas(schemasToAdd...)
}

func (s *MergedSchemas) resolvers(envDigest digest.Digest) (Resolvers, error) {
	es, err := s.getEnvSchema(envDigest)
	if err != nil {
		return nil, err
	}
	return es.resolvers(), nil
}

type envSchema struct {
	mu              sync.RWMutex
	digest          digest.Digest
	separateSchemas map[string]ExecutableSchema
	mergedSchema    ExecutableSchema
	compiledSchema  *graphql.Schema
}

func (s *envSchema) schema() *graphql.Schema {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.compiledSchema
}

func (s *envSchema) addSchemas(schemasToAdd ...ExecutableSchema) error {
	s.mu.Lock()
	defer s.mu.Unlock()

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

func (s *envSchema) resolvers() Resolvers {
	s.mu.Lock()
	defer s.mu.Unlock()
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
