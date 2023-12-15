package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/99designs/gqlgen/graphql"
	"github.com/opencontainers/go-digest"
	"github.com/sourcegraph/conc/pool"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dagql/idproto"
	"github.com/vito/dagql/ioctx"
	"github.com/vito/progrock"
)

// Server represents a GraphQL server whose schema is dynamically modified at
// runtime.
type Server struct {
	root        Object
	rec         *progrock.Recorder
	classes     map[string]ObjectType
	scalars     map[string]ScalarType
	cache       *CacheMap[digest.Digest, Typed]
	installLock sync.Mutex
}

// NewServer returns a new Server with the given root object.
func NewServer[T Typed](root T) *Server {
	queryClass := NewClass[T]()
	srv := &Server{
		root: Instance[T]{
			Constructor: idproto.New(root.Type()),
			Self:        root,
			Class:       queryClass,
		},
		classes: map[string]ObjectType{
			root.Type().Name(): queryClass,
		},
		scalars: map[string]ScalarType{
			"Boolean": Boolean{},
			"Int":     Int{},
			"Float":   Float{},
			"String":  String{},
			// instead of a single ID type, each object has its own ID type
			// "ID": ID{},
		},
		cache: NewCacheMap[digest.Digest, Typed](),
	}
	return srv
}

func (s *Server) RecordTo(rec *progrock.Recorder) {
	s.rec = rec
}

// Root returns the root object of the server. It is suitable for passing to
// Resolve to resolve a query.
func (s *Server) Root() Object {
	return s.root
}

var _ graphql.ExecutableSchema = (*Server)(nil)

// Schema returns the current schema of the server.
func (s *Server) Schema() *ast.Schema {
	// TODO track when the schema changes, cache until it changes again
	queryType := s.Root().Type().Name()
	schema := &ast.Schema{}
	for _, t := range s.classes { // TODO stable order
		def := definition(ast.Object, t)
		if def.Name == queryType {
			schema.Query = def
		}
		schema.AddTypes(def)
	}
	for _, t := range s.scalars {
		schema.AddTypes(definition(ast.Scalar, t))
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

		doc := gqlOp.Doc

		results := make(map[string]any)
		for _, op := range doc.Operations {
			switch op.Operation {
			case ast.Query:
				// TODO prospective
				if gqlOp.OperationName != "" && gqlOp.OperationName != op.Name {
					continue
				}
				sels, err := s.parseASTSelections(gqlOp, s.root.Type(), op.SelectionSet)
				if err != nil {
					return graphql.ErrorResponse(ctx, "failed to convert selections: %s", err)
				}
				results, err = s.Resolve(ctx, s.root, sels...)
				if err != nil {
					return graphql.ErrorResponse(ctx, "failed to resolve: %s", err)
				}
			case ast.Mutation:
				// TODO
				return graphql.ErrorResponse(ctx, "mutations not supported")
			case ast.Subscription:
				// TODO
				return graphql.ErrorResponse(ctx, "subscriptions not supported")
			}
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

// resolveContext stores data in context.Context for use by Resolve.
//
// We might want to just turn this into args for the usual reasons, but it's
// for telemetry related things, which feels as appropriate a reason as any.
type resolveContext struct {
	Parent digest.Digest
}

type resolveContextKey struct{}

func parentFrom(ctx context.Context) (digest.Digest, bool) {
	val := ctx.Value(resolveContextKey{})
	if val == nil {
		return "", false
	}
	return val.(resolveContext).Parent, true
}

func withParent(ctx context.Context, parent digest.Digest) context.Context {
	cp, _ := ctx.Value(resolveContextKey{}).(resolveContext)
	cp.Parent = parent
	return context.WithValue(ctx, resolveContextKey{}, cp)
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
	if len(id.Constructor) == 0 {
		return s.root, nil
	}
	sel, err := s.constructorToSelection(ctx, s.root.Type(), id.Constructor[0], id.Constructor[1:]...)
	if err != nil {
		return nil, err
	}
	var res any
	res, err = s.Resolve(ctx, s.root, sel)
	if err != nil {
		return nil, err
	}
	for _, sel := range id.Constructor {
		switch x := res.(type) {
		case map[string]any:
			res = x[sel.Field]
		default:
			return nil, fmt.Errorf("unexpected result type %T", x)
		}
	}
	val, ok := res.(Typed)
	if !ok {
		// should be impossible, since Instance.Select returns a Typed
		return nil, fmt.Errorf("unexpected result type %T", res)
	}
	return s.toSelectable(id, val)
}

func (s *Server) resolvePath(ctx context.Context, self Object, sel Selection) (res any, rerr error) {
	class, ok := s.classes[self.Type().Name()]
	if !ok {
		return nil, fmt.Errorf("resolvePath: unknown type: %q", self.Type().Name())
	}
	fieldDef, ok := class.FieldDefinition(sel.Selector.Field)
	if fieldDef == nil {
		return nil, fmt.Errorf("resolvePath: unknown field: %q", sel.Selector.Field)
	}

	chainedID := sel.Selector.appendToID(self.ID(), fieldDef)

	dig, err := chainedID.Canonical().Digest()
	if err != nil {
		return nil, err
	}

	if s.rec != nil {
		// TODO: I actually don't think we even need this. When we visualize the ID
		// you can just take the digest of each input ID and correlate events that
		// way. The relationships are already expressed.
		inputs := []digest.Digest{}
		if parent, ok := parentFrom(ctx); ok {
			inputs = append(inputs, parent)
		}
		for _, arg := range sel.Selector.Args {
			if obj, ok := arg.Value.(Object); ok {
				argDig, err := obj.ID().Digest()
				if err != nil {
					return nil, err
				}
				inputs = append(inputs, argDig)
			}
		}
		vtx := s.rec.Vertex(dig, chainedID.Display(), progrock.WithInputs(inputs...))
		defer vtx.Done(rerr)
		ctx = ioctx.WithStdout(ctx, vtx.Stdout())
		ctx = ioctx.WithStderr(ctx, vtx.Stderr())
	}

	ctx = withParent(ctx, dig)

	val, err := self.Select(ctx, sel.Selector)
	if err != nil {
		return nil, err
	}

	if val == nil {
		res = nil
	} else if len(sel.Subselections) == 0 {
		res = val
	} else if len(sel.Subselections) > 0 {
		enum, ok := val.(Enumerable)
		if !ok {
			node, err := s.toSelectable(chainedID, val)
			if err != nil {
				return nil, fmt.Errorf("instantiate: %w", err)
			}
			res, err = s.Resolve(ctx, node, sel.Subselections...)
			if err != nil {
				return nil, err
			}
		} else {
			// TODO arrays of arrays
			results := []any{} // TODO subtle: favor [] over null result
			for nth := 1; nth <= enum.Len(); nth++ {
				val, err := enum.Nth(nth)
				if err != nil {
					return nil, err
				}
				if wrapped, ok := val.(NullableWrapper); ok { // TODO unfortunate that we need this here too
					val, ok = wrapped.Unwrap()
					if !ok {
						results = append(results, nil)
						continue
					}
				}
				node, err := s.toSelectable(chainedID.Nth(nth), val)
				if err != nil {
					return nil, fmt.Errorf("instantiate %dth array element: %w", nth, err)
				}
				res, err := s.Resolve(ctx, node, sel.Subselections...)
				if err != nil {
					return nil, err
				}
				results = append(results, res)
			}
			res = results
		}
	}

	return res, nil
}

func (s *Server) constructorToSelection(ctx context.Context, selfType *ast.Type, first *idproto.Selector, rest ...*idproto.Selector) (Selection, error) {
	sel := Selection{
		Selector: Selector{
			Field: first.Field,
			Nth:   int(first.Nth),
		},
	}
	class, ok := s.classes[selfType.Name()]
	if !ok {
		return Selection{}, fmt.Errorf("unknown type: %q", selfType.Name())
	}
	fieldDef, ok := class.FieldDefinition(first.Field)
	if !ok {
		return Selection{}, fmt.Errorf("unknown field: %q", first.Field)
	}
	resType := fieldDef.Type

	for _, arg := range first.Args {
		argDef := fieldDef.Arguments.ForName(arg.Name)
		if argDef == nil {
			return Selection{}, fmt.Errorf("unknown argument: %q", arg.Name)
		}
		val, err := s.fromLiteral(ctx, arg.Value, argDef)
		if err != nil {
			return Selection{}, err
		}
		sel.Selector.Args = append(sel.Selector.Args, Arg{
			Name:  arg.Name,
			Value: val,
		})
	}

	if len(rest) > 0 {
		subsel, err := s.constructorToSelection(ctx, resType, rest[0], rest[1:]...)
		if err != nil {
			return Selection{}, err
		}
		sel.Subselections = append(sel.Subselections, subsel)
	}

	return sel, nil
}

func (s *Server) field(typeName, fieldName string) (*ast.FieldDefinition, error) {
	classes, ok := s.classes[typeName]
	if !ok {
		return nil, fmt.Errorf("unknown type: %q", typeName)
	}
	fieldDef, ok := classes.FieldDefinition(fieldName)
	if !ok {
		return nil, fmt.Errorf("unknown field: %q", fieldName)
	}
	return fieldDef, nil
}

func (s *Server) fromLiteral(ctx context.Context, lit *idproto.Literal, argDef *ast.ArgumentDefinition) (Typed, error) {
	switch v := lit.Value.(type) {
	case *idproto.Literal_Id:
		if v.Id.Type.NamedType == "" {
			return nil, fmt.Errorf("invalid ID: %q", v.Id)
		}
		id := v.Id
		class, ok := s.classes[id.Type.NamedType]
		if !ok {
			return nil, fmt.Errorf("unknown class: %q", id.Type.NamedType)
		}
		return class.NewID(id), nil
	case *idproto.Literal_Int:
		return NewInt(int(v.Int)), nil
	case *idproto.Literal_Float:
		return NewFloat(v.Float), nil
	case *idproto.Literal_String_:
		return NewString(v.String_), nil
	case *idproto.Literal_Bool:
		return NewBoolean(v.Bool), nil
	case *idproto.Literal_List:
		list := make(Array[Typed], len(v.List.Values))
		for i, val := range v.List.Values {
			typed, err := s.fromLiteral(ctx, val, argDef)
			if err != nil {
				return nil, err
			}
			list[i] = typed
		}
		return list, nil
	case *idproto.Literal_Object:
		return nil, fmt.Errorf("TODO: objects")
	case *idproto.Literal_Enum:
		typeName := argDef.Type.Name()
		scalar, ok := s.scalars[typeName]
		if !ok {
			return nil, fmt.Errorf("unknown scalar: %q", typeName)
		}
		return scalar.DecodeInput(v.Enum)
	default:
		panic(fmt.Sprintf("fromLiteral: unsupported literal type %T", v))
	}
}

func (s *Server) toSelectable(chainedID *idproto.ID, val Typed) (Object, error) {
	if sel, ok := val.(Object); ok {
		// We always support returning something that's already Selectable, e.g. an
		// object loaded from its ID.
		return sel, nil
	}
	class, ok := s.classes[val.Type().Name()]
	if !ok {
		return nil, fmt.Errorf("unknown type %q", val.Type().Name())
	}
	return class.New(chainedID, val)
}

func (s *Server) parseASTSelections(gqlOp *graphql.OperationContext, self *ast.Type, astSels ast.SelectionSet) ([]Selection, error) {
	vars := gqlOp.Variables

	class := s.classes[self.Name()]
	if class == nil {
		return nil, fmt.Errorf("parseASTSelections: unknown type: %q", self.Name())
	}

	sels := []Selection{}
	for _, sel := range astSels {
		switch x := sel.(type) {
		case *ast.Field:
			if x.Definition == nil {
				// surprisingly, this is a thing that can happen, even though most
				// validations should have happened by now.
				return nil, fmt.Errorf("unknown field: %q", x.Name)
			}
			sel, err := class.ParseField(x, vars)
			if err != nil {
				return nil, err
			}
			var subsels []Selection
			if len(x.SelectionSet) > 0 {
				subsels, err = s.parseASTSelections(gqlOp, x.Definition.Type, x.SelectionSet)
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
				subsels, err := s.parseASTSelections(gqlOp, self, fragment.SelectionSet)
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
	Args  []Arg
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

type Args []Arg

func (args Args) Lookup(name string) (Typed, bool) {
	for _, arg := range args {
		if arg.Name == name {
			return arg.Value, true
		}
	}
	return nil, false
}

type Arg struct {
	Name  string
	Value Typed
}

func (arg Arg) String() string {
	ast := ToLiteral(arg.Value).ToAST()
	return fmt.Sprintf("%s: %v", arg.Name, ast.Raw)
}

func (sel Selector) appendToID(id *idproto.ID, field *ast.FieldDefinition) *idproto.ID {
	cp := id.Clone()
	idArgs := make([]*idproto.Argument, 0, len(sel.Args))
	for _, arg := range sel.Args {
		idArgs = append(idArgs, &idproto.Argument{
			Name:  arg.Name,
			Value: ToLiteral(arg.Value),
		})
	}
	sort.Slice(idArgs, func(i, j int) bool {
		return idArgs[i].Name < idArgs[j].Name
	})
	cp.Constructor = append(cp.Constructor, &idproto.Selector{
		Field:   sel.Field,
		Args:    idArgs,
		Tainted: field.Directives.ForName("tainted") != nil, // TODO
		Meta:    field.Directives.ForName("meta") != nil,    // TODO
	})
	cp.Type = idproto.NewType(field.Type)
	return cp
}
