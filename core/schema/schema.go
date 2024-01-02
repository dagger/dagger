package schema

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"

	"github.com/containerd/containerd/content"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/tracing"
	"github.com/dagger/graphql"
	tools "github.com/dagger/graphql-go-tools"
	"github.com/dagger/graphql/gqlerrors"
	"github.com/iancoleman/strcase"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"golang.org/x/sync/errgroup"
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

func New(ctx context.Context, params InitializeArgs) (*APIServer, error) {
	svcs := core.NewServices(params.BuildkitClient)
	api := &APIServer{
		bk:           params.BuildkitClient,
		platform:     params.Platform,
		progSockPath: params.ProgSockPath,
		auth:         params.Auth,
		secrets:      params.Secrets,
		ociStore:     params.OCIStore,
		leaseManager: params.LeaseManager,
		services:     svcs,
		host:         core.NewHost(),

		endpoints: map[string]http.Handler{},

		buildCache:  core.NewCacheMap[uint64, *core.Container](),
		importCache: core.NewCacheMap[uint64, *specs.Descriptor](),

		loadModCache:      core.NewCacheMap[digest.Digest, *UserMod](),
		modByDagDigest:    map[digest.Digest]Mod{},
		clientCallContext: map[digest.Digest]*clientCallContext{},
	}

	coreSchema, err := mergeSchemaResolvers(
		&querySchema{api},
		&directorySchema{api, api.host, api.services, api.buildCache},
		&fileSchema{api, api.host, api.services},
		&gitSchema{api, api.services},
		&containerSchema{
			api,
			api.host,
			api.services,
			api.ociStore,
			api.leaseManager,
			api.buildCache,
			api.importCache,
		},
		&cacheSchema{api},
		&secretSchema{api},
		&serviceSchema{api, api.services},
		&hostSchema{api, api.host, api.services},
		&moduleSchema{api},
		&httpSchema{api, api.services},
		&platformSchema{api},
		&socketSchema{api, api.host},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to merge core schema: %w", err)
	}
	api.core = &CoreMod{api: api, compiledSchema: coreSchema}
	api.defaultDeps, err = newModDeps([]Mod{api.core})
	if err != nil {
		return nil, fmt.Errorf("failed to create default deps: %w", err)
	}

	// the main client caller starts out with the core API loaded
	api.clientCallContext[""] = &clientCallContext{
		deps: api.defaultDeps,
	}

	return api, nil
}

type APIServer struct {
	bk           *buildkit.Client
	platform     specs.Platform
	progSockPath string
	auth         *auth.RegistryAuthProvider
	secrets      *core.SecretStore
	ociStore     content.Store
	leaseManager *leaseutil.Manager
	host         *core.Host
	services     *core.Services

	buildCache  *core.CacheMap[uint64, *core.Container]
	importCache *core.CacheMap[uint64, *specs.Descriptor]

	// the http endpoints being served (as a map since APIs like shellEndpoint can add more)
	endpoints  map[string]http.Handler
	endpointMu sync.RWMutex

	// the core API simulated as a module
	core *CoreMod
	// the default deps of every user module (currently just core)
	defaultDeps *ModDeps

	// cache used to de-dupe loading modules from metadata
	loadModCache *core.CacheMap[digest.Digest, *UserMod]

	// map of mod dag digest -> mod
	modByDagDigest   map[digest.Digest]Mod
	modByDagDigestMu sync.RWMutex

	// The metadata of client calls.
	// For the special case of the main client caller, the key is just empty string.
	// This is never explicitly deleted from; instead it will just be garbage collected
	// when this server for the session shuts down
	clientCallContext map[digest.Digest]*clientCallContext
	clientCallMu      sync.RWMutex
}

type clientCallContext struct {
	// the DAG of modules being served to this client
	deps *ModDeps

	// If the client is itself from a function call in a user module, these are set with the
	// metadata of that ongoing function call
	mod    *UserMod
	fnCall *core.FunctionCall
}

func (s *APIServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	errorOut := func(err error, code int) {
		bklog.G(ctx).WithError(err).Error("failed to serve request")
		http.Error(w, err.Error(), code)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		errorOut(err, http.StatusInternalServerError)
		return
	}

	callContext, ok := s.clientCallContext[clientMetadata.ModuleCallerDigest]
	if !ok {
		errorOut(fmt.Errorf("client call %s not found", clientMetadata.ModuleCallerDigest), http.StatusInternalServerError)
		return
	}

	schema, err := callContext.deps.Schema(ctx)
	if err != nil {
		// TODO: technically this is not *always* bad request, should ideally be more specific and differentiate
		errorOut(err, http.StatusBadRequest)
		return
	}

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

	mux := http.NewServeMux()
	mux.Handle("/query", NewHandler(&HandlerConfig{
		Schema: schema.Compiled,
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

func (s *APIServer) ShutdownClient(ctx context.Context, client *engine.ClientMetadata) error {
	return s.services.StopClientServices(ctx, client)
}

func (s *APIServer) MuxEndpoint(ctx context.Context, path string, handler http.Handler) error {
	s.endpointMu.Lock()
	defer s.endpointMu.Unlock()
	s.endpoints[path] = handler
	return nil
}

func (s *APIServer) GetOrAddModFromMetadata(
	ctx context.Context,
	modMeta *core.Module,
	pipeline pipeline.Path,
) (*UserMod, error) {
	dgst, err := modMeta.BaseDigest()
	if err != nil {
		return nil, err
	}
	mod, err := s.loadModCache.GetOrInitialize(ctx, dgst, func(ctx context.Context) (*UserMod, error) {
		var eg errgroup.Group
		deps := make([]Mod, len(modMeta.DependencyConfig))
		for i, depRef := range modMeta.DependencyConfig {
			i, depRef := i, depRef
			eg.Go(func() error {
				mod, err := s.AddModFromRef(ctx, depRef, modMeta, pipeline)
				if err != nil {
					return err
				}
				deps[i] = mod
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return nil, err
		}
		deps = append(deps, s.defaultDeps.mods...)

		sdk, err := s.sdkForModule(ctx, modMeta)
		if err != nil {
			return nil, err
		}

		dag, err := newModDeps(deps)
		if err != nil {
			return nil, err
		}
		mod, err := newUserMod(s, modMeta, dag, sdk)
		if err != nil {
			return nil, err
		}

		s.modByDagDigestMu.Lock()
		defer s.modByDagDigestMu.Unlock()
		s.modByDagDigest[mod.DagDigest()] = mod
		return mod, nil
	})
	if err != nil {
		if errors.Is(err, core.ErrCacheMapRecursiveCall) {
			err = fmt.Errorf("module %s has a circular dependency", modMeta.Name)
		}
		return nil, err
	}
	return mod, nil
}

func (s *APIServer) AddModFromRef(
	ctx context.Context,
	ref string,
	parentMod *core.Module,
	pipeline pipeline.Path,
) (*UserMod, error) {
	modMeta, err := core.ModuleFromRef(
		ctx, s.bk, s.services, pipeline, s.platform,
		parentMod.SourceDirectory, parentMod.SourceDirectorySubpath,
		ref,
	)
	if err != nil {
		return nil, err
	}
	return s.GetOrAddModFromMetadata(ctx, modMeta, pipeline)
}

func (s *APIServer) GetModFromDagDigest(ctx context.Context, dagDgst digest.Digest) (Mod, error) {
	s.modByDagDigestMu.RLock()
	defer s.modByDagDigestMu.RUnlock()
	mod, ok := s.modByDagDigest[dagDgst]
	if !ok {
		return nil, fmt.Errorf("module %s not found", dagDgst)
	}
	return mod, nil
}

func (s *APIServer) ServeModuleToMainClient(ctx context.Context, modMeta *core.Module) error {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}
	if clientMetadata.ModuleCallerDigest != "" {
		return fmt.Errorf("cannot serve module to client %s", clientMetadata.ClientID)
	}

	mod, err := s.GetOrAddModFromMetadata(ctx, modMeta, nil)
	if err != nil {
		return err
	}

	s.clientCallMu.Lock()
	defer s.clientCallMu.Unlock()
	callCtx, ok := s.clientCallContext[""]
	if !ok {
		return fmt.Errorf("client call not found")
	}
	deps := append([]Mod{}, callCtx.deps.mods...)
	deps = append(deps, mod)
	callCtx.deps, err = newModDeps(deps)
	if err != nil {
		return err
	}
	return nil
}

func (s *APIServer) RegisterFunctionCall(dgst digest.Digest, deps *ModDeps, mod *UserMod, call *core.FunctionCall) error {
	if dgst == "" {
		return fmt.Errorf("cannot register function call with empty digest")
	}

	s.clientCallMu.Lock()
	defer s.clientCallMu.Unlock()
	_, ok := s.clientCallContext[dgst]
	if ok {
		return nil
	}
	s.clientCallContext[dgst] = &clientCallContext{
		deps:   deps,
		mod:    mod,
		fnCall: call,
	}
	return nil
}

func (s *APIServer) CurrentModule(ctx context.Context) (*UserMod, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if clientMetadata.ModuleCallerDigest == "" {
		return nil, fmt.Errorf("no current module for main client caller")
	}

	s.clientCallMu.RLock()
	defer s.clientCallMu.RUnlock()
	callCtx, ok := s.clientCallContext[clientMetadata.ModuleCallerDigest]
	if !ok {
		return nil, fmt.Errorf("client call %s not found", clientMetadata.ModuleCallerDigest)
	}
	return callCtx.mod, nil
}

func (s *APIServer) CurrentFunctionCall(ctx context.Context) (*core.FunctionCall, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if clientMetadata.ModuleCallerDigest == "" {
		return nil, fmt.Errorf("no current function call for main client caller")
	}

	s.clientCallMu.RLock()
	defer s.clientCallMu.RUnlock()
	callCtx, ok := s.clientCallContext[clientMetadata.ModuleCallerDigest]
	if !ok {
		return nil, fmt.Errorf("client call %s not found", clientMetadata.ModuleCallerDigest)
	}

	return callCtx.fnCall, nil
}

func (s *APIServer) CurrentServedDeps(ctx context.Context) (*ModDeps, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, ok := s.clientCallContext[clientMetadata.ModuleCallerDigest]
	if !ok {
		return nil, fmt.Errorf("client call %s not found", clientMetadata.ModuleCallerDigest)
	}
	return callCtx.deps, nil
}

type CompiledSchema struct {
	SchemaResolvers
	Compiled *graphql.Schema
}

type SchemaResolvers interface {
	Schema() string
	Resolvers() Resolvers
}

type StaticSchemaParams struct {
	Name      string
	Schema    string
	Resolvers Resolvers
}

func StaticSchema(p StaticSchemaParams) SchemaResolvers {
	return &staticSchema{p}
}

var _ SchemaResolvers = &staticSchema{}

type staticSchema struct {
	StaticSchemaParams
}

func (s *staticSchema) Schema() string {
	return s.StaticSchemaParams.Schema
}

func (s *staticSchema) Resolvers() Resolvers {
	return s.StaticSchemaParams.Resolvers
}

func mergeSchemaResolvers(newSchemas ...SchemaResolvers) (*CompiledSchema, error) {
	mergedSchema := StaticSchemaParams{Resolvers: make(Resolvers)}
	for _, newSchema := range newSchemas {
		mergedSchema.Schema += newSchema.Schema() + "\n"
		for typeName, newResolver := range newSchema.Resolvers() {
			switch newResolver := newResolver.(type) {
			case FieldResolvers:
				existingResolver, typeResolverAlreadyExisted := mergedSchema.Resolvers[typeName]
				if !typeResolverAlreadyExisted {
					existingResolver = newResolver.Clone()
				}
				existingObject, ok := existingResolver.(FieldResolvers)
				if !ok {
					return nil, fmt.Errorf("unexpected resolver type %T", existingResolver)
				}
				for fieldName, fieldResolveFn := range newResolver.Fields() {
					if typeResolverAlreadyExisted {
						// check for field conflicts if we are merging more fields into the existing object
						if _, ok := existingObject.Fields()[fieldName]; ok {
							return nil, fmt.Errorf("conflict on type %q field %q: %w", typeName, fieldName, ErrMergeFieldConflict)
						}
					}
					existingObject.SetField(fieldName, fieldResolveFn)
				}
				mergedSchema.Resolvers[typeName] = existingObject
			case ScalarResolver:
				if existing, ok := mergedSchema.Resolvers[typeName]; ok {
					if _, ok := existing.(ScalarResolver); !ok {
						return nil, fmt.Errorf("conflict on type %q: %w", typeName, ErrMergeTypeConflict)
					}
					return nil, fmt.Errorf("conflict on type %q: %w", typeName, ErrMergeScalarConflict)
				}
				mergedSchema.Resolvers[typeName] = newResolver
			default:
				return nil, fmt.Errorf("unexpected resolver type %T", newResolver)
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

	schemaResolvers := StaticSchema(mergedSchema)
	compiled, err := compile(schemaResolvers)
	if err != nil {
		return nil, err
	}

	return &CompiledSchema{
		SchemaResolvers: schemaResolvers,
		Compiled:        compiled,
	}, nil
}

func compile(s SchemaResolvers) (*graphql.Schema, error) {
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

func schemaIntrospectionJSON(ctx context.Context, compiledSchema graphql.Schema) (string, error) {
	result := graphql.Do(graphql.Params{
		Schema:        compiledSchema,
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

/*
This formats comments in the schema as:
"""
comment
"""

Which avoids corner cases where the comment ends in a `"`.
*/
func formatGqlDescription(desc string, args ...any) string {
	if desc == "" {
		return ""
	}
	return "\n" + strings.TrimSpace(fmt.Sprintf(desc, args...)) + "\n"
}

func typeDefToASTType(typeDef *core.TypeDef, isInput bool) (*ast.Type, error) {
	switch typeDef.Kind {
	case core.TypeDefKindString:
		return &ast.Type{
			NamedType: "String",
			NonNull:   !typeDef.Optional,
		}, nil
	case core.TypeDefKindInteger:
		return &ast.Type{
			NamedType: "Int",
			NonNull:   !typeDef.Optional,
		}, nil
	case core.TypeDefKindBoolean:
		return &ast.Type{
			NamedType: "Boolean",
			NonNull:   !typeDef.Optional,
		}, nil
	case core.TypeDefKindVoid:
		return &ast.Type{
			NamedType: "Void",
			NonNull:   !typeDef.Optional,
		}, nil
	case core.TypeDefKindList:
		if typeDef.AsList == nil {
			return nil, fmt.Errorf("expected list type def, got nil")
		}
		astType, err := typeDefToASTType(typeDef.AsList.ElementTypeDef, isInput)
		if err != nil {
			return nil, err
		}
		return &ast.Type{
			Elem:    astType,
			NonNull: !typeDef.Optional,
		}, nil
	case core.TypeDefKindObject:
		if typeDef.AsObject == nil {
			return nil, fmt.Errorf("expected object type def, got nil")
		}
		objTypeDef := typeDef.AsObject
		objName := gqlObjectName(objTypeDef.Name)
		if isInput {
			// idable types use their ID scalar as the input value
			return &ast.Type{NamedType: objName + "ID", NonNull: !typeDef.Optional}, nil
		}
		return &ast.Type{NamedType: objName, NonNull: !typeDef.Optional}, nil
	case core.TypeDefKindInterface:
		if typeDef.AsInterface == nil {
			return nil, fmt.Errorf("expected interface type def, got nil")
		}
		ifaceTypeDef := typeDef.AsInterface
		ifaceName := gqlObjectName(ifaceTypeDef.Name)
		if isInput {
			// idable types use their ID scalar as the input value
			return &ast.Type{NamedType: ifaceName + "ID", NonNull: !typeDef.Optional}, nil
		}
		return &ast.Type{NamedType: ifaceName, NonNull: !typeDef.Optional}, nil
	default:
		return nil, fmt.Errorf("unsupported type kind %q", typeDef.Kind)
	}
}

// relevant ast code we need to work with here:
// https://github.com/vektah/gqlparser/blob/35199fce1fa1b73c27f23c84f4430f47ac93329e/ast/value.go#L44
func astDefaultValue(typeDef *core.TypeDef, val any) (*ast.Value, error) {
	if val == nil {
		// no default value for this arg
		return nil, nil
	}
	switch typeDef.Kind {
	case core.TypeDefKindString:
		strVal, ok := val.(string)
		if !ok {
			return nil, fmt.Errorf("expected string default value, got %T", val)
		}
		return &ast.Value{
			Kind: ast.StringValue,
			Raw:  strVal,
		}, nil
	case core.TypeDefKindInteger:
		var intVal int
		switch val := val.(type) {
		case int:
			intVal = val
		case float64: // JSON unmarshaling to `any'
			intVal = int(val)
		default:
			return nil, fmt.Errorf("expected integer default value, got %T", val)
		}
		return &ast.Value{
			Kind: ast.IntValue,
			Raw:  strconv.Itoa(intVal),
		}, nil
	case core.TypeDefKindBoolean:
		boolVal, ok := val.(bool)
		if !ok {
			return nil, fmt.Errorf("expected bool default value, got %T", val)
		}
		return &ast.Value{
			Kind: ast.BooleanValue,
			Raw:  strconv.FormatBool(boolVal),
		}, nil
	case core.TypeDefKindVoid:
		if val != nil {
			return nil, fmt.Errorf("expected nil value, got %T", val)
		}
		return &ast.Value{
			Kind: ast.NullValue,
			Raw:  "null",
		}, nil
	case core.TypeDefKindList:
		astVal := &ast.Value{Kind: ast.ListValue}
		// val is coming from deserializing a json string (see jsonResolver), so it should be []any
		listVal, ok := val.([]any)
		if !ok {
			return nil, fmt.Errorf("expected list default value, got %T", val)
		}
		for _, elemVal := range listVal {
			elemASTVal, err := astDefaultValue(typeDef.AsList.ElementTypeDef, elemVal)
			if err != nil {
				return nil, fmt.Errorf("failed to get default value for list element: %w", err)
			}
			astVal.Children = append(astVal.Children, &ast.ChildValue{
				Value: elemASTVal,
			})
		}
		return astVal, nil
	case core.TypeDefKindObject:
		astVal := &ast.Value{Kind: ast.ObjectValue}
		// val is coming from deserializing a json string (see jsonResolver), so it should be map[string]any
		mapVal, ok := val.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("expected object default value, got %T", val)
		}
		for name, val := range mapVal {
			name = gqlFieldName(name)
			field, ok := typeDef.AsObject.FieldByName(name)
			if !ok {
				return nil, fmt.Errorf("object field %s.%s not found", typeDef.AsObject.Name, name)
			}
			fieldASTVal, err := astDefaultValue(field.TypeDef, val)
			if err != nil {
				return nil, fmt.Errorf("failed to get default value for object field %q: %w", name, err)
			}
			astVal.Children = append(astVal.Children, &ast.ChildValue{
				Name:  name,
				Value: fieldASTVal,
			})
		}
		return astVal, nil
	default:
		return nil, fmt.Errorf("unsupported type kind %q", typeDef.Kind)
	}
}

func gqlObjectName(name string) string {
	// gql object name is capitalized camel case
	return strcase.ToCamel(name)
}

func namespaceObject(objName, namespace string) string {
	gqlObjName := gqlObjectName(objName)
	if rest := strings.TrimPrefix(gqlObjName, gqlObjectName(namespace)); rest != gqlObjName {
		if len(rest) == 0 {
			// objName equals namespace, don't namespace this
			return gqlObjName
		}
		// we have this case check here to check for a boundary
		// e.g. if objName="Postman" and namespace="Post", then we should still namespace
		// this to "PostPostman" instead of just going for "Postman" (but we should do that
		// if objName="PostMan")
		if 'A' <= rest[0] && rest[0] <= 'Z' {
			// objName has namespace prefixed, don't namespace this
			return gqlObjName
		}
	}

	return gqlObjectName(namespace + "_" + objName)
}

func gqlFieldName(name string) string {
	// gql field name is uncapitalized camel case
	return strcase.ToLowerCamel(name)
}

func gqlArgName(name string) string {
	// gql arg name is uncapitalized camel case
	return strcase.ToLowerCamel(name)
}
