package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"

	"github.com/99designs/gqlgen/graphql"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dagql/idproto"
)

type Server struct {
	root    Selectable
	schema  *ast.Schema
	classes map[string]ObjectClass
	scalars map[string]ScalarClass
	cache   *CacheMap[digest.Digest, any]
}

func NewServer[T Typed](root T) *Server {
	schema := gqlparser.MustLoadSchema()
	queryClass := Class[T]{
		Fields: Fields[T]{},
	}
	rootNode := Object[T]{
		Constructor: idproto.New(root.Type().Name()),
		Self:        root,
		Class:       queryClass,
	}
	srv := &Server{
		schema:  schema,
		root:    rootNode,
		classes: map[string]ObjectClass{},
		scalars: map[string]ScalarClass{
			"Int":     Int{},
			"Float":   Float{},
			"String":  String{},
			"Boolean": Boolean{},
			// instead of a single ID type, each object has its own ID type
			// "ID": ID{},
		},
		cache: NewCacheMap[digest.Digest, any](),
	}
	srv.schema.Query = queryClass.Fields.Install(srv)

	return srv
}

func (s *Server) Root() Selectable {
	return s.root
}

var _ graphql.ExecutableSchema = (*Server)(nil)

func (s *Server) Complexity(typeName, field string, childComplexity int, args map[string]interface{}) (int, bool) {
	// TODO
	return 1, false
}

func (s *Server) Schema() *ast.Schema {
	return s.schema
}

func (s *Server) selections(gqlOp *graphql.OperationContext, astSels ast.SelectionSet) ([]Selection, error) {
	vars := gqlOp.Variables

	sels := []Selection{}
	for _, sel := range astSels {
		switch x := sel.(type) {
		case *ast.Field:
			args := make(map[string]Typed, len(x.Arguments))
			for _, arg := range x.Arguments {
				val, err := arg.Value.Value(vars)
				if err != nil {
					return nil, err
				}
				arg := x.Definition.Arguments.ForName(arg.Name)
				if arg == nil {
					return nil, fmt.Errorf("unknown argument: %q", arg.Name)
				}
				scalar, ok := s.scalars[arg.Type.Name()]
				if !ok {
					return nil, fmt.Errorf("unknown scalar: %q", arg.Type.Name())
				}
				typed, err := scalar.New(val)
				if err != nil {
					return nil, err
				}
				args[arg.Name] = typed
			}
			subsels, err := s.selections(gqlOp, x.SelectionSet)
			if err != nil {
				return nil, err
			}
			sels = append(sels, Selection{
				Alias: x.Alias,
				Selector: Selector{
					Field: x.Name,
					Args:  args,
				},
				Subselections: subsels,
			})
		case *ast.FragmentSpread:
			fragment := gqlOp.Doc.Fragments.ForName(x.Name)
			if fragment == nil {
				return nil, fmt.Errorf("unknown fragment: %s", x.Name)
			}
			subsels, err := s.selections(gqlOp, fragment.SelectionSet)
			if err != nil {
				return nil, err
			}
			sels = append(sels, subsels...)
		default:
			return nil, fmt.Errorf("unknown field type: %T", x)
		}
	}

	return sels, nil
}

func (s *Server) Load(ctx context.Context, id *idproto.ID) (Typed, error) {
	var res Typed = s.root
	for i, idSel := range id.Constructor {
		schemaType, ok := s.schema.Types[res.Type().Name()]
		if !ok {
			return nil, fmt.Errorf("unknown type: %q", res.Type().Name())
		}
		fieldDef := schemaType.Fields.ForName(idSel.Field)
		if fieldDef == nil {
			return nil, fmt.Errorf("unknown field: %q", idSel.Field)
		}
		class, ok := s.classes[res.Type().Name()]
		if !ok {
			return nil, fmt.Errorf("undefined class: %q", fieldDef.Type.Name())
		}
		// TODO: kind of annoying but technically correct; for the ID to match, the
		// return type at this point in time also has to match.
		stepID := id.Clone()
		stepID.Constructor = id.Constructor[:i+1]
		stepID.TypeName = fieldDef.Type.Name()
		obj, err := class.New(stepID, res)
		if err != nil {
			return nil, fmt.Errorf("instantiate from id: %w", err)
		}
		sel := Selector{
			Field: idSel.Field,
			Args:  make(map[string]Typed, len(idSel.Args)),
			Nth:   int(idSel.Nth),
		}
		for _, arg := range idSel.Args {
			val, err := s.fromLiteral(ctx, arg.Value)
			if err != nil {
				return nil, err
			}
			sel.Args[arg.Name] = val
		}
		res, err = obj.Select(ctx, sel)
		if err != nil {
			return nil, err
		}
		if sel.Nth != 0 {
			enum, ok := res.(Enumerable)
			if !ok {
				return nil, fmt.Errorf("cannot sub-select %dth item from %T", sel.Nth, res)
			}
			res, err = enum.Nth(sel.Nth)
			if err != nil {
				return nil, err
			}
		}
	}
	return res, nil
}

func (s *Server) fromLiteral(ctx context.Context, lit *idproto.Literal) (Typed, error) {
	switch v := lit.Value.(type) {
	case *idproto.Literal_Id:
		id := v.Id
		class, ok := s.classes[id.TypeName]
		if !ok {
			return nil, fmt.Errorf("unknown class: %q", id.TypeName)
		}
		return class.ID(id), nil
	case *idproto.Literal_Int:
		return NewInt(int(v.Int)), nil
	case *idproto.Literal_Float:
		return nil, fmt.Errorf("TODO: floats")
	case *idproto.Literal_String_:
		return NewString(v.String_), nil
	case *idproto.Literal_Bool:
		return NewBoolean(v.Bool), nil
	case *idproto.Literal_List:
		list := make(Array[Typed], len(v.List.Values))
		for i, val := range v.List.Values {
			typed, err := s.fromLiteral(ctx, val)
			if err != nil {
				return nil, err
			}
			list[i] = typed
		}
		return list, nil
	case *idproto.Literal_Object:
		return nil, fmt.Errorf("TODO: objects")
	default:
		panic(fmt.Sprintf("unsupported literal type %T", v))
	}
}

func (s *Server) Exec(ctx context.Context) graphql.ResponseHandler {
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
				sels, err := s.selections(gqlOp, op.SelectionSet)
				if err != nil {
					return graphql.ErrorResponse(ctx, "selections: %s", err)
				}
				results, err = s.Resolve(ctx, s.root, Query{sels})
				if err != nil {
					return graphql.ErrorResponse(ctx, "resolve: %s", err)
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

func ToLiteral(typed Typed) *idproto.Literal {
	switch x := typed.(type) {
	case Scalar:
		return x.Literal()
	case Node:
		return idproto.LiteralValue(x.ID())
	default:
		panic(fmt.Sprintf("cannot convert %T to Literal", x))
	}
}

func (sel Selector) AppendToID(id *idproto.ID, field *ast.FieldDefinition) *idproto.ID {
	cp := id.Clone()
	idArgs := make([]*idproto.Argument, 0, len(sel.Args))
	for name, val := range sel.Args {
		idArgs = append(idArgs, &idproto.Argument{
			Name:  name,
			Value: ToLiteral(val),
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
	cp.TypeName = field.Type.Name()
	return cp
}

func (s *Server) Resolve(ctx context.Context, self Selectable, q Query) (map[string]any, error) {
	results := make(map[string]any, len(q.Selections))

	for _, sel := range q.Selections {
		res, err := s.Select(ctx, self, sel)
		if err != nil {
			return nil, err
		}
		results[sel.Name()] = res
	}

	return results, nil
}

func (s *Server) Select(ctx context.Context, self Selectable, sel Selection) (any, error) {
	typeDef, ok := s.schema.Types[self.Type().Name()]
	if !ok {
		return nil, fmt.Errorf("unknown type: %q", self.Type().Name())
	}

	field := typeDef.Fields.ForName(sel.Selector.Field)
	if field == nil {
		return nil, fmt.Errorf("unknown field: %q", sel.Selector.Field)
	}

	chainedID := sel.Selector.AppendToID(self.ID(), field)

	// digest, err := chain.Canonical().Digest()
	// if err != nil {
	// 	return nil, err
	// }

	// if field.Pure && !chain.Tainted() { // TODO test !chain.Tainted(); intent is to not cache any queries that depend on a tainted input
	// 	val, err = s.cache.GetOrInitialize(ctx, digest, func(ctx context.Context) (any, error) {
	// 		return root.Resolve(ctx, sel.Selector)
	// 	})
	// } else {
	val, err := self.Select(ctx, sel.Selector)
	// }
	if err != nil {
		return nil, err
	}

	var isNull bool
	if n, ok := val.(Nullable); ok {
		val, ok = n.Unwrap()
		isNull = !ok
	}

	var res any
	if isNull {
		res = nil
	} else if len(sel.Subselections) == 0 {
		res = val
	} else if len(sel.Subselections) > 0 {
		class, ok := s.classes[field.Type.Name()]
		if !ok {
			return nil, fmt.Errorf("unknown type %q", field.Type.Name())
		}
		switch {
		case field.Type.NamedType != "":
			node, err := class.New(chainedID, val)
			if err != nil {
				return nil, fmt.Errorf("instantiate: %w", err)
			}
			res, err = s.Resolve(ctx, node, Query{
				Selections: sel.Subselections,
			})
			if err != nil {
				return nil, err
			}
		case field.Type.Elem != nil:
			enum, ok := val.(Enumerable)
			if !ok {
				return nil, fmt.Errorf("cannot sub-select %T", val)
			}
			// TODO arrays of arrays
			results := []any{} // TODO subtle: favor [] over null result
			for nth := 1; nth <= enum.Len(); nth++ {
				indexedID := chainedID.Nth(nth)
				val, err := enum.Nth(nth)
				if err != nil {
					return nil, err
				}
				node, err := class.New(indexedID, val)
				if err != nil {
					return nil, fmt.Errorf("instantiate %dth array element: %w", nth, err)
				}
				res, err := s.Resolve(ctx, node, Query{
					Selections: sel.Subselections,
				})
				if err != nil {
					return nil, err
				}
				results = append(results, res)
			}
			res = results
		default:
			return nil, fmt.Errorf("cannot sub-select %T", val)
		}
	}

	if sel.Selector.Nth != 0 {
		enum, ok := res.(Enumerable)
		if !ok {
			return nil, fmt.Errorf("cannot sub-select %dth item from %T", sel.Selector.Nth, val)
		}
		return enum.Nth(sel.Selector.Nth)
	}

	return res, nil
}

func (fields Fields[T]) Install(server *Server) *ast.Definition {
	var t T
	typeName := t.Type().Name()

	schemaType, ok := server.schema.Types[typeName]
	if !ok {
		schemaType = &ast.Definition{
			Kind:        ast.Object,
			Description: "TODO", // t.Description()
			Name:        typeName,
		}
		server.schema.AddTypes(schemaType)

		if _, ok := server.scalars[typeName+"ID"]; !ok {
			idType := &ast.Definition{
				Kind: ast.Scalar,
				Name: typeName + "ID",
			}
			server.schema.AddTypes(idType)
			server.scalars[typeName+"ID"] = ID[T]{}
		}

		fields["id"] = Field[T]{
			Spec: FieldSpec{
				Type: &ast.Type{
					NamedType: typeName + "ID",
					NonNull:   true,
				},
			},
			NodeFunc: func(ctx context.Context, self Node, args map[string]Typed) (Typed, error) {
				return ID[T]{ID: self.ID()}, nil
			},
		}
	}

	for fieldName, field := range fields {
		schemaField := schemaType.Fields.ForName(fieldName)
		if schemaField != nil {
			// TODO
			log.Printf("field %s.%q redefined", typeName, fieldName)
			continue
		}

		schemaArgs := ast.ArgumentDefinitionList{}
		for _, arg := range field.Spec.Args {
			schemaArg := &ast.ArgumentDefinition{
				Name: arg.Name,
				Type: arg.Type,
			}
			if arg.Default != nil {
				schemaArg.DefaultValue = LiteralToAST(arg.Default.Literal())
			}
			schemaArgs = append(schemaArgs, schemaArg)
		}

		schemaField = &ast.FieldDefinition{
			Name: fieldName,
			// Description  string
			Arguments: schemaArgs,
			// DefaultValue *Value                 // only for input objects
			Type: field.Spec.Type,
			// Directives   DirectiveList
		}

		// intentionally mutates
		schemaType.Fields = append(schemaType.Fields, schemaField)
	}

	if orig, stitch := server.classes[typeName]; stitch {
		switch cls := orig.(type) {
		case Class[T]:
			for fieldName, field := range fields {
				cls.Fields[fieldName] = field
			}
		default:
			panic(fmt.Errorf("cannot stitch type %q: not an object", typeName))
		}
	} else {
		server.classes[typeName] = Class[T]{
			Fields: fields,
		}
	}

	return schemaType
}

type Query struct {
	Selections []Selection
}

type Selection struct {
	Alias         string
	Selector      Selector
	Subselections []Selection
}

func (sel Selection) Name() string {
	if sel.Alias != "" {
		return sel.Alias
	}
	return sel.Selector.Field
}
