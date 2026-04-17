package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/errcode"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/dagger/dagger/engine"
	"github.com/iancoleman/strcase"
	"github.com/opencontainers/go-digest"
	"github.com/sourcegraph/conc/pool"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"
	"github.com/vektah/gqlparser/v2/validator/rules"
	"github.com/zeebo/xxh3"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/dagger/dagger/util/sortutil"
)

// Server represents a GraphQL server whose schema is dynamically modified at
// runtime.
type Server struct {
	root       AnyObjectResult
	telemetry  AroundFunc
	objects    map[string]ObjectType
	scalars    map[string]ScalarType
	typeDefs   map[string]TypeDef
	directives map[string]DirectiveSpec

	schemas       map[call.View]*ast.Schema
	schemaDigests map[call.View]digest.Digest
	schemaOnces   map[call.View]*sync.Once
	schemaLock    *sync.Mutex

	installLock  *sync.RWMutex
	installHooks []InstallHook

	// View is the default view that is applied to queries on this server.
	//
	// WARNING: this is *not* the view of the current query (for that, inspect
	// the current id)
	View call.View

	// canonical, if set, is the server without entrypoint sugar.
	// Entrypoint proxies flatten a module's methods onto the Query root
	// as syntactic convenience; the canonical server preserves the real
	// namespace where constructors and core fields live unshadowed.
	//
	// Load, LoadType, and callers that need to bypass proxies (proxy
	// resolvers, SDK plumbing) use Canonical() to reach it.
	canonical *Server
}

func (s *Server) Canonical() *Server {
	if s.canonical != nil {
		return s.canonical
	}
	return s
}

func (s *Server) SetCanonical(canonical *Server) {
	s.canonical = canonical
}

type InstallHook interface {
	InstallObject(ObjectType, ...*ast.Directive)
	// FIXME: add support for other install functions
}

// InstallHookForker is implemented by install hooks that carry server-specific
// state and must be rebound when a server is forked.
type InstallHookForker interface {
	ForkInstallHook(*Server) InstallHook
}

// AroundFunc is a function that is called around every non-cached selection.
//
// It's a little funny looking. I may have goofed it. This will be cleaned up
// soon.
type AroundFunc func(
	context.Context,
	*CallRequest,
) (context.Context, func(res AnyResult, cached bool, err *error))

// TypeDef is a type whose sole practical purpose is to define a GraphQL type,
// so it explicitly includes the Definitive interface.
type TypeDef interface {
	Type
	Definitive
}

// NewServer returns a new Server with the given root object.
func NewServer[T Typed](_ context.Context, root T) (*Server, error) {
	srv := newBlankServer()
	rootClass := NewClass(srv, ClassOpts[T]{})
	rootRes := ObjectResult[T]{
		Result: newDetachedResult(nil, root),
		class:  rootClass,
	}
	srv.root = rootRes
	srv.InstallObject(rootClass)
	for _, scalar := range coreScalars {
		srv.InstallScalar(scalar)
	}
	for _, directive := range coreDirectives {
		srv.InstallDirective(directive)
	}
	return srv, nil
}

func newBlankServer() *Server {
	return &Server{
		objects:       map[string]ObjectType{},
		scalars:       map[string]ScalarType{},
		typeDefs:      map[string]TypeDef{},
		directives:    map[string]DirectiveSpec{},
		installLock:   &sync.RWMutex{},
		schemas:       make(map[call.View]*ast.Schema),
		schemaDigests: make(map[call.View]digest.Digest),
		schemaOnces:   make(map[call.View]*sync.Once),
		schemaLock:    &sync.Mutex{},
	}
}

// Fork returns a new server that starts with a clone of the current server's
// installed schema state but with an independent root object and independently
// mutable object type tables.
func (s *Server) Fork(_ context.Context, root Typed) (*Server, error) {
	out := newBlankServer()
	out.telemetry = s.telemetry
	out.View = s.View

	s.installLock.RLock()
	defer s.installLock.RUnlock()

	for name, scalar := range s.scalars {
		out.scalars[name] = scalar
	}
	for name, typeDef := range s.typeDefs {
		out.typeDefs[name] = typeDef
	}
	for name, directive := range s.directives {
		out.directives[name] = directive
	}
	for name, objectType := range s.objects {
		forkable, ok := objectType.(ForkableObjectType)
		if !ok {
			return nil, fmt.Errorf("object type %q (%T) cannot be forked", name, objectType)
		}
		forkedType, err := forkable.ForkObjectType(out)
		if err != nil {
			return nil, fmt.Errorf("fork object type %q: %w", name, err)
		}
		out.objects[name] = forkedType
	}
	for _, hook := range s.installHooks {
		forkable, ok := hook.(InstallHookForker)
		if !ok {
			return nil, fmt.Errorf("install hook %T cannot be forked", hook)
		}
		out.installHooks = append(out.installHooks, forkable.ForkInstallHook(out))
	}

	rootType, ok := out.objects[root.Type().Name()]
	if !ok {
		return nil, fmt.Errorf("forked root type %q not found", root.Type().Name())
	}
	rootObj, err := rootType.New(newDetachedResult(nil, root))
	if err != nil {
		return nil, fmt.Errorf("new forked root: %w", err)
	}
	out.root = rootObj

	return out, nil
}

func (s *Server) invalidateSchemaCache() {
	s.schemaLock.Lock()
	clear(s.schemas)
	clear(s.schemaDigests)
	clear(s.schemaOnces)
	s.schemaLock.Unlock()
}

func NewDefaultHandler(es graphql.ExecutableSchema) *handler.Server {
	// TODO: avoid this deprecated method, and customize the options
	srv := handler.NewDefaultServer(es)

	srv.SetValidationRulesFn(func() *rules.Rules {
		validationRules := rules.NewDefaultRules()

		// HACK: these rules are disabled because some clients don't send the right
		// types:
		//   - PHP + Elixir SDKs send enums quoted
		//   - The shell sends enums quoted, and ints/floats as strings
		//   - etc
		validationRules.RemoveRule(rules.ValuesOfCorrectTypeRule.Name)
		validationRules.RemoveRule(rules.ValuesOfCorrectTypeRuleWithoutSuggestions.Name)

		// HACK: this rule is disabled because PHP modules <=v0.15.2 query
		// inputArgs incorrectly.
		validationRules.RemoveRule(rules.ScalarLeafsRule.Name)

		return validationRules
	})

	return srv
}

var coreScalars = []ScalarType{
	Boolean(false),
	Int(0),
	Float(0),
	String(""),
	// instead of a single ID type, each object has its own ID type
	// ID{},
}

var coreDirectives = []DirectiveSpec{
	{
		Name: "deprecated",
		Description: FormatDescription(
			`The @deprecated built-in directive is used within the type system
			definition language to indicate deprecated portions of a GraphQL
			service's schema, such as deprecated fields on a type, arguments on a
			field, input fields on an input type, or values of an enum type.`),
		Args: NewInputSpecs(
			InputSpec{
				Name: "reason",
				Description: FormatDescription(
					`Explains why this element was deprecated, usually also including a
					suggestion for how to access supported similar data. Formatted in
					[Markdown](https://daringfireball.net/projects/markdown/).`),
				Type:    String(""),
				Default: String("No longer supported"),
			},
		),
		Locations: []DirectiveLocation{
			DirectiveLocationFieldDefinition,
			DirectiveLocationArgumentDefinition,
			DirectiveLocationInputFieldDefinition,
			DirectiveLocationEnumValue,
		},
	},
	{
		Name: "experimental",
		Description: FormatDescription(
			`Explains why this element is marked experimental.
			Formatted in [Markdown](https://daringfireball.net/projects/markdown/).`),
		Args: NewInputSpecs(
			InputSpec{
				Name:        "reason",
				Description: FormatDescription(`Explains why this element was marked experimental.`),
				Type:        String(""),
				Default:     String("Not stabilized"),
			},
		),
		Locations: []DirectiveLocation{
			DirectiveLocationFieldDefinition,
			DirectiveLocationArgumentDefinition,
			DirectiveLocationInputFieldDefinition,
			DirectiveLocationEnumValue,
		},
	},
}

// Root returns the root object of the server. It is suitable for passing to
// Resolve to resolve a query.
func (s *Server) Root() AnyObjectResult {
	return s.root
}

// InstallObject installs the given Object type into the schema, or returns the
// previously installed type if it was already present
func (s *Server) InstallObject(class ObjectType, directives ...*ast.Directive) ObjectType {
	s.installLock.Lock()

	if class, ok := s.objects[class.TypeName()]; ok {
		s.installLock.Unlock()
		return class
	}

	s.invalidateSchemaCache()

	s.objects[class.TypeName()] = class
	if idType, hasID := class.IDType(); hasID {
		s.scalars[idType.TypeName()] = idType

		spec := FieldSpec{
			Name:        fmt.Sprintf("load%sFromID", class.TypeName()),
			Description: fmt.Sprintf("Load a %s from its ID.", class.TypeName()),
			Type:        class.Typed(),
			Args: NewInputSpecs(
				InputSpec{
					Name: "id",
					Type: idType,
				},
			),
			Directives: directives,
		}

		s.Root().ObjectType().ExtendLoadByID(
			spec,
			func(ctx context.Context, _ AnyResult, args map[string]Input) (AnyResult, error) {
				idable, ok := args["id"].(IDable)
				if !ok {
					return nil, fmt.Errorf("expected IDable, got %T", args["id"])
				}
				id, err := idable.ID()
				if err != nil {
					return nil, fmt.Errorf("expected valid ID: %w", err)
				}
				if id.Type() == nil {
					return nil, fmt.Errorf("expected typed ID, got untyped ID")
				}
				if id.Type().ToAST().NamedType != class.TypeName() {
					return nil, fmt.Errorf("expected ID of type %q, got %q", class.TypeName(), id.Type().ToAST().NamedType)
				}
				srv := CurrentDagqlServer(ctx)
				if srv == nil {
					return nil, fmt.Errorf("current dagql server not found")
				}
				res, err := srv.Load(ctx, id)
				if err != nil {
					return nil, fmt.Errorf("load: %w", err)
				}
				return res, nil
			},
		)
	}
	s.installLock.Unlock()

	for _, hook := range s.installHooks {
		hook.InstallObject(class, directives...)
	}

	return class
}

// InstallScalar installs the given Scalar type into the schema, or returns the
// previously installed type if it was already present
func (s *Server) InstallScalar(scalar ScalarType) ScalarType {
	s.installLock.Lock()
	defer s.installLock.Unlock()
	if scalar, ok := s.scalars[scalar.TypeName()]; ok {
		return scalar
	}
	s.invalidateSchemaCache()
	s.scalars[scalar.TypeName()] = scalar
	return scalar
}

// InstallDirective installs the given Directive type into the schema.
func (s *Server) InstallDirective(directive DirectiveSpec) {
	s.installLock.Lock()
	defer s.installLock.Unlock()
	s.directives[directive.Name] = directive
	s.invalidateSchemaCache()
}

// InstallTypeDef installs an arbitrary type definition into the schema.
func (s *Server) InstallTypeDef(def TypeDef) {
	s.installLock.Lock()
	defer s.installLock.Unlock()
	s.typeDefs[def.TypeName()] = def
	s.invalidateSchemaCache()
}

// ObjectType returns the ObjectType with the given name, if it exists.
func (s *Server) ObjectType(name string) (ObjectType, bool) {
	s.installLock.RLock()
	defer s.installLock.RUnlock()
	t, ok := s.objects[name]
	return t, ok
}

// ScalarType returns the ScalarType with the given name, if it exists.
func (s *Server) ScalarType(name string) (ScalarType, bool) {
	s.installLock.RLock()
	defer s.installLock.RUnlock()
	t, ok := s.scalars[name]
	return t, ok
}

// InputType returns the InputType with the given name, if it exists.
func (s *Server) TypeDef(name string) (TypeDef, bool) {
	s.installLock.RLock()
	defer s.installLock.RUnlock()
	t, ok := s.typeDefs[name]
	return t, ok
}

// Around installs a function to be called around every non-cached selection.
func (s *Server) Around(rec AroundFunc) {
	s.telemetry = rec
}

// Query is a convenience method for executing a query against the server
// without having to go through HTTP. This can be useful for introspection, for
// example.
func (s *Server) Query(ctx context.Context, query string, vars map[string]any) (map[string]any, error) {
	ctx = srvToContext(ctx, s)
	return s.ExecOp(ctx, &graphql.OperationContext{
		RawQuery:  query,
		Variables: vars,
	})
}

var _ graphql.ExecutableSchema = (*Server)(nil)

// Schema returns the current schema of the server.
func (s *Server) Schema() *ast.Schema {
	return s.SchemaForView(s.View)
}

func (s *Server) SchemaForView(view call.View) *ast.Schema {
	s.installLock.RLock()
	defer s.installLock.RUnlock()
	s.schemaLock.Lock()
	defer s.schemaLock.Unlock()

	if s.schemaOnces[view] == nil {
		s.schemaOnces[view] = &sync.Once{}
	}

	s.schemaOnces[view].Do(func() {
		queryType := s.Root().Type().Name()
		schema := &ast.Schema{
			Types:         make(map[string]*ast.Definition),
			PossibleTypes: make(map[string][]*ast.Definition),
		}
		sortutil.RangeSorted(s.objects, func(_ string, t ObjectType) {
			def := definition(ast.Object, t, view)
			if def.Name == queryType {
				schema.Query = def
			}
			schema.AddTypes(def)
			schema.AddPossibleType(def.Name, def)
		})
		sortutil.RangeSorted(s.scalars, func(_ string, t ScalarType) {
			def := definition(ast.Scalar, t, view)
			schema.AddTypes(def)
			schema.AddPossibleType(def.Name, def)
		})
		sortutil.RangeSorted(s.typeDefs, func(_ string, t TypeDef) {
			def := t.TypeDefinition(view)
			schema.AddTypes(def)
			schema.AddPossibleType(def.Name, def)
		})
		schema.Directives = map[string]*ast.DirectiveDefinition{}
		sortutil.RangeSorted(s.directives, func(n string, d DirectiveSpec) {
			schema.Directives[n] = d.DirectiveDefinition(view)
		})
		h := xxh3.New()
		json.NewEncoder(h).Encode(schema)
		s.schemas[view] = schema
		s.schemaDigests[view] = digest.NewDigest(hashutil.XXH3, h)
	})

	return s.schemas[view]
}

// SchemaDigest returns the digest of the current schema.
func (s *Server) SchemaDigest() digest.Digest {
	s.Schema() // ensure it's built
	s.schemaLock.Lock()
	defer s.schemaLock.Unlock()
	return s.schemaDigests[s.View]
}

// Complexity returns the complexity of the given field.
func (s *Server) Complexity(ctx context.Context, typeName, field string, childComplexity int, args map[string]any) (int, bool) {
	// TODO
	return 1, false
}

// ExtendedError is an error that can provide extra data in an error response.
type ExtendedError interface {
	error
	Extensions() map[string]any
}

// Exec implements graphql.ExecutableSchema.
func (s *Server) Exec(ctx1 context.Context) graphql.ResponseHandler {
	return func(ctx context.Context) (res *graphql.Response) {
		gqlOp := graphql.GetOperationContext(ctx)

		if err := gqlOp.Validate(ctx); err != nil {
			return graphql.ErrorResponse(ctx, "validate: %s", err)
		}

		results, err := s.ExecOp(ctx, gqlOp)
		if err != nil {
			return &graphql.Response{
				Errors: gqlErrs(err),
			}
		}

		data, err := json.Marshal(results)
		if err != nil {
			return graphql.ErrorResponse(ctx, "marshal: %s", err)
		}

		return &graphql.Response{
			Data: json.RawMessage(data),
		}
	}
}

func gqlErrs(err error) (errs gqlerror.List) {
	if list, ok := err.(gqlerror.List); ok {
		return list
	}
	if unwrap, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range unwrap.Unwrap() {
			errs = append(errs, gqlErrs(e)...)
		}
	} else if err != nil {
		errs = append(errs, gqlErr(err, nil))
	}
	return
}

func (s *Server) ExecOp(ctx context.Context, gqlOp *graphql.OperationContext) (results map[string]any, rerr error) {
	ctx = srvToContext(ctx, s)
	if gqlOp.Doc == nil {
		gqlOp.Doc, rerr = parser.ParseQuery(&ast.Source{Input: gqlOp.RawQuery})
		if rerr != nil {
			return nil, gqlErrs(rerr)
		}

		//nolint:staticcheck // annoying, but we can't easily switch to this without inconsistencies
		listErr := validator.Validate(s.Schema(), gqlOp.Doc)
		if len(listErr) != 0 {
			for _, e := range listErr {
				errcode.Set(e, errcode.ValidationFailed)
			}
			return nil, listErr
		}
	}
	results = make(map[string]any)
	for _, op := range gqlOp.Doc.Operations {
		switch op.Operation {
		case ast.Query:
			if gqlOp.OperationName != "" && gqlOp.OperationName != op.Name {
				continue
			}
			var sels []Selection
			sels, rerr = s.parseASTSelections(ctx, gqlOp, s.root.Type(), op.SelectionSet)
			if rerr != nil {
				return nil, fmt.Errorf("query:\n%s\n\nerror: parse selections: %w", gqlOp.RawQuery, rerr)
			}
			results, rerr = s.Resolve(ctx, s.root, sels...)
			if rerr != nil {
				return nil, rerr
			}
		case ast.Mutation:
			// TODO
			return nil, fmt.Errorf("mutations not supported")
		case ast.Subscription:
			// TODO
			return nil, fmt.Errorf("subscriptions not supported")
		}
	}
	return results, nil
}

// Resolve resolves the given selections on the given object.
//
// Each selection is resolved in parallel, and the results are returned in a
// map whose keys correspond to the selection's field name or alias.
func (s *Server) Resolve(ctx context.Context, self AnyObjectResult, sels ...Selection) (map[string]any, error) {
	ctx = srvToContext(ctx, s)
	if len(sels) == 0 {
		return nil, nil
	}

	if len(sels) == 1 {
		sel := sels[0]
		// Resolve is in the hot path, so avoiding overhead of goroutines, sync.Map, etc. when there's only
		// one selection (probably the most common case) likely pays off.
		res, err := s.resolvePath(ctx, self, sel)
		if err != nil {
			return nil, gqlErrs(err)
		}
		return map[string]any{sel.Name(): res}, nil
	}

	results := new(sync.Map)

	pool := pool.New().WithErrors()
	for _, sel := range sels {
		pool.Go(func() error {
			res, err := s.resolvePath(ctx, self, sel)
			if err != nil {
				return err
			}
			results.Store(sel.Name(), res)
			return nil
		})
	}
	if err := pool.Wait(); err != nil {
		return nil, gqlErrs(err)
	}

	resultsMap := make(map[string]any)
	results.Range(func(key, value any) bool {
		resultsMap[key.(string)] = value
		return true
	})
	return resultsMap, nil
}

// Load loads the object with the given ID.
func (s *Server) Load(ctx context.Context, id *call.ID) (AnyObjectResult, error) {
	ctx = srvToContext(ctx, s)
	if id == nil {
		return nil, fmt.Errorf("load: nil ID")
	}
	// Delegate to the canonical server so IDs are always evaluated
	// against the real schema, not the sugared one.
	if c := s.canonical; c != nil {
		return c.Load(ctx, id)
	}
	res, err := s.LoadType(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.toSelectable(res)
}

func (s *Server) loadNthValue(
	ctx context.Context,
	parent AnyResult,
	nth int,
	nullAsError bool,
) (AnyResult, error) {
	if parent == nil {
		if nullAsError {
			return nil, fmt.Errorf("item %d is null from enumerable", nth)
		}
		return nil, nil
	}

	res, err := parent.NthValue(ctx, nth)
	if err != nil {
		return nil, fmt.Errorf("nth %d: %w", nth, err)
	}
	if res == nil {
		if nullAsError {
			return nil, fmt.Errorf("item %d is null from enumerable", nth)
		}
		return nil, nil
	}

	res, ok := res.DerefValue()
	if !ok || res == nil {
		if nullAsError {
			return nil, fmt.Errorf("item %d is null from enumerable", nth)
		}
		return nil, nil
	}
	return res, nil
}

func (s *Server) LoadType(ctx context.Context, id *call.ID) (_ AnyResult, rerr error) {
	ctx = srvToContext(ctx, s)
	if id == nil {
		return nil, fmt.Errorf("load type: nil ID")
	}
	if c := s.canonical; c != nil {
		return c.LoadType(ctx, id)
	}

	leaseCtx, release, err := withOperationLease(ctx)
	if err != nil {
		return nil, fmt.Errorf("load %s: acquire operation lease: %w", id.Display(), err)
	}
	ctx = leaseCtx
	defer func() {
		if releaseErr := release(context.WithoutCancel(ctx)); releaseErr != nil && rerr == nil {
			rerr = releaseErr
		}
	}()

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("load %s: current client metadata: %w", id.Display(), err)
	}
	if clientMetadata.SessionID == "" {
		return nil, fmt.Errorf("load %s: empty session ID", id.Display())
	}
	cache, err := EngineCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("load %s: current dagql cache: %w", id.Display(), err)
	}
	if id.IsHandle() {
		res, err := cache.LoadResultByResultID(ctx, clientMetadata.SessionID, s, id.EngineResultID())
		if err != nil {
			return nil, err
		}
		if id.Type() != nil && id.Type().ToAST().NonNull {
			if derefCapable, ok := res.(interface{ withDerefViewAny() AnyResult }); ok {
				if shared := res.cacheSharedResult(); shared != nil {
					payload := shared.loadPayloadState()
					if inner, valid := derefTyped(payload.self); valid && inner != nil && inner.Type() != nil && inner.Type().Name() == id.Type().NamedType() {
						res = derefCapable.withDerefViewAny()
					}
				}
			}
		}
		if id.Type() != nil && !id.Type().ToAST().NonNull && res.Type() != nil && res.Type().NonNull && res.Type().Name() == id.Type().NamedType() {
			res = res.NullableWrapped()
		}
		if id.Type() != nil && res.Type() != nil && res.Type().Name() != id.Type().NamedType() {
			return nil, fmt.Errorf("load %s: expected %s, got %s", idInputDebugString(id), id.Type().ToAST(), res.Type())
		}
		return res, nil
	}

	state := &recipeLoadState{
		ctx:       ctx,
		srv:       s,
		cache:     cache,
		sessionID: clientMetadata.SessionID,
		loads:     make(map[string]*recipeLoadFuture),
	}
	return state.load(id)
}

type recipeLoadFuture struct {
	done chan struct{}
	res  AnyResult
	err  error
}

type recipeLoadState struct {
	ctx       context.Context
	srv       *Server
	cache     *Cache
	sessionID string

	mu    sync.Mutex
	loads map[string]*recipeLoadFuture
}

func (state *recipeLoadState) load(id *call.ID) (AnyResult, error) {
	if id == nil {
		return nil, nil
	}
	if id.IsHandle() {
		return state.srv.LoadType(state.ctx, id)
	}

	key := id.Digest().String()
	state.mu.Lock()
	if future := state.loads[key]; future != nil {
		state.mu.Unlock()
		<-future.done
		return future.res, future.err
	}
	future := &recipeLoadFuture{done: make(chan struct{})}
	state.loads[key] = future
	state.mu.Unlock()

	future.res, future.err = state.loadRecipeVertex(id)
	close(future.done)
	return future.res, future.err
}

func (state *recipeLoadState) loadRecipeVertex(id *call.ID) (AnyResult, error) {
	callCtx := state.ctx
	if hit, ok, err := state.cache.lookupCacheForDigests(callCtx, state.sessionID, state.srv, id.Digest(), id.ExtraDigests()); err != nil {
		return nil, fmt.Errorf("load %s: fast cache lookup: %w", idInputDebugString(id), err)
	} else if ok {
		return hit, nil
	}

	if nth := int(id.Nth()); nth != 0 {
		receiver := id.Receiver()
		if receiver == nil {
			return nil, fmt.Errorf("load %s: nth selection missing receiver", idInputDebugString(id))
		}
		parent, err := state.load(receiver)
		if err != nil {
			return nil, fmt.Errorf("load %s: receiver: %w", idInputDebugString(id), err)
		}
		return state.srv.loadNthValue(callCtx, parent, nth, true)
	}

	inputIDs := directRecipeInputIDs(id)
	loadedInputs := make(map[string]AnyResult, len(inputIDs))
	var loadedMu sync.Mutex
	eg, _ := errgroup.WithContext(state.ctx)
	for _, inputID := range inputIDs {
		inputID := inputID
		eg.Go(func() error {
			res, err := state.load(inputID)
			if err != nil {
				return err
			}
			loadedMu.Lock()
			loadedInputs[inputID.Digest().String()] = res
			loadedMu.Unlock()
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		if fallbackID, ok := rewriteWithFileRecipeToDirectoryFallback(id, err); ok {
			return state.load(fallbackID)
		}
		return nil, fmt.Errorf("load %s: inputs: %w", idInputDebugString(id), err)
	}
	var base AnyResult
	if receiver := id.Receiver(); receiver != nil {
		base = loadedInputs[receiver.Digest().String()]
		if base == nil {
			return nil, fmt.Errorf("load %s: missing loaded receiver", idInputDebugString(id))
		}
	} else {
		base = state.srv.root
	}

	baseObj, err := state.srv.toSelectable(base)
	if err != nil {
		return nil, fmt.Errorf("load %s: instantiate base: %w", idInputDebugString(id), err)
	}
	frame, err := state.loadedResultCallFromRecipeID(id, loadedInputs)
	if err != nil {
		return nil, fmt.Errorf("load %s: build result call: %w", idInputDebugString(id), err)
	}
	callCtx = ContextWithCall(callCtx, frame)
	sel, err := selectorFromLoadedCall(callCtx, frame, baseObj)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", idInputDebugString(id), err)
	}
	req := &CallRequest{ResultCall: frame}
	if hit, ok, err := state.cache.lookupCallRequest(callCtx, state.sessionID, state.srv, req); err != nil {
		return nil, fmt.Errorf("load %s: structural cache lookup: %w", idInputDebugString(id), err)
	} else if ok {
		return hit, nil
	}
	return baseObj.Select(callCtx, state.srv, sel)
}

// TODO: DELETE THIS ANCIENT HACK AND ALL ASSOCIATED CRAP
// TODO: DELETE THIS ANCIENT HACK AND ALL ASSOCIATED CRAP
// TODO: DELETE THIS ANCIENT HACK AND ALL ASSOCIATED CRAP
// TODO: DELETE THIS ANCIENT HACK AND ALL ASSOCIATED CRAP
// TODO: DELETE THIS ANCIENT HACK AND ALL ASSOCIATED CRAP
func rewriteWithFileRecipeToDirectoryFallback(id *call.ID, err error) (*call.ID, bool) {
	if err == nil || id == nil || id.IsHandle() || id.Field() != "withFile" {
		return nil, false
	}
	if !strings.Contains(err.Error(), "is a directory, not a file") {
		return nil, false
	}
	if !recipeBoolArgIsTrue(id, "allowDirectorySourceFallback") {
		return nil, false
	}

	receiver := id.Receiver()
	if receiver == nil {
		return nil, false
	}

	sourceArg := id.Arg("source")
	if sourceArg == nil {
		return nil, false
	}
	sourceLit, ok := sourceArg.Value().(*call.LiteralID)
	if !ok {
		return nil, false
	}
	dirSourceID, ok := directorySourceIDFromFileSourceIDForRecipeLoad(sourceLit.Value())
	if !ok {
		return nil, false
	}

	newArgs := make([]*call.Argument, 0, len(id.Args()))
	for _, arg := range id.Args() {
		switch arg.Name() {
		case "allowDirectorySourceFallback":
			continue
		case "source":
			newArgs = append(newArgs, call.NewArgument("source", call.NewLiteralID(dirSourceID), arg.IsSensitive()))
		default:
			newArgs = append(newArgs, arg)
		}
	}

	opts := []call.IDOpt{
		call.WithView(id.View()),
		call.WithModule(id.Module()),
		call.WithNth(int(id.Nth())),
		call.WithEffectIDs(id.EffectIDs()),
		call.WithArgs(newArgs...),
		call.WithImplicitInputs(id.ImplicitInputs()...),
	}
	for _, extra := range id.ExtraDigests() {
		opts = append(opts, call.WithExtraDigest(extra))
	}

	return receiver.Append(id.Type().ToAST(), "withDirectory", opts...), true
}

func recipeBoolArgIsTrue(id *call.ID, name string) bool {
	if id == nil || id.IsHandle() {
		return false
	}
	arg := id.Arg(name)
	if arg == nil {
		return false
	}
	lit, ok := arg.Value().(*call.LiteralBool)
	return ok && lit.Value()
}

func directorySourceIDFromFileSourceIDForRecipeLoad(fileSourceID *call.ID) (*call.ID, bool) {
	if fileSourceID == nil || fileSourceID.IsHandle() || fileSourceID.Field() != "file" {
		return nil, false
	}

	receiver := fileSourceID.Receiver()
	if receiver == nil {
		return nil, false
	}

	pathArg := fileSourceID.Arg("path")
	if pathArg == nil {
		return nil, false
	}
	pathLit, ok := pathArg.Value().(*call.LiteralString)
	if !ok {
		return nil, false
	}

	return receiver.Append(
		receiver.Type().ToAST(),
		"directory",
		call.WithArgs(call.NewArgument("path", call.NewLiteralString(pathLit.Value()), false)),
	), true
}

func directRecipeInputIDs(id *call.ID) []*call.ID {
	if id == nil || id.IsHandle() {
		return nil
	}

	var inputIDs []*call.ID
	if receiver := id.Receiver(); receiver != nil {
		inputIDs = append(inputIDs, receiver)
	}
	if mod := id.Module(); mod != nil && mod.ID() != nil {
		inputIDs = append(inputIDs, mod.ID())
	}
	for _, arg := range id.Args() {
		if arg == nil {
			continue
		}
		gatherRecipeLiteralInputIDs(arg.Value(), &inputIDs)
	}
	for _, input := range id.ImplicitInputs() {
		if input == nil {
			continue
		}
		gatherRecipeLiteralInputIDs(input.Value(), &inputIDs)
	}
	return inputIDs
}

func gatherRecipeLiteralInputIDs(lit call.Literal, inputIDs *[]*call.ID) {
	switch v := lit.(type) {
	case *call.LiteralID:
		*inputIDs = append(*inputIDs, v.Value())
	case *call.LiteralList:
		for _, item := range v.Values() {
			gatherRecipeLiteralInputIDs(item, inputIDs)
		}
	case *call.LiteralObject:
		for _, field := range v.Args() {
			if field == nil {
				continue
			}
			gatherRecipeLiteralInputIDs(field.Value(), inputIDs)
		}
	}
}

func (state *recipeLoadState) loadedResultCallFromRecipeID(id *call.ID, loadedInputs map[string]AnyResult) (*ResultCall, error) {
	if id == nil {
		return nil, nil
	}
	if id.IsHandle() {
		return nil, fmt.Errorf("handle-form IDs cannot be converted to result calls: %s", idInputDebugString(id))
	}

	var callType *ResultCallType
	if id.Type() != nil {
		callType = NewResultCallType(id.Type().ToAST())
	}
	frame := &ResultCall{
		Kind:         ResultCallKindField,
		Type:         callType,
		Field:        id.Field(),
		View:         id.View(),
		Nth:          id.Nth(),
		EffectIDs:    id.EffectIDs(),
		ExtraDigests: id.ExtraDigests(),
	}
	if receiver := id.Receiver(); receiver != nil {
		receiverRef, err := state.loadedResultCallRefForRecipeID(receiver, loadedInputs)
		if err != nil {
			return nil, fmt.Errorf("receiver: %w", err)
		}
		frame.Receiver = receiverRef
	}
	if mod := id.Module(); mod != nil {
		modRef, err := state.loadedResultCallRefForRecipeID(mod.ID(), loadedInputs)
		if err != nil {
			return nil, fmt.Errorf("module: %w", err)
		}
		frame.Module = &ResultCallModule{
			ResultRef: modRef,
			Name:      mod.Name(),
			Ref:       mod.Ref(),
			Pin:       mod.Pin(),
		}
	}
	for _, arg := range id.Args() {
		converted, err := state.loadedResultCallArgFromRecipeArgument(arg, loadedInputs)
		if err != nil {
			return nil, fmt.Errorf("arg %q: %w", arg.Name(), err)
		}
		frame.Args = append(frame.Args, converted)
	}
	for _, input := range id.ImplicitInputs() {
		converted, err := state.loadedResultCallArgFromRecipeArgument(input, loadedInputs)
		if err != nil {
			return nil, fmt.Errorf("implicit input %q: %w", input.Name(), err)
		}
		frame.ImplicitInputs = append(frame.ImplicitInputs, converted)
	}
	return frame, nil
}

func (state *recipeLoadState) loadedResultCallRefForRecipeID(id *call.ID, loadedInputs map[string]AnyResult) (*ResultCallRef, error) {
	if id == nil {
		return nil, nil
	}
	if id.IsHandle() {
		return nil, fmt.Errorf("handle-form IDs cannot be used as recipe input refs: %s", idInputDebugString(id))
	}

	res := loadedInputs[id.Digest().String()]
	if res == nil {
		return nil, fmt.Errorf("missing loaded result for %s", id.Digest())
	}
	shared := res.cacheSharedResult()
	if shared == nil || shared.id == 0 {
		return nil, fmt.Errorf("loaded result for %s is not attached", id.Digest())
	}
	return &ResultCallRef{ResultID: uint64(shared.id), shared: shared}, nil
}

func (state *recipeLoadState) loadedResultCallArgFromRecipeArgument(arg *call.Argument, loadedInputs map[string]AnyResult) (*ResultCallArg, error) {
	if arg == nil {
		return nil, nil
	}
	value, err := state.loadedResultCallLiteralFromRecipeLiteral(arg.Value(), loadedInputs)
	if err != nil {
		return nil, err
	}
	return &ResultCallArg{
		Name:        arg.Name(),
		IsSensitive: arg.IsSensitive(),
		Value:       value,
	}, nil
}

func (state *recipeLoadState) loadedResultCallLiteralFromRecipeLiteral(lit call.Literal, loadedInputs map[string]AnyResult) (*ResultCallLiteral, error) {
	switch v := lit.(type) {
	case nil:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindNull}, nil
	case *call.LiteralNull:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindNull}, nil
	case *call.LiteralBool:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindBool, BoolValue: v.Value()}, nil
	case *call.LiteralInt:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindInt, IntValue: v.Value()}, nil
	case *call.LiteralFloat:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindFloat, FloatValue: v.Value()}, nil
	case *call.LiteralString:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: v.Value()}, nil
	case *call.LiteralEnum:
		return &ResultCallLiteral{Kind: ResultCallLiteralKindEnum, EnumValue: v.Value()}, nil
	case *call.LiteralDigestedString:
		return &ResultCallLiteral{
			Kind:                 ResultCallLiteralKindDigestedString,
			DigestedStringValue:  v.Value(),
			DigestedStringDigest: v.Digest(),
		}, nil
	case *call.LiteralID:
		resultRef, err := state.loadedResultCallRefForRecipeID(v.Value(), loadedInputs)
		if err != nil {
			return nil, err
		}
		return &ResultCallLiteral{
			Kind:      ResultCallLiteralKindResultRef,
			ResultRef: resultRef,
		}, nil
	case *call.LiteralList:
		items := make([]*ResultCallLiteral, 0, v.Len())
		for _, item := range v.Values() {
			converted, err := state.loadedResultCallLiteralFromRecipeLiteral(item, loadedInputs)
			if err != nil {
				return nil, err
			}
			items = append(items, converted)
		}
		return &ResultCallLiteral{
			Kind:      ResultCallLiteralKindList,
			ListItems: items,
		}, nil
	case *call.LiteralObject:
		fields := make([]*ResultCallArg, 0, v.Len())
		for _, field := range v.Args() {
			converted, err := state.loadedResultCallArgFromRecipeArgument(field, loadedInputs)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", field.Name(), err)
			}
			fields = append(fields, converted)
		}
		return &ResultCallLiteral{
			Kind:         ResultCallLiteralKindObject,
			ObjectFields: fields,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported recipe literal %T", lit)
	}
}

func selectorFromLoadedCall(ctx context.Context, frame *ResultCall, baseObj AnyObjectResult) (Selector, error) {
	if frame == nil {
		return Selector{}, fmt.Errorf("nil result call")
	}
	view := frame.View
	fieldSpec, ok := baseObj.ObjectType().FieldSpec(frame.Field, view)
	if !ok {
		return Selector{}, fmt.Errorf("field %q not found on %s", frame.Field, baseObj.Type().Name())
	}
	args := make([]NamedInput, 0, len(frame.Args))
	for _, argSpec := range fieldSpec.Args.Inputs(view) {
		var frameArg *ResultCallArg
		for _, arg := range frame.Args {
			if arg != nil && arg.Name == argSpec.Name {
				frameArg = arg
				break
			}
		}
		if frameArg == nil {
			continue
		}
		inputVal, err := inputValueFromResultCallLiteral(ctx, frameArg.Value)
		if err != nil {
			return Selector{}, fmt.Errorf("request arg %q literal input: %w", argSpec.Name, err)
		}
		input, err := argSpec.Type.Decoder().DecodeInput(inputVal)
		if err != nil {
			return Selector{}, fmt.Errorf("request arg %q value as %T (%s) using %T: %w", argSpec.Name, argSpec.Type, argSpec.Type.Type(), argSpec.Type.Decoder(), err)
		}
		args = append(args, NamedInput{Name: argSpec.Name, Value: input})
	}
	return Selector{
		Field: frame.Field,
		Args:  args,
		Nth:   int(frame.Nth),
		View:  view,
	}, nil
}

// Select evaluates a series of chained field selections starting from the
// given object and assigns the final result value into dest.
//nolint:gocyclo // intrinsically long state machine; refactoring would hurt clarity
func (s *Server) Select(ctx context.Context, self AnyObjectResult, dest any, sels ...Selector) (rerr error) {
	ctx = srvToContext(ctx, s)
	if isNonInternal(ctx) {
		// We only want "non internal" to apply to the immediate call, so flip it
		// from here on; it already did its job in avoiding the withInternal below.
		//
		// FIXME: this is an absurd dance, we should maybe just remove the
		// auto-Internaling, but that'll be a game of wack-a-mole
		ctx = withoutNonInternalTelemetry(ctx)
	} else {
		// Annotate ctx with the internal flag so we can distinguish self-calls from
		// user-calls in the UI.
		//
		// Only do this if we haven't been explicitly told not to (internal=false).
		ctx = withInternal(ctx)
	}

	leaseCtx, release, err := withOperationLease(ctx)
	if err != nil {
		return fmt.Errorf("acquire operation lease: %w", err)
	}
	ctx = leaseCtx
	defer func() {
		if releaseErr := release(context.WithoutCancel(ctx)); releaseErr != nil && rerr == nil {
			rerr = releaseErr
		}
	}()

	var res AnyResult = self
	for i, sel := range sels {
		nth := sel.Nth
		// if we are selecting the nth element, then select the parent list first and
		// grab the NthValue below
		if nth != 0 {
			sel.Nth = 0
		}
		var err error
		res, err = self.Select(ctx, s, sel)
		if err != nil {
			return err
		}

		if res == nil {
			// null result; nothing to do
			return nil
		}
		unwrap := res.Unwrap()
		if unwrap == nil {
			if shared := res.cacheSharedResult(); shared != nil {
				state := shared.loadPayloadState()
				if state.isObject {
					typeName := sharedResultObjectTypeName(shared, state)
					return fmt.Errorf(
						"select %s returned unresolved object-typed result %q (shared result %d: hasValue=%t, persistedEnvelope=%t)",
						sel.Field,
						typeName,
						shared.id,
						state.hasValue,
						state.persistedEnvelope != nil,
					)
				}
			}
			if _, ok := res.(AnyObjectResult); ok {
				typeName := ""
				if shared := res.cacheSharedResult(); shared != nil {
					payload := shared.loadPayloadState()
					typeName = sharedResultObjectTypeName(shared, payload)
					return fmt.Errorf(
						"select %s returned unresolved object result %q (shared result %d: hasValue=%t, persistedEnvelope=%t)",
						sel.Field,
						typeName,
						shared.id,
						payload.hasValue,
						payload.persistedEnvelope != nil,
					)
				}
				return fmt.Errorf("select %s returned unresolved object result %q", sel.Field, typeName)
			}
			// null scalar result; nothing to do
			return nil
		}

		if nth != 0 {
			res, err = s.loadNthValue(ctx, res, nth, true)
			if err != nil {
				return err
			}
		}

		destV := reflect.ValueOf(dest).Elem()
		if res.Type().Elem != nil {
			if i+1 < len(sels) {
				return fmt.Errorf("cannot sub-select enum of %s", res.Type())
			}
			if destV.Type().Kind() != reflect.Slice {
				// assigning to something like dagql.Typed, don't need to enumerate
				break
			}
			isObj := s.isObjectType(res.Type().Elem.Name())
			enum, isEnum := res.Unwrap().(Enumerable)
			if !isEnum {
				return fmt.Errorf("cannot assign non-Enumerable %T to %s", res, destV.Type())
			}
			// HACK: list of objects must be the last selection right now unless nth used in Selector.
			if i+1 < len(sels) {
				return fmt.Errorf("cannot sub-select enum of %s", res.Type())
			}
			for nth := 1; nth <= enum.Len(); nth++ {
				val, err := s.loadNthValue(ctx, res, nth, false)
				if err != nil {
					return err
				}
				if val == nil || val.Unwrap() == nil {
					if err := appendAssign(destV, nil); err != nil {
						return err
					}
					continue
				}
				if isObj {
					val, err = s.toSelectable(val)
					if err != nil {
						return fmt.Errorf("select %dth array element: %w", nth, err)
					}
				}
				if err := appendAssign(destV, val); err != nil {
					return err
				}
			}
			return nil
		} else if s.isObjectType(res.Type().Name()) {
			// if the result is an Object, set it as the next selection target, and
			// assign res to the "hydrated" Object
			self, err = s.toSelectable(res)
			if err != nil {
				return err
			}
			res = self
		} else if i+1 < len(sels) {
			// if the result is not an object and there are further selections,
			// that's a logic error.
			return fmt.Errorf("cannot sub-select %s", res.Type())
		}
	}

	return assign(reflect.ValueOf(dest).Elem(), res)
}

func (s *Server) isObjectType(typeName string) bool {
	_, ok := s.ObjectType(typeName)
	return ok
}

// Attach an install hook
func (s *Server) AddInstallHook(hook InstallHook) {
	s.installLock.Lock()
	s.installHooks = append(s.installHooks, hook)
	s.installLock.Unlock()
}

func LoadIDs[T Typed](ctx context.Context, srv *Server, ids []ID[T]) ([]T, error) {
	out := make([]T, len(ids))
	eg := new(errgroup.Group)
	for i, id := range ids {
		eg.Go(func() error {
			val, err := id.Load(ctx, srv)
			if err != nil {
				return err
			}
			out[i] = val.Self()
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return out, err
	}
	return out, nil
}

func LoadIDResults[T Typed](ctx context.Context, srv *Server, ids []ID[T]) ([]ObjectResult[T], error) {
	out := make([]ObjectResult[T], len(ids))
	eg := new(errgroup.Group)
	for i, id := range ids {
		eg.Go(func() error {
			val, err := id.Load(ctx, srv)
			if err != nil {
				return err
			}
			out[i] = val
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return out, err
	}
	return out, nil
}

type callCtx struct{}

func ContextWithCall(ctx context.Context, call *ResultCall) context.Context {
	return context.WithValue(ctx, callCtx{}, call)
}

func CurrentCall(ctx context.Context) *ResultCall {
	val := ctx.Value(callCtx{})
	if val == nil {
		return nil
	}
	return val.(*ResultCall)
}

// ChildFieldCall derives the call frame for a child field selection while
// preserving the receiver lineage, module, and view from the parent call.
func ChildFieldCall(parent *ResultCall, field string, fieldType *ast.Type) *ResultCall {
	if parent == nil {
		return nil
	}
	return &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(fieldType),
		Field:    field,
		View:     parent.View,
		Receiver: &ResultCallRef{Call: parent.clone()},
		Module:   parent.Module.clone(),
	}
}

type srvCtx struct{}

type cacheCtx struct{}

func ContextWithCache(ctx context.Context, cache *Cache) context.Context {
	return context.WithValue(ctx, cacheCtx{}, cache)
}

func EngineCache(ctx context.Context) (*Cache, error) {
	val := ctx.Value(cacheCtx{})
	if val == nil {
		return nil, fmt.Errorf("no dagql cache in context")
	}
	return val.(*Cache), nil
}

func srvToContext(ctx context.Context, srv *Server) context.Context {
	if CurrentDagqlServer(ctx) == srv {
		return ctx
	}
	return context.WithValue(ctx, srvCtx{}, srv)
}

func CurrentDagqlServer(ctx context.Context) *Server {
	val := ctx.Value(srvCtx{})
	if val == nil {
		return nil
	}
	return val.(*Server)
}

// NewResultForCurrentCall creates a new Result that's set to the current call
// from the given self value.
func NewResultForCurrentCall[T Typed](
	ctx context.Context,
	self T,
) (Result[T], error) {
	return NewResultForCall(self, CurrentCall(ctx))
}

// NewResultForCall creates a new Result with the given call and self value.
func NewResultForCall[T Typed](
	self T,
	call *ResultCall,
) (res Result[T], _ error) {
	if call == nil {
		return res, errors.New("call is nil")
	}

	// check that we aren't trying to create a Result for a Result itself
	if _, ok := any(self).(AnyResult); ok {
		return res, fmt.Errorf("cannot create Result for %T, it is already a Result", self)
	}

	return newDetachedResult(call, self), nil
}

func NewObjectResultForCurrentCall[T Typed](
	ctx context.Context,
	srv *Server,
	self T,
) (ObjectResult[T], error) {
	return NewObjectResultForCall(self, srv, CurrentCall(ctx))
}

func NewObjectResultForCall[T Typed](
	self T,
	srv *Server,
	call *ResultCall,
) (res ObjectResult[T], _ error) {
	objType, ok := srv.ObjectType(self.Type().Name())
	if !ok {
		return res, fmt.Errorf("unknown type %q", self.Type().Name())
	}
	class, ok := objType.(Class[T])
	if !ok {
		return res, fmt.Errorf("not a Class: %T", objType)
	}

	inst, err := NewResultForCall(self, call)
	if err != nil {
		return res, err
	}

	return ObjectResult[T]{
		Result: inst,
		class:  class,
	}, nil
}

func idToPath(id *call.ID) ast.Path {
	path := ast.Path{}
	if id == nil { // Query
		return path
	}
	if id.Receiver() != nil {
		path = idToPath(id.Receiver())
	}
	path = append(path, ast.PathName(id.Field()))
	if id.Nth() != 0 {
		path = append(path, ast.PathIndex(id.Nth()-1))
	}
	return path
}

func gqlErr(rerr error, path ast.Path) *gqlerror.Error {
	var gqlErr *gqlerror.Error
	if errors.As(rerr, &gqlErr) {
		if len(gqlErr.Path) == 0 {
			gqlErr.Path = path
		}
		return gqlErr
	}
	gqlErr = &gqlerror.Error{
		Err:     rerr,
		Message: rerr.Error(),
		Path:    path,
	}
	var ext ExtendedError
	if errors.As(rerr, &ext) {
		gqlErr.Extensions = ext.Extensions()
	}
	return gqlErr
}

func (s *Server) resolvePath(ctx context.Context, self AnyObjectResult, sel Selection) (res any, rerr error) {
	defer func() {
		if r := recover(); r != nil {
			rerr = PanicError{
				Cause:     r,
				Self:      self,
				Selection: sel,
				Stack:     debug.Stack(),
			}
		}

		if rerr != nil {
			var queryPath ast.Path
			if recipeID, err := self.RecipeID(ctx); err == nil {
				queryPath = append(idToPath(recipeID), ast.PathName(sel.Name()))
			} else {
				queryPath = ast.Path{ast.PathName(sel.Name())}
			}
			rerr = gqlErr(rerr, queryPath)
		}
	}()

	if sel.Selector.Nth != 0 {
		// NOTE: this is explicitly not handled - but it's fine because
		// resolvePath is called from selectors from field parsing, so we
		// shouldn't hit this in practice
		return nil, fmt.Errorf("cannot resolve selector path with nth")
	}

	leaseCtx, release, err := withOperationLease(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire operation lease: %w", err)
	}
	ctx = leaseCtx
	defer func() {
		if releaseErr := release(context.WithoutCancel(ctx)); releaseErr != nil && rerr == nil {
			rerr = releaseErr
		}
	}()

	val, err := self.Select(ctx, s, sel.Selector)
	if err != nil {
		return nil, err
	}

	if val == nil || val.Unwrap() == nil {
		// a nil value ignores all sub-selections
		return nil, nil
	}

	enum, ok := val.Unwrap().(Enumerable)
	if ok {
		// we're sub-selecting into an enumerable value, so we need to resolve each
		// element

		length := enum.Len()
		results := make([]any, length) // TODO subtle: favor [] over null result

		if len(sel.Subselections) == 0 {
			// No subselections - resolve serially (fast path, no goroutine overhead)
			for nth := 1; nth <= length; nth++ {
				elemVal, err := s.loadNthValue(ctx, val, nth, false)
				if err != nil {
					return nil, err
				}
				if elemVal == nil || elemVal.Unwrap() == nil {
					results[nth-1] = nil
					continue
				}
				results[nth-1] = elemVal.Unwrap()
			}
		} else {
			// Has subselections - resolve in parallel
			p := pool.New().WithErrors()
			for nth := 1; nth <= length; nth++ {
				p.Go(func() error {
					elemVal, err := s.loadNthValue(ctx, val, nth, false)
					if err != nil {
						return err
					}
					if elemVal == nil {
						results[nth-1] = nil
						return nil
					}
					node, err := s.toSelectable(elemVal)
					if err != nil {
						return fmt.Errorf("instantiate %dth array element: %w", nth, err)
					}
					res, err := s.Resolve(ctx, node, sel.Subselections...)
					if err != nil {
						return err
					}
					results[nth-1] = res
					return nil
				})
			}
			if err := p.Wait(); err != nil {
				return nil, err
			}
		}
		return results, nil
	}

	if len(sel.Subselections) == 0 {
		// Check if the value is an object type that requires sub-selections.
		// Without this check, returning an object without sub-selections
		// leads to a cryptic JSON marshal error because object values
		// contain function closures that can't be serialized.
		if _, ok := val.(AnyObjectResult); ok {
			return nil, fmt.Errorf("field %q of type %q must have a selection of subfields", sel.Selector.Field, val.Type().Name())
		}
		if _, ok := s.ObjectType(val.Type().Name()); ok {
			return nil, fmt.Errorf("field %q of type %q must have a selection of subfields", sel.Selector.Field, val.Type().Name())
		}
		return val.Unwrap(), nil
	}

	// instantiate the return value so we can sub-select
	node, err := s.toSelectable(val)
	if err != nil {
		return nil, fmt.Errorf("instantiate: %w", err)
	}

	return s.Resolve(ctx, node, sel.Subselections...)
}

func (s *Server) toSelectable(val AnyResult) (AnyObjectResult, error) {
	if sel, ok := val.(AnyObjectResult); ok {
		// We always support returning something that's already Selectable, e.g. an
		// object loaded from its ID.
		return sel, nil
	}

	className := val.Type().Name()
	class, ok := s.ObjectType(className)
	if ok {
		return class.New(val)
	}

	// if this is an interface value, we may only know about the underlying object
	// it's wrapping, check that
	if iface, ok := UnwrapAs[InterfaceValue](val); ok {
		obj, err := iface.UnderlyingObject()
		if err != nil {
			return nil, fmt.Errorf("toSelectable iface conversion: %w", err)
		}
		className := obj.Type().Name()
		class, ok = s.ObjectType(className)
		if ok {
			shared := val.cacheSharedResult()
			frame := shared.loadResultCall()
			if shared == nil || frame == nil {
				return nil, fmt.Errorf("toSelectable iface conversion: missing result call frame")
			}
			val, err = NewResultForCall(obj, frame)
			if err != nil {
				return nil, fmt.Errorf("toSelectable iface conversion: %w", err)
			}
			return class.New(val)
		}
	}

	return nil, fmt.Errorf("toSelectable: unknown type %q", val.Type().Name())
}

func (s *Server) parseASTSelections(ctx context.Context, gqlOp *graphql.OperationContext, self *ast.Type, astSels ast.SelectionSet) ([]Selection, error) {
	vars := gqlOp.Variables

	class := s.objects[self.Name()]
	if class == nil {
		return nil, fmt.Errorf("parseASTSelections: not an Object type: %q", self.Name())
	}

	sels := []Selection{}
	for _, sel := range astSels {
		switch x := sel.(type) {
		case *ast.Field:
			sel, resType, err := class.ParseField(ctx, s.View, x, vars)
			if err != nil {
				return nil, fmt.Errorf("parse field %q: %w", x.Name, err)
			}
			var subsels []Selection
			if len(x.SelectionSet) > 0 {
				subsels, err = s.parseASTSelections(ctx, gqlOp, resType, x.SelectionSet)
				if err != nil {
					return nil, err
				}
			}

			sels = append(sels, Selection{
				Alias:         x.Alias,
				Selector:      sel,
				Subselections: subsels,
			})
		case *ast.FragmentSpread:
			fragment := gqlOp.Doc.Fragments.ForName(x.Name)
			if fragment == nil {
				return nil, fmt.Errorf("unknown fragment: %s", x.Name)
			}
			if len(fragment.SelectionSet) > 0 {
				subsels, err := s.parseASTSelections(ctx, gqlOp, self, fragment.SelectionSet)
				if err != nil {
					return nil, err
				}
				sels = append(sels, subsels...)
			}
		default:
			return nil, fmt.Errorf("unknown field type: %T", x)
		}
	}

	return sels, nil
}

// Selection represents a selection of a field on an object.
type Selection struct {
	Alias         string
	Selector      Selector
	Subselections []Selection
}

// Name returns the name of the selection, which is either the alias or the
// field name.
func (sel Selection) Name() string {
	if sel.Alias != "" {
		return sel.Alias
	}
	return sel.Selector.Field
}

// Selector specifies how to retrieve a value from an Result.
type Selector struct {
	Field string
	Args  []NamedInput
	Nth   int
	View  call.View
}

func (sel Selector) String() string {
	str := sel.Field
	if len(sel.Args) > 0 {
		str += "("
		for i, arg := range sel.Args {
			if i > 0 {
				str += ", "
			}
			str += arg.String()
		}
		str += ")"
	}
	if sel.Nth != 0 {
		str += fmt.Sprintf("[%d]", sel.Nth)
	}
	return str
}

type Inputs []NamedInput

func (args Inputs) Lookup(name string) (Input, bool) {
	for _, arg := range args {
		if arg.Name == name {
			return arg.Value, true
		}
	}
	return nil, false
}

type NamedInput struct {
	Name  string
	Value Input
}

func (arg NamedInput) String() string {
	return fmt.Sprintf("%s: %v", arg.Name, arg.Value.ToLiteral().ToAST())
}

type DecoderFunc func(any) (Input, error)

var _ InputDecoder = DecoderFunc(nil)

func (f DecoderFunc) DecodeInput(val any) (Input, error) {
	return f(val)
}

type InputObject[T Type] struct {
	Value  T
	fields []inputObjectField
}

var _ Input = InputObject[Type]{} // TODO

func (InputObject[T]) Type() *ast.Type {
	var zero T
	return &ast.Type{
		NamedType: zero.TypeName(),
		NonNull:   true,
	}
}

func (InputObject[T]) Decoder() InputDecoder {
	return DecoderFunc(func(val any) (Input, error) {
		vals, ok := val.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("expected map[string]any, got %T", val)
		}
		var obj T
		fields, err := setInputObjectFields(&obj, vals)
		if err != nil {
			return nil, err
		}
		return InputObject[T]{
			Value:  obj,
			fields: fields,
		}, nil
	})
}

type inputObjectField struct {
	name  string
	value Input
}

func (input InputObject[T]) resultCallInputObjectFields() []inputObjectField {
	return input.fields
}

func setInputObjectFields(obj any, vals map[string]any) ([]inputObjectField, error) {
	objT := reflect.TypeOf(obj).Elem()
	objV := reflect.ValueOf(obj)
	if objT.Kind() != reflect.Struct {
		// TODO handle pointer?
		return nil, fmt.Errorf("object must be a struct, got %T", obj)
	}
	fields := make([]inputObjectField, 0, objT.NumField())
	for i := range objT.NumField() {
		fieldT := objT.Field(i)
		fieldV := objV.Elem().Field(i)
		name := fieldT.Tag.Get("name")
		if name == "" {
			name = strcase.ToLowerCamel(fieldT.Name)
		}
		if name == "-" {
			continue
		}
		fieldI := fieldV.Interface()
		if fieldT.Anonymous {
			// embedded struct
			val := reflect.New(fieldT.Type)
			embeddedFields, err := setInputObjectFields(val.Interface(), vals)
			if err != nil {
				return nil, err
			}
			fieldV.Set(val.Elem())
			fields = append(fields, embeddedFields...)
			continue
		}
		zeroInput, err := builtinOrInput(fieldI)
		if err != nil {
			return nil, fmt.Errorf("arg %q: %w", fieldT.Name, err)
		}
		var input Input
		if val, ok := vals[name]; ok {
			var err error
			input, err = zeroInput.Decoder().DecodeInput(val)
			if err != nil {
				return nil, err
			}
		} else if inputDefStr, hasDefault := fieldT.Tag.Lookup("default"); hasDefault {
			var err error
			input, err = zeroInput.Decoder().DecodeInput(inputDefStr)
			if err != nil {
				return nil, fmt.Errorf("convert default value for arg %s: %w", name, err)
			}
		} else if zeroInput.Type().NonNull {
			return nil, fmt.Errorf("missing required input field %q", name)
		}
		if input != nil { // will be nil for optional fields
			if err := assign(fieldV, input); err != nil {
				return nil, fmt.Errorf("assign input object %q as %+v (%T): %w", fieldT.Name, input, input, err)
			}
			fields = append(fields, inputObjectField{name: name, value: input})
		}
	}
	return fields, nil
}

func (input InputObject[T]) ToLiteral() call.Literal {
	if input.fields == nil {
		panic(fmt.Errorf("input object %T is missing decoded fields", input.Value))
	}
	args := make([]*call.Argument, 0, len(input.fields))
	for _, field := range input.fields {
		args = append(args, call.NewArgument(field.name, field.value.ToLiteral(), false))
	}
	return call.NewLiteralObject(args...)
}
