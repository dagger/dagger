package schema

import (
	"context"
	"fmt"
	"net/http"
	"runtime/debug"
	"sort"
	"sync"

	"github.com/containerd/containerd/content"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/tracing"
	"github.com/dagger/graphql"
	tools "github.com/dagger/graphql-go-tools"
	"github.com/moby/buildkit/util/bklog"
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
	svcs := core.NewServices(params.BuildkitClient)
	merged := &MergedSchemas{
		bk:           params.BuildkitClient,
		platform:     params.Platform,
		progSockPath: params.ProgSockPath,
		auth:         params.Auth,
		secrets:      params.Secrets,
		ociStore:     params.OCIStore,
		leaseManager: params.LeaseManager,
		services:     svcs,
		host:         core.NewHost(),

		buildCache:           core.NewCacheMap[uint64, *core.Container](),
		importCache:          core.NewCacheMap[uint64, *specs.Descriptor](),
		functionContextCache: NewFunctionContextCache(),
		moduleCache:          core.NewCacheMap[digest.Digest, *core.Module](),

		moduleSchemaViews: map[digest.Digest]*moduleSchemaView{},
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
	services     *core.Services

	buildCache           *core.CacheMap[uint64, *core.Container]
	importCache          *core.CacheMap[uint64, *specs.Descriptor]
	functionContextCache *FunctionContextCache
	moduleCache          *core.CacheMap[digest.Digest, *core.Module]

	mu sync.RWMutex
	// Map of module digest -> schema presented to module.
	// For the original client not in an module, digest is just "".
	moduleSchemaViews map[digest.Digest]*moduleSchemaView
}

// requires s.mu write lock held
func (s *MergedSchemas) initializeModuleSchema(moduleDigest digest.Digest) (*moduleSchemaView, error) {
	ms := &moduleSchemaView{
		separateSchemas: map[string]ExecutableSchema{},
		endpoints:       map[string]http.Handler{},
		services:        s.services,
	}

	err := ms.addSchemas(
		&querySchema{s},
		&directorySchema{s, s.host, s.services, s.buildCache},
		&fileSchema{s, s.host, s.services},
		&gitSchema{s, s.services},
		&containerSchema{
			s,
			s.host,
			s.services,
			s.ociStore,
			s.leaseManager,
			s.buildCache,
			s.importCache,
		},
		&cacheSchema{s},
		&secretSchema{s},
		&hostSchema{s, s.host, s.services},
		&moduleSchema{
			MergedSchemas:        s,
			currentSchemaView:    ms,
			functionContextCache: s.functionContextCache,
			moduleCache:          s.moduleCache,
		},
		&httpSchema{s, s.services},
		&platformSchema{s},
		&socketSchema{s, s.host},
	)
	if err != nil {
		return nil, err
	}

	s.moduleSchemaViews[moduleDigest] = ms
	return ms, nil
}

func (s *MergedSchemas) getModuleSchemaView(moduleDigest digest.Digest) (*moduleSchemaView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ms, ok := s.moduleSchemaViews[moduleDigest]
	if !ok {
		var err error
		ms, err = s.initializeModuleSchema(moduleDigest)
		if err != nil {
			return nil, err
		}
	}
	return ms, nil
}

func (s *MergedSchemas) Schema(moduleDigest digest.Digest) (*graphql.Schema, error) {
	ms, err := s.getModuleSchemaView(moduleDigest)
	if err != nil {
		return nil, err
	}
	return ms.schema(), nil
}

func (s *MergedSchemas) MuxEndpoint(path string, handler http.Handler, moduleDigest digest.Digest) error {
	ms, err := s.getModuleSchemaView(moduleDigest)
	if err != nil {
		return err
	}
	ms.muxEndpoint(path, handler)
	return nil
}

func (s *MergedSchemas) HTTPHandler(moduleDigest digest.Digest) (http.Handler, error) {
	return s.getModuleSchemaView(moduleDigest)
}

type moduleSchemaView struct {
	mu              sync.RWMutex
	separateSchemas map[string]ExecutableSchema
	mergedSchema    ExecutableSchema
	compiledSchema  *graphql.Schema
	endpointMu      sync.RWMutex
	endpoints       map[string]http.Handler
	services        *core.Services
}

func (s *moduleSchemaView) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if v := recover(); v != nil {
			bklog.G(context.TODO()).Errorf("panic serving schema: %v %s", v, string(debug.Stack()))

			// TODO: this doesn't play nice with hijacked conns, need to fix
			/*
				msg := "Internal Server Error"
				code := http.StatusInternalServerError
				switch v := v.(type) {
				case error:
					msg = v.Error()
					if errors.As(v, &InvalidInputError{}) {
						// panics can happen on invalid input in scalar serde
						code = http.StatusBadRequest
					}
				case string:
					msg = v
				}
				res := graphql.Result{
					Errors: []gqlerrors.FormattedError{
						gqlerrors.NewFormattedError(msg),
					},
				}
				bytes, err := json.Marshal(res)
				if err != nil {
					panic(err)
				}
				http.Error(w, string(bytes), code)
			*/
		}
	}()

	clientMetadata, err := engine.ClientMetadataFromContext(r.Context())
	if err != nil {
		bklog.G(context.TODO()).WithError(err).Error("failed to get client metadata")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	mux := http.NewServeMux()
	mux.Handle("/query", NewHandler(&HandlerConfig{
		Schema: s.schema(),
	}))
	mux.Handle("/shutdown", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		bklog.G(ctx).Debugf("shutting down client %s", clientMetadata.ClientID)
		if err := s.services.StopClientServices(ctx, clientMetadata); err != nil {
			bklog.G(ctx).WithError(err).Error("failed to shutdown")
		}
	}))

	s.endpointMu.RLock()
	for path, handler := range s.endpoints {
		mux.Handle(path, handler)
	}
	s.endpointMu.RUnlock()

	mux.ServeHTTP(w, r)
}

func (s *moduleSchemaView) muxEndpoint(path string, handler http.Handler) {
	s.endpointMu.Lock()
	defer s.endpointMu.Unlock()
	// TODO:
	bklog.G(context.TODO()).Debugf("registering endpoint %s", path)
	s.endpoints[path] = handler
}

func (s *moduleSchemaView) schema() *graphql.Schema {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.compiledSchema
}

func (s *MergedSchemas) ShutdownClient(ctx context.Context, client *engine.ClientMetadata) error {
	return s.services.StopClientServices(ctx, client)
}

func (s *moduleSchemaView) addSchemas(schemasToAdd ...ExecutableSchema) error {
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

func (s *moduleSchemaView) resolvers() Resolvers {
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
