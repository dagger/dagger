package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"sync"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/errcode"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/iancoleman/strcase"
	"github.com/opencontainers/go-digest"
	"github.com/sourcegraph/conc/pool"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"
	"github.com/vektah/gqlparser/v2/validator/rules"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/dagql/call"
)

func init() {
	// HACK: these rules are disabled because some clients don't send the right
	// types:
	//   - PHP + Elixir SDKs send enums quoted
	//   - The shell sends enums quoted, and ints/floats as strings
	//   - etc
	validator.RemoveRule(rules.ValuesOfCorrectTypeRule.Name)
	validator.RemoveRule(rules.ValuesOfCorrectTypeRuleWithoutSuggestions.Name)

	// HACK: this rule is disabled because PHP modules <=v0.15.2 query
	// inputArgs incorrectly.
	validator.RemoveRule(rules.ScalarLeafsRule.Name)
}

// Server represents a GraphQL server whose schema is dynamically modified at
// runtime.
type Server struct {
	root        Object
	telemetry   AroundFunc
	objects     map[string]ObjectType
	scalars     map[string]ScalarType
	typeDefs    map[string]TypeDef
	directives  map[string]DirectiveSpec
	installLock *sync.Mutex
	// FIXME: implement stacked middleware
	middleware Middleware

	// View is the view that is applied to all queries on this server
	View string

	// Cache is the inner cache used by the server. It can be replicated to
	// another *Server to inherit and share caches.
	//
	// TODO: copy-on-write
	Cache Cache
}

type Middleware interface {
	InstallObject(ObjectType, func(ObjectType))
	// FIXME: add support for other install functions
}

// AroundFunc is a function that is called around every non-cached selection.
//
// It's a little funny looking. I may have goofed it. This will be cleaned up
// soon.
type AroundFunc func(
	context.Context,
	Object,
	*call.ID,
) (context.Context, func(res Typed, cached bool, err error))

// Cache stores results of pure selections against Server.
type Cache interface {
	GetOrInitialize(
		context.Context,
		digest.Digest,
		func(context.Context) (Typed, error),
	) (Typed, bool, error)
}

// TypeDef is a type whose sole practical purpose is to define a GraphQL type,
// so it explicitly includes the Definitive interface.
type TypeDef interface {
	Type
	Definitive
}

// NewServer returns a new Server with the given root object.
func NewServer[T Typed](root T) *Server {
	rootClass := NewClass(ClassOpts[T]{
		// NB: there's nothing actually stopping this from being a thing, except it
		// currently confuses the Dagger Go SDK. could be a nifty way to pass
		// around global config I suppose.
		NoIDs: true,
	})
	srv := &Server{
		Cache: NewCache(),
		root: Instance[T]{
			Self:  root,
			Class: rootClass,
		},
		objects:     map[string]ObjectType{},
		scalars:     map[string]ScalarType{},
		typeDefs:    map[string]TypeDef{},
		directives:  map[string]DirectiveSpec{},
		installLock: &sync.Mutex{},
	}
	srv.InstallObject(rootClass)
	for _, scalar := range coreScalars {
		srv.InstallScalar(scalar)
	}
	for _, directive := range coreDirectives {
		srv.InstallDirective(directive)
	}
	return srv
}

func NewDefaultHandler(es graphql.ExecutableSchema) *handler.Server {
	// TODO: avoid this deprecated method, and customize the options
	return handler.NewDefaultServer(es) //nolint: staticcheck
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
		Args: []InputSpec{
			{
				Name: "reason",
				Description: FormatDescription(
					`Explains why this element was deprecated, usually also including a
					suggestion for how to access supported similar data. Formatted in
					[Markdown](https://daringfireball.net/projects/markdown/).`),
				Type:    String(""),
				Default: String("No longer supported"),
			},
		},
		Locations: []DirectiveLocation{
			DirectiveLocationFieldDefinition,
			DirectiveLocationArgumentDefinition,
			DirectiveLocationInputFieldDefinition,
			DirectiveLocationEnumValue,
		},
	},
	{
		Name: "impure",
		Description: FormatDescription(
			`Indicates that a field may resolve to different values when called
			repeatedly with the same inputs, or that the field has side effects.
			Impure fields are never cached.`),
		Args: []InputSpec{
			{
				Name: "reason",
				Description: FormatDescription(
					`Explains why this element is impure, i.e. whether it performs side
					effects or yield a different result with the same arguments.`),
				Type: String(""),
			},
		},
		Locations: []DirectiveLocation{
			DirectiveLocationFieldDefinition,
		},
	},
	{
		Name: "meta",
		Description: FormatDescription(
			`Indicates that a field's selection can be removed from any query without
			changing the result. Meta fields are dropped from cache keys.`),
		Locations: []DirectiveLocation{
			DirectiveLocationFieldDefinition,
		},
	},
	{
		Name:        "sourceMap",
		Description: FormatDescription(`Indicates the source information for where a given field is defined.`),
		Args: []InputSpec{
			{
				Name: "module",
				Type: String(""),
			},
			{
				Name: "filename",
				Type: String(""),
			},
			{
				Name: "line",
				Type: Int(0),
			}, {
				Name: "column",
				Type: Int(0),
			},
		},
		Locations: []DirectiveLocation{
			DirectiveLocationScalar,
			DirectiveLocationObject,
			DirectiveLocationFieldDefinition,
			DirectiveLocationArgumentDefinition,
			DirectiveLocationUnion,
			DirectiveLocationEnum,
			DirectiveLocationEnumValue,
			DirectiveLocationInputObject,
		},
	},
}

// Root returns the root object of the server. It is suitable for passing to
// Resolve to resolve a query.
func (s *Server) Root() Object {
	return s.root
}

type Loadable interface {
	Load(context.Context, *Server) (Typed, error)
}

// InstallObject installs the given Object type into the schema.
func (s *Server) InstallObject(class ObjectType) {
	// FIXME: shortcut to get our "agent" middleware to work
	// s.installLock.Lock()
	// defer s.installLock.Unlock()
	s.installObject(class)
}

func (s *Server) installObject(o ObjectType) {
	install := func(class ObjectType) {
		s.objects[class.TypeName()] = class
		if idType, hasID := class.IDType(); hasID {
			s.scalars[idType.TypeName()] = idType
			s.Root().ObjectType().Extend(
				FieldSpec{
					Name:           fmt.Sprintf("load%sFromID", class.TypeName()),
					Description:    fmt.Sprintf("Load a %s from its ID.", class.TypeName()),
					Type:           class.Typed(),
					ImpurityReason: "The given ID ultimately determines the purity of its result.",
					Args: []InputSpec{
						{
							Name: "id",
							Type: idType,
						},
					},
				},
				func(ctx context.Context, self Object, args map[string]Input) (Typed, error) {
					idable, ok := args["id"].(IDable)
					if !ok {
						return nil, fmt.Errorf("expected IDable, got %T", args["id"])
					}
					id := idable.ID()
					if id.Type().ToAST().NamedType != class.TypeName() {
						return nil, fmt.Errorf("expected ID of type %q, got %q", class.TypeName(), id.Type().ToAST().NamedType)
					}
					res, err := s.Load(ctx, idable.ID())
					if err != nil {
						return nil, fmt.Errorf("load: %w", err)
					}
					return res, nil
				},
				nil,
			)
		}
	}
	if middleware := s.middleware; middleware != nil {
		middleware.InstallObject(o, install)
	} else {
		install(o)
	}
}

// InstallScalar installs the given Scalar type into the schema.
func (s *Server) InstallScalar(scalar ScalarType) {
	s.installLock.Lock()
	defer s.installLock.Unlock()
	s.scalars[scalar.TypeName()] = scalar
}

// InstallDirective installs the given Directive type into the schema.
func (s *Server) InstallDirective(directive DirectiveSpec) {
	s.installLock.Lock()
	defer s.installLock.Unlock()
	s.directives[directive.Name] = directive
}

// InstallTypeDef installs an arbitrary type definition into the schema.
func (s *Server) InstallTypeDef(def TypeDef) {
	s.installLock.Lock()
	defer s.installLock.Unlock()
	s.typeDefs[def.TypeName()] = def
}

// ObjectType returns the ObjectType with the given name, if it exists.
func (s *Server) ObjectType(name string) (ObjectType, bool) {
	s.installLock.Lock()
	defer s.installLock.Unlock()
	t, ok := s.objects[name]
	return t, ok
}

// ScalarType returns the ScalarType with the given name, if it exists.
func (s *Server) ScalarType(name string) (ScalarType, bool) {
	s.installLock.Lock()
	defer s.installLock.Unlock()
	t, ok := s.scalars[name]
	return t, ok
}

// InputType returns the InputType with the given name, if it exists.
func (s *Server) TypeDef(name string) (TypeDef, bool) {
	s.installLock.Lock()
	defer s.installLock.Unlock()
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
	return s.ExecOp(ctx, &graphql.OperationContext{
		RawQuery:  query,
		Variables: vars,
	})
}

var _ graphql.ExecutableSchema = (*Server)(nil)

// Schema returns the current schema of the server.
func (s *Server) Schema() *ast.Schema { // TODO: change this to be updated whenever something is installed, instead
	s.installLock.Lock()
	defer s.installLock.Unlock()
	// TODO track when the schema changes, cache until it changes again
	queryType := s.Root().Type().Name()
	schema := &ast.Schema{
		Types:         make(map[string]*ast.Definition),
		PossibleTypes: make(map[string][]*ast.Definition),
	}
	for _, t := range s.objects { // TODO stable order
		def := definition(ast.Object, t, s.View)
		if def.Name == queryType {
			schema.Query = def
		}
		schema.AddTypes(def)
		schema.AddPossibleType(def.Name, def)
	}
	for _, t := range s.scalars {
		def := definition(ast.Scalar, t, s.View)
		schema.AddTypes(def)
		schema.AddPossibleType(def.Name, def)
	}
	for _, t := range s.typeDefs {
		def := t.TypeDefinition(s.View)
		schema.AddTypes(def)
		schema.AddPossibleType(def.Name, def)
	}
	schema.Directives = map[string]*ast.DirectiveDefinition{}
	for n, d := range s.directives {
		schema.Directives[n] = d.DirectiveDefinition()
	}
	return schema
}

// Complexity returns the complexity of the given field.
func (s *Server) Complexity(typeName, field string, childComplexity int, args map[string]interface{}) (int, bool) {
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

func (s *Server) ExecOp(ctx context.Context, gqlOp *graphql.OperationContext) (map[string]any, error) {
	if gqlOp.Doc == nil {
		var err error
		gqlOp.Doc, err = parser.ParseQuery(&ast.Source{Input: gqlOp.RawQuery})
		if err != nil {
			return nil, gqlErrs(err)
		}

		listErr := validator.Validate(s.Schema(), gqlOp.Doc)
		if len(listErr) != 0 {
			for _, e := range listErr {
				errcode.Set(e, errcode.ValidationFailed)
			}
			return nil, listErr
		}
	}
	results := make(map[string]any)
	for _, op := range gqlOp.Doc.Operations {
		switch op.Operation {
		case ast.Query:
			if gqlOp.OperationName != "" && gqlOp.OperationName != op.Name {
				continue
			}
			sels, err := s.parseASTSelections(ctx, gqlOp, s.root.Type(), op.SelectionSet)
			if err != nil {
				return nil, fmt.Errorf("query:\n%s\n\nerror: parse selections: %w", gqlOp.RawQuery, err)
			}
			results, err = s.Resolve(ctx, s.root, sels...)
			if err != nil {
				return nil, err
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
func (s *Server) Resolve(ctx context.Context, self Object, sels ...Selection) (map[string]any, error) {
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
func (s *Server) Load(ctx context.Context, id *call.ID) (Object, error) {
	var base Object
	var err error
	if id.Receiver() != nil {
		base, err = s.Load(ctx, id.Receiver())
		if err != nil {
			return nil, fmt.Errorf("load base: %w", err)
		}
	} else {
		base = s.root
	}

	res, id, err := base.Call(ctx, s, id)
	if err != nil {
		return nil, fmt.Errorf("load: %w", err)
	}
	return s.toSelectable(id, res)
}

// Select evaluates a series of chained field selections starting from the
// given object and assigns the final result value into dest.
func (s *Server) Select(ctx context.Context, self Object, dest any, sels ...Selector) error {
	// Annotate ctx with the internal flag so we can distinguish self-calls from
	// user-calls in the UI.
	ctx = withInternal(ctx)

	var res Typed = self
	var id *call.ID
	for i, sel := range sels {
		var err error
		res, id, err = self.Select(ctx, s, sel)
		if err != nil {
			return fmt.Errorf("select: %w", err)
		}

		if _, ok := s.ObjectType(res.Type().Name()); ok {
			enum, isEnum := res.(Enumerable)
			if sel.Nth != 0 {
				if !isEnum {
					return fmt.Errorf("nth used on non enumerable %s", res.Type())
				}
				res, err = enum.Nth(sel.Nth)
				if err != nil {
					return fmt.Errorf("selector nth %d: %w", sel.Nth, err)
				}
			} else if isEnum {
				// HACK: list of objects must be the last selection right now unless nth used in Selector.
				if i+1 < len(sels) {
					return fmt.Errorf("cannot sub-select enum of %s", res.Type())
				}
				for nth := 1; nth <= enum.Len(); nth++ {
					val, err := enum.Nth(nth)
					if err != nil {
						return fmt.Errorf("nth %d: %w", nth, err)
					}
					if wrapped, ok := val.(Derefable); ok {
						val, ok = wrapped.Deref()
						if !ok {
							if err := appendAssign(reflect.ValueOf(dest).Elem(), nil); err != nil {
								return err
							}
							continue
						}
					}
					nthID := id.SelectNth(nth)
					obj, err := s.toSelectable(nthID, val)
					if err != nil {
						return fmt.Errorf("select %dth array element: %w", nth, err)
					}
					if err := appendAssign(reflect.ValueOf(dest).Elem(), obj); err != nil {
						return err
					}
				}
				return nil
			}

			// if the result is an Object, set it as the next selection target, and
			// assign res to the "hydrated" Object
			self, err = s.toSelectable(id, res)
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

// Attach a middleware to hook into object installation
func (s *Server) SetMiddleware(middleware Middleware) {
	s.installLock.Lock()
	s.middleware = middleware
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
			out[i] = val.Self
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return out, err
	}
	return out, nil
}

type idCtx struct{}

func idToContext(ctx context.Context, id *call.ID) context.Context {
	return context.WithValue(ctx, idCtx{}, id)
}

func CurrentID(ctx context.Context) *call.ID {
	val := ctx.Value(idCtx{})
	if val == nil {
		return nil
	}
	return val.(*call.ID)
}

// NewInstanceForCurrentID creates a new Instance that's set to the current ID from
// the given self value.
func NewInstanceForCurrentID[P, T Typed](
	ctx context.Context,
	srv *Server,
	parent Instance[P],
	self T,
) (Instance[T], error) {
	return NewInstanceForID(srv, parent, self, CurrentID(ctx))
}

// NewInstanceForID creates a new Instance with the given ID and self value.
func NewInstanceForID[P, T Typed](
	srv *Server,
	parent Instance[P],
	self T,
	id *call.ID,
) (Instance[T], error) {
	if id == nil {
		return Instance[T]{}, errors.New("id is nil")
	}

	objType, ok := srv.ObjectType(self.Type().Name())
	if !ok {
		return Instance[T]{}, fmt.Errorf("unknown type %q", self.Type().Name())
	}
	class, ok := objType.(Class[T])
	if !ok {
		return Instance[T]{}, fmt.Errorf("not a Class: %T", objType)
	}

	// sanity check: verify the ID matches the type of self
	if id.Type().NamedType() != objType.TypeName() {
		return Instance[T]{}, fmt.Errorf("expected ID of type %q, got %q", objType.TypeName(), id.Type().NamedType())
	}

	return Instance[T]{
		Constructor: id,
		Self:        self,
		Class:       class,
		Module:      parent.Module,
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

func (s *Server) resolvePath(ctx context.Context, self Object, sel Selection) (res any, rerr error) {
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
			rerr = gqlErr(rerr, append(idToPath(self.ID()), ast.PathName(sel.Name())))
		}
	}()

	val, chainedID, err := self.Select(ctx, s, sel.Selector)
	if err != nil {
		return nil, err
	}

	if val == nil {
		// a nil value ignores all sub-selections
		return nil, nil
	}

	enum, ok := val.(Enumerable)
	if ok {
		// we're sub-selecting into an enumerable value, so we need to resolve each
		// element

		// TODO arrays of arrays
		results := []any{} // TODO subtle: favor [] over null result
		for nth := 1; nth <= enum.Len(); nth++ {
			val, err := enum.Nth(nth)
			if err != nil {
				return nil, err
			}
			if wrapped, ok := val.(Derefable); ok {
				val, ok = wrapped.Deref()
				if !ok {
					results = append(results, nil)
					continue
				}
			}
			if len(sel.Subselections) == 0 {
				results = append(results, val)
			} else {
				nthID := chainedID.SelectNth(nth)
				node, err := s.toSelectable(nthID, val)
				if err != nil {
					return nil, fmt.Errorf("instantiate %dth array element: %w", nth, err)
				}
				res, err := s.Resolve(ctx, node, sel.Subselections...)
				if err != nil {
					return nil, err
				}
				results = append(results, res)
			}
		}
		return results, nil
	}

	if len(sel.Subselections) == 0 {
		return val, nil
	}

	// instantiate the return value so we can sub-select
	node, err := s.toSelectable(chainedID, val)
	if err != nil {
		return nil, fmt.Errorf("instantiate: %w", err)
	}

	return s.Resolve(ctx, node, sel.Subselections...)
}

func (s *Server) toSelectable(chainedID *call.ID, val Typed) (Object, error) {
	if sel, ok := val.(Object); ok {
		// We always support returning something that's already Selectable, e.g. an
		// object loaded from its ID.
		return sel, nil
	}
	class, ok := s.ObjectType(val.Type().Name())
	if !ok {
		return nil, fmt.Errorf("toSelectable: unknown type %q", val.Type().Name())
	}
	return class.New(chainedID, val)
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

// Selector specifies how to retrieve a value from an Instance.
type Selector struct {
	Field string
	Args  []NamedInput
	Nth   int
	View  string

	// Override the default purity of the field. Typically used so that an impure
	// resolver call can return a pure result, by calling to itself or another
	// field with pure arguments.
	//
	// If Pure is false, and the field is marked Impure, the selection will not
	// be cached and the object's ID will be tainted.
	//
	// If Pure is true, the selection will be cached regardless of the field's
	// purity, and the object's ID will be untainted.
	Pure bool
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
	Value T
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
		if err := setInputObjectFields(&obj, vals); err != nil {
			return nil, err
		}
		return InputObject[T]{
			Value: obj,
		}, nil
	})
}

func setInputObjectFields(obj any, vals map[string]any) error {
	objT := reflect.TypeOf(obj).Elem()
	objV := reflect.ValueOf(obj)
	if objT.Kind() != reflect.Struct {
		// TODO handle pointer?
		return fmt.Errorf("object must be a struct, got %T", obj)
	}
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
			if err := setInputObjectFields(val.Interface(), vals); err != nil {
				return err
			}
			fieldV.Set(val.Elem())
			continue
		}
		zeroInput, err := builtinOrInput(fieldI)
		if err != nil {
			return fmt.Errorf("arg %q: %w", fieldT.Name, err)
		}
		var input Input
		if val, ok := vals[name]; ok {
			var err error
			input, err = zeroInput.Decoder().DecodeInput(val)
			if err != nil {
				return err
			}
		} else if inputDefStr, hasDefault := fieldT.Tag.Lookup("default"); hasDefault {
			var err error
			input, err = zeroInput.Decoder().DecodeInput(inputDefStr)
			if err != nil {
				return fmt.Errorf("convert default value for arg %s: %w", name, err)
			}
		} else if zeroInput.Type().NonNull {
			return fmt.Errorf("missing required input field %q", name)
		}
		if input != nil { // will be nil for optional fields
			if err := assignInputField(fieldV, input); err != nil {
				return fmt.Errorf("assign %q: %w", fieldT.Name, err)
			}
		}
	}
	return nil
}

func (input InputObject[T]) ToLiteral() call.Literal {
	obj := input.Value
	args, err := collectLiteralArgs(obj)
	if err != nil {
		panic(fmt.Errorf("collectLiteralArgs: %w", err))
	}
	return call.NewLiteralObject(args...)
}

func collectLiteralArgs(obj any) ([]*call.Argument, error) {
	objT := reflect.TypeOf(obj)
	objV := reflect.ValueOf(obj)
	if objV.Kind() != reflect.Struct {
		// TODO handle pointer?
		return nil, fmt.Errorf("object must be a struct, got %T", obj)
	}
	args := []*call.Argument{}
	for i := range objV.NumField() {
		fieldT := objT.Field(i)
		name := fieldT.Tag.Get("name")
		if name == "" {
			name = strcase.ToLowerCamel(fieldT.Name)
		}
		if name == "-" {
			continue
		}
		fieldI := objV.Field(i).Interface()
		if fieldT.Anonymous {
			subArgs, err := collectLiteralArgs(fieldI)
			if err != nil {
				return nil, fmt.Errorf("arg %q: %w", fieldT.Name, err)
			}
			args = append(args, subArgs...)
			continue
		}
		input, err := builtinOrInput(fieldI)
		if err != nil {
			return nil, fmt.Errorf("arg %q: %w", fieldT.Name, err)
		}
		args = append(args, call.NewArgument(
			name,
			input.ToLiteral(),
			false,
		))
	}
	return args, nil
}

// assignInputField is like assign but handles Optional type wrapping/unwrapping.
func assignInputField(fieldV reflect.Value, input Input) error {
	// If the field isn't settable, just return an error
	if !fieldV.CanSet() {
		return fmt.Errorf("field is not settable")
	}

	// If input is nil, it usually means the user omitted it
	if input == nil {
		// Check if the destination is an Optional[…]
		if isOptionalType(fieldV.Type()) {
			// Set it to Optional{Valid: false}
			optVal := reflect.New(fieldV.Type()).Elem()
			fieldV.Set(optVal)
		}
		return nil
	}

	// Reflect on the field's actual type
	fieldType := fieldV.Type()
	inputVal := reflect.ValueOf(input)

	// If the field is an Optional[…], handle that logic:
	if isOptionalType(fieldType) {
		// Is input itself already an Optional?
		if inputVal.Type() == fieldType {
			// They match: just assign directly
			fieldV.Set(inputVal)
			return nil
		}

		// Otherwise, the user provided a bare T, but field requires Optional[T].
		// So create an Optional[T] with Valid = true
		optVal := reflect.New(fieldType).Elem()
		valueField := optVal.FieldByName("Value")
		validField := optVal.FieldByName("Valid")

		// Set .Value = input, .Valid = true
		valueField.Set(inputVal)
		validField.SetBool(true)

		fieldV.Set(optVal)
		return nil
	}

	// If the field is not optional, but the user gave an Optional, unwrap it
	if isOptionalType(inputVal.Type()) {
		// Suppose input is Optional[T], but field is T
		validField := inputVal.FieldByName("Valid").Bool()
		if !validField {
			// Optional is not valid => user explicitly gave null => zero value
			fieldV.Set(reflect.Zero(fieldType))
			return nil
		}

		// If valid => unwrap .Value
		unwrapped := inputVal.FieldByName("Value")
		if !unwrapped.Type().AssignableTo(fieldType) {
			return fmt.Errorf("cannot unwrap optional type %s into %s", unwrapped.Type(), fieldType)
		}
		fieldV.Set(unwrapped)
		return nil
	}

	// If none of the optional logic applies, and the types already match, assign directly
	if inputVal.Type().AssignableTo(fieldType) {
		fieldV.Set(inputVal)
		return nil
	}

	// Otherwise, we can't assign
	return fmt.Errorf("cannot assign %T into field of type %s", input, fieldType)
}

// isOptionalType checks if typ is something like dagql.Optional[…]
func isOptionalType(typ reflect.Type) bool {
	if typ.Kind() == reflect.Struct && typ.Name() == "Optional" {
		return true
	}
	return false
}
