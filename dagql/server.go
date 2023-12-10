package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/99designs/gqlgen/graphql"
	"github.com/dagger/dagql/idproto"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

type Server struct {
	rootNode Node

	schema *ast.Schema

	types map[string]TypeResolver

	cache *CacheMap[digest.Digest, any]
}

func NewServer[T Typed](root T) *Server {
	queryClass := Class[T]{
		Fields: Fields[T]{},
	}
	rootNode := ObjectNode[T]{
		Constructor: idproto.New(root.TypeName()),
		Self:        root,
		Class:       queryClass,
	}
	srv := &Server{
		schema:   gqlparser.MustLoadSchema(),
		rootNode: rootNode,
		types:    map[string]TypeResolver{
			// TODO what makes these needed?
			// "Int": ScalarResolver[Int]{},
		},
		cache: NewCacheMap[digest.Digest, any](),
	}
	srv.schema.Query = queryClass.Fields.Install(srv)
	return srv
}

func (s *Server) Root() Node {
	return s.rootNode
}

var _ graphql.ExecutableSchema = (*Server)(nil)

func (s *Server) Complexity(typeName, field string, childComplexity int, args map[string]interface{}) (int, bool) {
	// TODO
	return 1, false
}

func (s *Server) Schema() *ast.Schema {
	return s.schema
}

func ToQuery(vars map[string]any, sels ast.SelectionSet) Query {
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
				Subselections: ToQuery(vars, x.SelectionSet).Selections,
			})
		}
	}

	return query
}

func (s *Server) Exec(ctx context.Context) graphql.ResponseHandler {
	return func(ctx context.Context) *graphql.Response {
		gqlOp := graphql.GetOperationContext(ctx)

		if err := gqlOp.Validate(ctx); err != nil {
			return graphql.ErrorResponse(ctx, "validate: %s", err)
		}

		results := make(map[string]any)
		for _, op := range gqlOp.Doc.Operations {
			switch op.Operation {
			case ast.Query:
				// TODO prospective
				if gqlOp.OperationName != "" && gqlOp.OperationName != op.Name {
					continue
				}
				var err error
				results, err = s.Resolve(ctx, s.rootNode, ToQuery(gqlOp.Variables, op.SelectionSet))
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

func (s *Server) Resolve(ctx context.Context, root Node, q Query) (map[string]any, error) {
	results := make(map[string]any, len(q.Selections))

	for _, sel := range q.Selections {
		typeDef, ok := s.schema.Types[root.TypeName()]
		if !ok {
			// TODO better error
			return nil, fmt.Errorf("unknown type %q", root.TypeName())
		}

		field := typeDef.Fields.ForName(sel.Selector.Field)
		if field == nil {
			// TODO better error
			return nil, fmt.Errorf("unknown field: %q", sel.Selector.Field)
		}

		chain := root.ID().Clone()
		idArgs := make([]*idproto.Argument, 0, len(sel.Selector.Args))
		for name, val := range sel.Selector.Args {
			idArgs = append(idArgs, &idproto.Argument{
				Name:  name,
				Value: val.Literal,
			})
		}
		sort.Slice(idArgs, func(i, j int) bool { // TODO load-bearing! helper?
			return idArgs[i].Name < idArgs[j].Name
		})
		chain.Constructor = append(chain.Constructor, &idproto.Selector{
			Field:   sel.Selector.Field,
			Args:    idArgs,
			Tainted: field.Directives.ForName("tainted") != nil, // TODO
			Meta:    field.Directives.ForName("meta") != nil,    // TODO
		})

		// TODO: should this be a full Type? feels odd to just have a TypeName...
		// i've definitely thought about this before already tho
		if field.Type == nil {
			panic("nil type: " + field.Name)
		}

		chain.TypeName = field.Type.Name()

		// digest, err := chain.Canonical().Digest()
		// if err != nil {
		// 	return nil, err
		// }

		var val any
		var err error
		// if field.Pure && !chain.Tainted() { // TODO test !chain.Tainted(); intent is to not cache any queries that depend on a tainted input
		// 	val, err = s.cache.GetOrInitialize(ctx, digest, func(ctx context.Context) (any, error) {
		// 		return root.Resolve(ctx, sel.Selector)
		// 	})
		// } else {
		val, err = root.Resolve(ctx, field, sel.Selector.Args)
		// }
		if err != nil {
			return nil, err
		}

		if len(sel.Subselections) > 0 {
			if field.Type.NamedType == "" {
				// TODO better error
				return nil, fmt.Errorf("cannot select from non-node")
			}

			resolver, ok := s.types[field.Type.Name()]
			if !ok {
				// TODO better error
				return nil, fmt.Errorf("unknown type %q", field.Type.Name())
			}

			obj, ok := resolver.(ClassType)
			if !ok {
				// TODO better error
				return nil, fmt.Errorf("cannot select from type %s: expected %T, got %T", field.Type.Name(), obj, resolver)
			}

			node, err := obj.Instantiate(chain, val)
			if err != nil {
				// TODO better error
				return nil, err
			}

			val, err = s.Resolve(ctx, node, Query{
				Selections: sel.Subselections,
			})
			if err != nil {
				return nil, err
			}
		}

		results[sel.Name()] = val
	}

	return results, nil
}

func (fields Fields[T]) Install(server *Server) *ast.Definition {
	var t T
	typeName := t.TypeName()

	schemaType, ok := server.schema.Types[typeName]
	if !ok {
		schemaType = &ast.Definition{
			Kind:        ast.Object,
			Description: "TODO", // t.Description()
			Name:        typeName,
		}
		server.schema.Types[typeName] = schemaType

		if _, ok := server.types[typeName+"ID"]; !ok {
			server.types[typeName+"ID"] = ScalarResolver[ID[T]]{}
		}

		fields["id"] = Field[T]{
			Spec: FieldSpec{
				Type: &ast.Type{
					NamedType: typeName + "ID",
					NonNull:   true,
				},
			},
			NodeFunc: func(ctx context.Context, self Node, args map[string]Literal) (any, error) {
				return ID[T]{ID: self.ID()}, nil
			},
		}
	}

	for fieldName, field := range fields {
		schemaField := schemaType.Fields.ForName(fieldName)
		if schemaField != nil {
			panic(fmt.Sprintf("field %s.%q already defined", typeName, fieldName))
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
