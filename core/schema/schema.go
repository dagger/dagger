package schema

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"sort"
	"strings"
	"sync"

	"github.com/containerd/containerd/content"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/idproto"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/tracing"
	"github.com/dagger/graphql"
	tools "github.com/dagger/graphql-go-tools"
	"github.com/dagger/graphql/gqlerrors"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// NOTE: core is not *technically* a module (yet) but we treat it as one when checking
// for where a given type was loaded from
const coreModuleName = "core"

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

		moduleCache:       core.NewCacheMap[digest.Digest, *core.Module](),
		dependenciesCache: core.NewCacheMap[digest.Digest, []*core.Module](),

		queryCache: core.NewCacheMap[digest.Digest, any](),

		schemaViews:    map[digest.Digest]*schemaView{},
		moduleContexts: map[digest.Digest]*moduleContext{},
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

	moduleCache       *core.CacheMap[digest.Digest, *core.Module]
	dependenciesCache *core.CacheMap[digest.Digest, []*core.Module]

	// TODO(vito): theoretically this replaces most of above?
	queryCache *core.CacheMap[digest.Digest, any]

	mu sync.RWMutex
	// Map of module digest -> schema presented to module.
	// For the original client not in an module, digest is just "".
	schemaViews map[digest.Digest]*schemaView
	// map of module contexts, used to store metadata about the module making api requests
	// to this server. Needs to be separate from schemaViews because there can be multiple
	// module contexts for a single schema view.
	moduleContexts map[digest.Digest]*moduleContext
}

type moduleContext struct {
	module     *core.Module
	fnCall     *core.FunctionCall
	schemaView *schemaView
}

// requires s.mu write lock held
func (s *MergedSchemas) initializeSchemaView(viewDigest digest.Digest) (*schemaView, error) {
	ms := &schemaView{
		viewDigest:      viewDigest,
		separateSchemas: map[string]ExecutableSchema{},
		endpoints:       map[string]http.Handler{},
		services:        s.services,
	}

	err := ms.addSchemas(
		&querySchema{s},
		&directorySchema{s, s.host, s.services},
		&fileSchema{s, s.host, s.services},
		&gitSchema{s, s.services},
		&containerSchema{
			s,
			s.host,
			s.services,
			s.ociStore,
			s.leaseManager,
		},
		&cacheSchema{s},
		&secretSchema{s},
		&serviceSchema{s, s.services},
		&hostSchema{s, s.host, s.services},
		&moduleSchema{
			MergedSchemas:     s,
			moduleCache:       s.moduleCache,
			dependenciesCache: s.dependenciesCache,
		},
		&httpSchema{s, s.services},
		&platformSchema{s},
		&socketSchema{s, s.host},
	)
	if err != nil {
		return nil, err
	}

	s.schemaViews[viewDigest] = ms
	return ms, nil
}

func load[T any](ctx context.Context, id *resourceid.ID[T], ms *MergedSchemas) (T, error) {
	var zero T
	view, err := ms.currentSchemaView(ctx)
	if err != nil {
		return zero, err
	}
	return id.Resolve(ms.queryCache, view.compiledSchema)
}

func (s *MergedSchemas) getSchemaView(viewDigest digest.Digest) (*schemaView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ms, ok := s.schemaViews[viewDigest]
	if !ok {
		var err error
		ms, err = s.initializeSchemaView(viewDigest)
		if err != nil {
			return nil, err
		}
	}
	return ms, nil
}

func (s *MergedSchemas) getModuleSchemaView(mod *core.Module) (*schemaView, error) {
	modDgst, err := mod.ID().Digest()
	if err != nil {
		return nil, fmt.Errorf("failed to compute schema view digest: %w", err)
	}
	return s.getSchemaView(modDgst)
}

func (s *MergedSchemas) registerModuleFunctionCall(mod *core.Module, fnCall *core.FunctionCall) (*schemaView, digest.Digest, error) {
	schemaView, err := s.getModuleSchemaView(mod)
	if err != nil {
		return nil, "", err
	}

	fnCallDgst, err := fnCall.ID().Digest()
	if err != nil {
		return nil, "", err
	}

	dgst := digest.FromString(schemaView.viewDigest.String() + "." + fnCallDgst.String())
	s.mu.Lock()
	defer s.mu.Unlock()

	s.moduleContexts[dgst] = &moduleContext{
		module:     mod,
		fnCall:     fnCall,
		schemaView: schemaView,
	}

	return schemaView, dgst, nil
}

func (s *MergedSchemas) currentSchemaView(ctx context.Context) (*schemaView, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	if clientMetadata.ModuleContextDigest == "" {
		return s.getSchemaView("")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	moduleContext, ok := s.moduleContexts[clientMetadata.ModuleContextDigest]
	if !ok {
		return nil, fmt.Errorf("module context not found")
	}
	return moduleContext.schemaView, nil
}

func (s *MergedSchemas) currentModule(ctx context.Context) (*core.Module, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if clientMetadata.ModuleContextDigest == "" {
		return nil, fmt.Errorf("not in a module")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	moduleContext, ok := s.moduleContexts[clientMetadata.ModuleContextDigest]
	if !ok {
		return nil, fmt.Errorf("module context not found")
	}
	return moduleContext.module, nil
}

func (s *MergedSchemas) currentFunctionCall(ctx context.Context) (*core.FunctionCall, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if clientMetadata.ModuleContextDigest == "" {
		return nil, fmt.Errorf("not in a module")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	moduleContext, ok := s.moduleContexts[clientMetadata.ModuleContextDigest]
	if !ok {
		return nil, fmt.Errorf("module context not found")
	}
	return moduleContext.fnCall, nil
}

func (s *MergedSchemas) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	schemaView, err := s.currentSchemaView(r.Context())
	if err != nil {
		bklog.G(r.Context()).WithError(err).Error("failed to get schema view")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	schemaView.ServeHTTP(w, r)
}

func (s *MergedSchemas) MuxEndpoint(ctx context.Context, path string, handler http.Handler) error {
	schemaView, err := s.currentSchemaView(ctx)
	if err != nil {
		return err
	}
	schemaView.muxEndpoint(path, handler)
	return nil
}

type schemaView struct {
	viewDigest digest.Digest

	mu              sync.RWMutex
	separateSchemas map[string]ExecutableSchema
	mergedSchema    ExecutableSchema
	compiledSchema  *graphql.Schema
	endpointMu      sync.RWMutex
	endpoints       map[string]http.Handler
	services        *core.Services
}

func (s *MergedSchemas) ShutdownClient(ctx context.Context, client *engine.ClientMetadata) error {
	return s.services.StopClientServices(ctx, client)
}

func loader[T any](cache *core.CacheMap[digest.Digest, any]) func(*idproto.ID) (T, error) {
	return func(id *idproto.ID) (T, error) {
		var zero T
		dig, err := id.Digest()
		if err != nil {
			return zero, err
		}
		val, err := cache.Get(dig)
		if err != nil {
			return zero, err
		}
		t, ok := val.(T)
		if !ok {
			return zero, fmt.Errorf("ID refers to a %T, not a %T", val, t)
		}
		return t, nil
	}
}

func (s *schemaView) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if v := recover(); v != nil {
			bklog.G(context.TODO()).Errorf("panic serving schema: %v %s", v, string(debug.Stack()))

			// check whether this is a hijacked connection, if so we can't write any http errors to it
			_, err := w.Write(nil)
			if err == http.ErrHijacked {
				return
			}

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
		RootObjectFn: func(ctx context.Context, r *http.Request) any {
			q := &core.Query{}
			q.SetID(idproto.New("Query"))
			return q
		},
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

func (s *schemaView) muxEndpoint(path string, handler http.Handler) {
	s.endpointMu.Lock()
	defer s.endpointMu.Unlock()
	s.endpoints[path] = handler
}

func (s *schemaView) schema() *graphql.Schema {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.compiledSchema
}

func (s *schemaView) schemaIntrospectionJSON(ctx context.Context) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.compiledSchema == nil {
		return "", errors.New("schema not initialized")
	}

	result := graphql.Do(graphql.Params{
		Schema:        *s.compiledSchema,
		RequestString: introspection.Query,
		OperationName: "IntrospectionQuery",
		Context:       ctx,
	})
	if result.HasErrors() {
		var err error
		for _, e := range result.Errors {
			err = errors.Join(err, e)
		}
		return "", fmt.Errorf("introspection query failed: %w", err)
	}
	jsonBytes, err := json.Marshal(result.Data)
	if err != nil {
		return "", fmt.Errorf("failed to marshal introspection result: %w", err)
	}
	return string(jsonBytes), nil
}

func (s *schemaView) addSchemas(schemasToAdd ...ExecutableSchema) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// make a copy of the current schemas
	separateSchemas := map[string]ExecutableSchema{}
	for k, v := range s.separateSchemas {
		separateSchemas[k] = v
	}

	// add in new schemas, recursively adding dependencies
	newSchemas := make([]ExecutableSchema, 0, len(schemasToAdd))
	for _, newSchema := range schemasToAdd {
		// Skip adding schema if it has already been added, higher callers
		// are expected to handle checks that schemas with the same name are
		// actually equivalent
		_, ok := separateSchemas[newSchema.Name()]
		if ok {
			continue
		}

		newSchemas = append(newSchemas, newSchema)
		separateSchemas[newSchema.Name()] = newSchema
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

func (s *schemaView) resolvers() Resolvers {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mergedSchema.Resolvers()
}

func (s *schemaView) sourceModuleName(astType *ast.Type) (string, bool) {
	if astType == nil {
		return "", false
	}
	if astType.Elem != nil {
		return s.sourceModuleName(astType.Elem)
	}

	typeName := astType.NamedType

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, schema := range s.separateSchemas {
		_, ok := schema.Resolvers()[typeName]
		if !ok {
			continue
		}
		return schema.SourceModuleName(), true
	}
	return "", false
}

type ExecutableSchema interface {
	Name() string
	SourceModuleName() string
	Schema() string
	Resolvers() Resolvers
}

type StaticSchemaParams struct {
	Name             string
	SourceModuleName string
	Schema           string
	Resolvers        Resolvers
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

func (s *staticSchema) SourceModuleName() string {
	return s.StaticSchemaParams.SourceModuleName
}

func (s *staticSchema) Schema() string {
	return s.StaticSchemaParams.Schema
}

func (s *staticSchema) Resolvers() Resolvers {
	return s.StaticSchemaParams.Resolvers
}

func mergeExecutableSchemas(existingSchema ExecutableSchema, newSchemas ...ExecutableSchema) (ExecutableSchema, error) {
	mergedSchema := StaticSchemaParams{Resolvers: make(Resolvers)}
	if existingSchema != nil {
		mergedSchema.Name = existingSchema.Name()
		mergedSchema.Schema = existingSchema.Schema()
		mergedSchema.Resolvers = existingSchema.Resolvers()
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
		var sourceContext string

		var gqlError *gqlerror.Error
		if errors.As(err, &gqlError) && len(gqlError.Locations) >= 1 {
			line := gqlError.Locations[0].Line
			sourceContext = getSourceContext(mergedSchema.Schema, line, 3)
		} else {
			sourceContext = getSourceContext(mergedSchema.Schema, 0, -1)
		}

		return nil, fmt.Errorf("schema validation failed: %w\n\n%s", err, sourceContext)
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

// getSourceContext is a little helper to extract a target line with a number
// of lines of surrounding context. If surrounding is negative, then all the
// lines will be returned.
func getSourceContext(input string, target int, surrounding int) string {
	removeLines := surrounding > 0

	output := strings.Builder{}
	scanner := bufio.NewScanner(strings.NewReader(input))

	padding := len(fmt.Sprint(target + surrounding))

	count := 0
	if removeLines && target-surrounding > 1 {
		output.WriteString(fmt.Sprintf(" %*s | ...\n", padding, ""))
	}
	for scanner.Scan() {
		count += 1
		if removeLines && (count < target-surrounding || count > target+surrounding) {
			continue
		}
		output.WriteString(fmt.Sprintf(" %*d | ", padding, count))
		output.WriteString(scanner.Text())
		output.WriteString("\n")
	}
	if removeLines && target+surrounding < count {
		output.WriteString(fmt.Sprintf(" %*s | ...\n", padding, ""))
	}
	return output.String()
}
