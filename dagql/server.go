package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"

	"github.com/99designs/gqlgen/graphql"
	"github.com/dagger/dagql/idproto"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

type Server struct {
	root   Selectable
	schema *ast.Schema
	types  map[string]TypeResolver
	cache  *CacheMap[digest.Digest, any]
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
		schema: schema,
		root:   rootNode,
		types:  map[string]TypeResolver{
			// TODO what makes these needed?
			// "Int": ScalarResolver[Int]{},
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

func ToQuery(gqlOp *graphql.OperationContext, sels ast.SelectionSet) Query {
	vars := gqlOp.Variables
	query := Query{}

	for _, sel := range sels {
		switch x := sel.(type) {
		case *ast.Field:
			args := make(map[string]Literal, len(x.Arguments))
			for _, arg := range x.Arguments {
				val, err := arg.Value.Value(vars)
				if err != nil {
					// TODO
					panic(err)
				}
				args[arg.Name] = Literal{idproto.LiteralValue(val)}
			}
			query.Selections = append(query.Selections, Selection{
				Alias: x.Alias,
				Selector: Selector{
					Field: x.Name,
					Args:  args,
				},
				Subselections: ToQuery(gqlOp, x.SelectionSet).Selections,
			})
		case *ast.FragmentSpread:
			fragment := gqlOp.Doc.Fragments.ForName(x.Name)
			if fragment == nil {
				panic(fmt.Sprintf("unknown fragment: %s", x.Name))
			}
			query.Selections = append(query.Selections,
				ToQuery(gqlOp, fragment.SelectionSet).Selections...)
		default:
			panic(fmt.Sprintf("unknown field type: %T", x))
		}
	}

	return query
}

func ConstructorToQuery(sels []*idproto.Selector) Query {
	query := Query{}

	if len(sels) == 0 {
		return query
	}

	sel := sels[0]

	args := make(map[string]Literal, len(sel.Args))
	for _, arg := range sel.Args {
		args[arg.Name] = Literal{arg.Value}
	}
	query.Selections = append(query.Selections, Selection{
		Selector: Selector{
			Field: sel.Field,
			Args:  args,
			Nth:   int(sel.Nth),
		},
		Subselections: ConstructorToQuery(sels[1:]).Selections,
	})

	return query
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
				var err error
				results, err = s.Resolve(ctx, s.root, ToQuery(gqlOp, op.SelectionSet))
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

func (sel Selector) Chain(id *idproto.ID, field *ast.FieldDefinition) *idproto.ID {
	chain := id.Clone()
	idArgs := make([]*idproto.Argument, 0, len(sel.Args))
	for name, val := range sel.Args {
		idArgs = append(idArgs, &idproto.Argument{
			Name:  name,
			Value: val.Literal,
		})
	}
	sort.Slice(idArgs, func(i, j int) bool {
		return idArgs[i].Name < idArgs[j].Name
	})
	chain.Constructor = append(chain.Constructor, &idproto.Selector{
		Field:   sel.Field,
		Args:    idArgs,
		Tainted: field.Directives.ForName("tainted") != nil, // TODO
		Meta:    field.Directives.ForName("meta") != nil,    // TODO
	})
	chain.TypeName = field.Type.Name()
	return chain
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

	chainedID := sel.Selector.Chain(self.ID(), field)

	// digest, err := chain.Canonical().Digest()
	// if err != nil {
	// 	return nil, err
	// }

	// if field.Pure && !chain.Tainted() { // TODO test !chain.Tainted(); intent is to not cache any queries that depend on a tainted input
	// 	val, err = s.cache.GetOrInitialize(ctx, digest, func(ctx context.Context) (any, error) {
	// 		return root.Resolve(ctx, sel.Selector)
	// 	})
	// } else {
	val, err := self.Select(ctx, field, sel.Selector.Args)
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
		resolver, ok := s.types[field.Type.Name()]
		if !ok {
			return nil, fmt.Errorf("unknown type %q", field.Type.Name())
		}
		class, ok := resolver.(Instantiator)
		if !ok {
			return nil, fmt.Errorf("cannot select from type %s: expected %T, got %T", field.Type.Name(), class, resolver)
		}
		switch {
		case field.Type.NamedType != "":
			node, err := class.Instantiate(chainedID, val)
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
				node, err := class.Instantiate(indexedID, val)
				if err != nil {
					return nil, fmt.Errorf("instantiate: %w", err)
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

func (i ID[T]) Load(ctx context.Context, server *Server) (T, error) {
	var res T
	// TODO check cache
	results, err := server.Resolve(ctx, server.Root(), i.Query())
	if err != nil {
		return res, err
	}
	val := unpack(results)
	obj, ok := val.(T)
	if !ok {
		return res, fmt.Errorf("load: expected %T, got %T", res, val)
	}
	return obj, nil
}

func unpack(vals any) any {
	switch x := vals.(type) {
	case map[string]any:
		for _, val := range x {
			return unpack(val)
		}
		return x
	default:
		return x
	}
}

func (i ID[T]) Query() Query {
	return ConstructorToQuery(i.ID.Constructor)
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

		if _, ok := server.types[typeName+"ID"]; !ok {
			idType := &ast.Definition{
				Kind: ast.Scalar,
				Name: typeName + "ID",
			}
			server.schema.AddTypes(idType)
			server.types[typeName+"ID"] = ScalarResolver[ID[T]]{}
		}

		fields["id"] = Field[T]{
			Spec: FieldSpec{
				Type: &ast.Type{
					NamedType: typeName + "ID",
					NonNull:   true,
				},
			},
			NodeFunc: func(ctx context.Context, self Node, args map[string]Literal) (Typed, error) {
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
				schemaArg.DefaultValue = arg.Default.ToAST()
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

	if orig, stitch := server.types[typeName]; stitch {
		switch cls := orig.(type) {
		case Class[T]:
			for fieldName, field := range fields {
				cls.Fields[fieldName] = field
			}
		default:
			panic(fmt.Errorf("cannot stitch type %q: not an object", typeName))
		}
	} else {
		server.types[typeName] = Class[T]{
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
