package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"

	"github.com/99designs/gqlgen/graphql"
	"github.com/dagger/dagql/idproto"
	"github.com/dagger/dagql/introspection"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

type Server struct {
	root   Resolver
	schema *ast.Schema
	types  map[string]TypeResolver
	cache  *CacheMap[digest.Digest, any]
}

func NewServer[T Typed](root T) *Server {
	schema := gqlparser.MustLoadSchema()
	queryClass := Class[T]{
		Fields: Fields[T]{
			"__schema": Func(func(ctx context.Context, self T, args struct{}) (introspection.Schema, error) {
				return introspection.WrapSchema(schema), nil
			}),
			"__type": Func(func(ctx context.Context, self T, args struct {
				Name String
			}) (introspection.Type, error) {
				def, ok := schema.Types[args.Name.Value]
				if !ok {
					return introspection.Type{}, fmt.Errorf("unknown type: %q", args.Name)
				}
				return introspection.WrapTypeFromDef(schema, def), nil
			}),
		},
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

	typeKind := EnumSpec{
		Name: "__TypeKind",
		Values: []*ast.EnumValueDefinition{
			{Name: "SCALAR"},
			{Name: "OBJECT"},
			{Name: "INTERFACE"},
			{Name: "UNION"},
			{Name: "ENUM"},
			{Name: "INPUT_OBJECT"},
			{Name: "LIST"},
			{Name: "NON_NULL"},
		},
	}
	typeKind.Install(srv)

	directiveLocation := EnumSpec{
		Name: "__DirectiveLocation",
		Values: []*ast.EnumValueDefinition{
			{Name: "QUERY"},
			{Name: "MUTATION"},
			{Name: "SUBSCRIPTION"},
			{Name: "FIELD"},
			{Name: "FRAGMENT_DEFINITION"},
			{Name: "FRAGMENT_SPREAD"},
			{Name: "INLINE_FRAGMENT"},
			{Name: "VARIABLE_DEFINITION"},
			{Name: "SCHEMA"},
			{Name: "SCALAR"},
			{Name: "OBJECT"},
			{Name: "FIELD_DEFINITION"},
			{Name: "ARGUMENT_DEFINITION"},
			{Name: "INTERFACE"},
			{Name: "UNION"},
			{Name: "ENUM"},
			{Name: "ENUM_VALUE"},
			{Name: "INPUT_OBJECT"},
			{Name: "INPUT_FIELD_DEFINITION"},
		},
	}
	directiveLocation.Install(srv)

	Fields[introspection.Schema]{
		"queryType": Func(func(ctx context.Context, self introspection.Schema, args struct{}) (introspection.Type, error) {
			return introspection.NewType(*self.QueryType()), nil
		}),
		"mutationType": Func(func(ctx context.Context, self introspection.Schema, args struct{}) (Optional[introspection.Type], error) {
			if self.MutationType() == nil {
				return Optional[introspection.Type]{}, nil
			}
			return Opt(introspection.NewType(*self.MutationType())), nil
		}),
		"subscriptionType": Func(func(ctx context.Context, self introspection.Schema, args struct{}) (Optional[introspection.Type], error) {
			if self.SubscriptionType() == nil {
				return Optional[introspection.Type]{}, nil
			}
			return Opt(introspection.NewType(*self.SubscriptionType())), nil
		}),
		"types": Func(func(ctx context.Context, self introspection.Schema, args struct{}) (Array[introspection.Type], error) {
			var types []introspection.Type
			for _, def := range self.Types() {
				types = append(types, introspection.NewType(def))
			}
			return types, nil
		}),
		"directives": Func(func(ctx context.Context, self introspection.Schema, args struct{}) (Array[introspection.Directive], error) {
			var directives []introspection.Directive
			for _, dir := range self.Directives() {
				directives = append(directives, introspection.NewDirective(dir))
			}
			return directives, nil
		}),
	}.Install(srv)

	Fields[introspection.Type]{
		"name": Func(func(ctx context.Context, self introspection.Type, args struct{}) (Optional[String], error) {
			if self.Name() == nil {
				return NoOpt[String](), nil
			} else {
				return Opt(String{*self.Name()}), nil
			}
		}),
		"kind": Func(func(ctx context.Context, self introspection.Type, args struct{}) (Enum, error) {
			return Enum{
				Enum:  typeKind.Type(),
				Value: self.Kind(),
			}, nil
		}),
	}.Install(srv)

	Fields[introspection.Directive]{
		"name": Func(func(ctx context.Context, self introspection.Directive, args struct{}) (String, error) {
			return String{self.Name}, nil
		}),
		"description": Func(func(ctx context.Context, self introspection.Directive, args struct{}) (Optional[String], error) {
			if self.Description() == nil {
				return NoOpt[String](), nil
			} else {
				return Opt(String{*self.Description()}), nil
			}
		}),
		"locations": Func(func(ctx context.Context, self introspection.Directive, args struct{}) (Array[Enum], error) {
			var locations []Enum
			for _, loc := range self.Locations {
				locations = append(locations, Enum{
					Enum:  directiveLocation.Type(),
					Value: loc,
				})
			}
			return locations, nil
		}),
		"args": Func(func(ctx context.Context, self introspection.Directive, _ struct{}) (Array[introspection.InputValue], error) {
			var args []introspection.InputValue
			for _, arg := range self.Args {
				args = append(args, introspection.NewInputValue(arg))
			}
			return args, nil
		}),
	}.Install(srv)

	return srv
}

func (s *Server) Root() Resolver {
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

		results := make(map[string]any)
		for _, op := range gqlOp.Doc.Operations {
			switch op.Operation {
			case ast.Query:
				// TODO prospective
				if gqlOp.OperationName != "" && gqlOp.OperationName != op.Name {
					continue
				}
				var err error
				results, err = s.Resolve(ctx, s.root, ToQuery(gqlOp.Variables, op.SelectionSet))
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

func (s *Server) Resolve(ctx context.Context, self Resolver, q Query) (map[string]any, error) {
	results := make(map[string]any, len(q.Selections))

	for _, sel := range q.Selections {
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
		val, err := self.Resolve(ctx, field, sel.Selector.Args)
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
		if isNull || len(sel.Subselections) == 0 {
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
				var results []any
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
				return nil, fmt.Errorf("cannot sub-select %T", val)
			}
			res, err = enum.Nth(sel.Selector.Nth)
			if err != nil {
				return nil, err
			}
		}

		results[sel.Name()] = res
	}

	return results, nil
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
