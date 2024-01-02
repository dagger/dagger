package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"github.com/99designs/gqlgen/graphql"
	"github.com/iancoleman/strcase"
	"github.com/opencontainers/go-digest"
	"github.com/sourcegraph/conc/pool"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vito/dagql/idproto"
)

type TelemetryFunc func(context.Context, *idproto.ID) (context.Context, func(error))

// Server represents a GraphQL server whose schema is dynamically modified at
// runtime.
type Server struct {
	root        Object
	telemetry   TelemetryFunc
	objects     map[string]ObjectType
	scalars     map[string]ScalarType
	typeDefs    map[string]TypeDef
	installLock *sync.Mutex

	// Cache is the inner cache used by the server. It can be replicated to
	// another *Server to inherit and share caches.
	//
	// TODO: copy-on-write
	Cache Cache
}

// Cache stores results of pure selections against Server.
type Cache interface {
	GetOrInitialize(
		context.Context,
		digest.Digest,
		func(context.Context) (Typed, error),
	) (Typed, error)
}

// TypeDef is a type whose sole practical purpose is to define a GraphQL type,
// so it explicitly includes the Definitive interface.
type TypeDef interface {
	Type
	Definitive
}

var coreScalars = []ScalarType{
	Boolean(false),
	Int(0),
	Float(0),
	String(""),
	// instead of a single ID type, each object has its own ID type
	// ID{},
}

// NewServer returns a new Server with the given root object.
func NewServer[T Typed](root T) *Server {
	rootClass := NewClass[T]()
	srv := &Server{
		Cache: NewCache(),

		root: Instance[T]{
			Self:  root,
			Class: rootClass,
		},
		objects:     map[string]ObjectType{},
		scalars:     map[string]ScalarType{},
		typeDefs:    map[string]TypeDef{},
		installLock: &sync.Mutex{},
	}
	srv.InstallObject(rootClass)
	for _, scalar := range coreScalars {
		srv.InstallScalar(scalar)
	}
	return srv
}

// Merge installs every type from the other Server into this server.
//
// In the event of a conflict, an error is returned.
//
// Built-in types (Boolean, Int, Float, String) are not merged.
func (s *Server) Merge(other *Server, merge func(a, b Type) Type) {
	s.installLock.Lock()
	defer s.installLock.Unlock()
	other.installLock.Lock()
	defer other.installLock.Unlock()
	for name, obj := range other.objects {
		s.objects[name] = obj
	}
	for name, scalar := range other.scalars {
		s.scalars[name] = scalar
	}
	for name, typeDef := range other.typeDefs {
		s.typeDefs[name] = typeDef
	}
}

// Root returns the root object of the server. It is suitable for passing to
// Resolve to resolve a query.
func (s *Server) Root() Object {
	return s.root
}

// InstallObject installs the given Object type into the schema.
func (s *Server) InstallObject(class ObjectType) {
	s.installLock.Lock()
	defer s.installLock.Unlock()
	s.objects[class.TypeName()] = class
}

// InstallScalar installs the given Scalar type into the schema.
func (s *Server) InstallScalar(scalar ScalarType) {
	s.installLock.Lock()
	defer s.installLock.Unlock()
	s.scalars[scalar.TypeName()] = scalar
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

func (s *Server) RecordTo(rec TelemetryFunc) {
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
	schema := &ast.Schema{}
	for _, t := range s.objects { // TODO stable order
		def := definition(ast.Object, t)
		if def.Name == queryType {
			schema.Query = def
		}
		schema.AddTypes(def)
	}
	for _, t := range s.scalars {
		schema.AddTypes(definition(ast.Scalar, t))
	}
	for _, t := range s.typeDefs {
		schema.AddTypes(t.Definition())
	}
	return schema
}

// Complexity returns the complexity of the given field.
func (s *Server) Complexity(typeName, field string, childComplexity int, args map[string]interface{}) (int, bool) {
	// TODO
	return 1, false
}

// Exec implements graphql.ExecutableSchema.
func (s *Server) Exec(ctx1 context.Context) graphql.ResponseHandler {
	return func(ctx context.Context) *graphql.Response {
		gqlOp := graphql.GetOperationContext(ctx)

		if err := gqlOp.Validate(ctx); err != nil {
			return graphql.ErrorResponse(ctx, "validate: %s", err)
		}

		results, err := s.ExecOp(ctx, gqlOp)
		if err != nil {
			gqlOp.Error(ctx, err)
			return graphql.ErrorResponse(ctx, "exec: %s", err)
		}

		data, err := json.Marshal(results)
		if err != nil {
			gqlOp.Error(ctx, err)
			return graphql.ErrorResponse(ctx, "marshal: %s", err)
		}

		return &graphql.Response{
			Data: json.RawMessage(data),
		}
	}
}

func (s *Server) ExecOp(ctx context.Context, gqlOp *graphql.OperationContext) (map[string]any, error) {
	if gqlOp.Doc == nil {
		var err error
		gqlOp.Doc, err = parser.ParseQuery(&ast.Source{Input: gqlOp.RawQuery})
		if err != nil {
			return nil, err
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
				return nil, fmt.Errorf("resolve: %w", err)
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

	pool := new(pool.ErrorPool)
	for _, sel := range sels {
		sel := sel
		pool.Go(func() error {
			res, err := s.resolvePath(ctx, self, sel)
			if err != nil {
				return fmt.Errorf("%s: %w", sel.Name(), err)
			}
			results.Store(sel.Name(), res)
			return nil
		})
	}
	if err := pool.Wait(); err != nil {
		return nil, err
	}

	resultsMap := make(map[string]any)
	results.Range(func(key, value any) bool {
		resultsMap[key.(string)] = value
		return true
	})
	return resultsMap, nil
}

// Load loads the object with the given ID.
func (s *Server) Load(ctx context.Context, id *idproto.ID) (Object, error) {
	var base Object
	var err error
	if id.Parent != nil {
		base, err = s.Load(ctx, id.Parent)
		if err != nil {
			return nil, err
		}
	} else {
		base = s.root
	}
	selfType := base.Type()
	astField := &ast.Field{
		Name: id.Field,
	}
	vars := map[string]any{}
	for _, arg := range id.Args {
		vars[arg.Name] = arg.Value.ToInput()
		astField.Arguments = append(astField.Arguments, &ast.Argument{
			Name: arg.Name,
			Value: &ast.Value{
				Kind: ast.Variable,
				Raw:  arg.Name,
			},
		})
	}
	class, ok := s.objects[selfType.Name()]
	if !ok {
		return nil, fmt.Errorf("constructorToSelection: unknown type: %q", selfType.Name())
	}
	sel, _, err := class.ParseField(ctx, astField, vars)
	if err != nil {
		return nil, err
	}
	sel.Nth = int(id.Nth)
	res, id, err := s.cachedSelect(ctx, base, sel)
	if err != nil {
		return nil, err
	}
	return s.toSelectable(id, res)
}

func (s *Server) Select(ctx context.Context, self Object, dest any, sels ...Selector) error {
	for _, sel := range sels {
		res, id, err := s.cachedSelect(ctx, self, sel)
		if err != nil {
			return err
		}
		self, err = s.toSelectable(id, res)
		if err != nil {
			return err
		}
	}
	return assign(reflect.ValueOf(dest).Elem(), self)
}

func LoadIDs[T Typed](ctx context.Context, srv *Server, ids []ID[T]) ([]T, error) {
	var out []T
	for _, id := range ids {
		// TODO(vito): parallelize, in case these IDs haven't been seen before
		val, err := id.Load(ctx, srv)
		if err != nil {
			return nil, err
		}
		out = append(out, val.Self)
	}
	return out, nil
}

type idCtx struct{}

func idToContext(ctx context.Context, id *idproto.ID) context.Context {
	return context.WithValue(ctx, idCtx{}, id)
}

func CurrentID(ctx context.Context) *idproto.ID {
	val := ctx.Value(idCtx{})
	if val == nil {
		return nil
	}
	return val.(*idproto.ID)
}

func (s *Server) cachedSelect(ctx context.Context, self Object, sel Selector) (res Typed, chained *idproto.ID, rerr error) {
	chainedID, err := self.IDFor(ctx, sel)
	if err != nil {
		return nil, nil, err
	}
	ctx = idToContext(ctx, chainedID)
	dig, err := chainedID.Canonical().Digest()
	if err != nil {
		return nil, nil, err
	}
	if s.telemetry != nil {
		var done func(error)
		wrapped, done := s.telemetry(ctx, chainedID)
		defer func() {
			done(rerr)
		}()
		ctx = wrapped
	}
	var val Typed
	if chainedID.IsTainted() {
		val, err = self.Select(ctx, sel)
	} else {
		val, err = s.Cache.GetOrInitialize(ctx, dig, func(ctx context.Context) (Typed, error) {
			return self.Select(ctx, sel)
		})
	}
	if err != nil {
		return nil, nil, err
	}
	return val, chainedID, nil
}

func (s *Server) resolvePath(ctx context.Context, self Object, sel Selection) (res any, rerr error) {
	val, chainedID, err := s.cachedSelect(ctx, self, sel.Selector)
	if err != nil {
		return nil, err
	}

	if val == nil {
		// a nil value ignores all sub-selections
		return nil, nil
	}

	if len(sel.Subselections) == 0 {
		return val, nil
		// if lit, ok := val.(idproto.Literate); ok {
		// 	// there are no sub-selections; we're done
		// 	return lit.ToLiteral().ToInput(), nil
		// }
		// return nil, fmt.Errorf("%s must have sub-selections", val.Type())
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
			if wrapped, ok := val.(NullableWrapper); ok {
				val, ok = wrapped.Unwrap()
				if !ok {
					results = append(results, nil)
					continue
				}
			}
			node, err := s.toSelectable(chainedID.SelectNth(nth), val)
			if err != nil {
				return nil, fmt.Errorf("instantiate %dth array element: %w", nth, err)
			}
			res, err := s.Resolve(ctx, node, sel.Subselections...)
			if err != nil {
				return nil, err
			}
			results = append(results, res)
		}
		return results, nil
	}

	// instantiate the return value so we can sub-select
	node, err := s.toSelectable(chainedID, val)
	if err != nil {
		return nil, fmt.Errorf("instantiate: %w", err)
	}

	return s.Resolve(ctx, node, sel.Subselections...)
}

func (s *Server) toSelectable(chainedID *idproto.ID, val Typed) (Object, error) {
	if sel, ok := val.(Object); ok {
		// We always support returning something that's already Selectable, e.g. an
		// object loaded from its ID.
		return sel, nil
	}
	class, ok := s.objects[val.Type().Name()]
	if !ok {
		return nil, fmt.Errorf("unknown type %q", val.Type().Name())
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
			sel, resType, err := class.ParseField(ctx, x, vars)
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

func (sel Selector) AppendTo(id *idproto.ID, astType *ast.Type, tainted bool) *idproto.ID {
	idArgs := make([]*idproto.Argument, 0, len(sel.Args))
	for _, arg := range sel.Args {
		if arg.Value == nil {
			// we don't include null arguments, since they would needlessly bust caches
			continue
		}
		idArgs = append(idArgs, &idproto.Argument{
			Name:  arg.Name,
			Value: arg.Value.ToLiteral(),
		})
	}
	// TODO: it's better DX if it matches schema order
	sort.Slice(idArgs, func(i, j int) bool {
		return idArgs[i].Name < idArgs[j].Name
	})
	appended := id.
		Append(
			astType,
			sel.Field,
			idArgs...,
		).
		WithTainted(tainted)
	if sel.Nth != 0 {
		appended = appended.SelectNth(sel.Nth)
	}
	return appended
}

type Inputs []NamedInput

func (args Inputs) Lookup(name string) (Typed, bool) {
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
	for i := 0; i < objT.NumField(); i++ {
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
			if err := assign(fieldV, input); err != nil {
				return fmt.Errorf("assign %q: %w", fieldT.Name, err)
			}
		}
	}
	return nil
}

func (input InputObject[T]) ToLiteral() *idproto.Literal {
	obj := input.Value
	args, err := collectLiteralArgs(obj)
	if err != nil {
		panic(fmt.Errorf("collectLiteralArgs: %w", err))
	}
	return &idproto.Literal{
		Value: &idproto.Literal_Object{
			Object: &idproto.Object{
				Values: args,
			},
		},
	}
}

func collectLiteralArgs(obj any) ([]*idproto.Argument, error) {
	objT := reflect.TypeOf(obj)
	objV := reflect.ValueOf(obj)
	if objV.Kind() != reflect.Struct {
		// TODO handle pointer?
		return nil, fmt.Errorf("object must be a struct, got %T", obj)
	}
	args := []*idproto.Argument{}
	for i := 0; i < objV.NumField(); i++ {
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
		args = append(args, &idproto.Argument{
			Name:  name,
			Value: input.ToLiteral(),
		})
	}
	return args, nil
}
