package dagql

import (
	"context"
	"fmt"

	"github.com/dagger/dagql/idproto"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

type Server struct {
	Root   Node
	Schema *ast.Schema

	types map[string]TypeResolver

	cache *CacheMap[digest.Digest, any]
}

func NewServer[T Typed](root T) *Server {
	queryFields := ObjectFields[T]{}

	srv := &Server{
		Schema: gqlparser.MustLoadSchema(),
		Root: ObjectNode[T]{
			Constructor: idproto.New(root.TypeName()),
			Self:        root,
			Class: Class[T]{
				Fields: queryFields,
			},
		},
		types: map[string]TypeResolver{
			// "Int": ScalarResolver[Int]{},
		},
		cache: NewCacheMap[digest.Digest, any](),
	}

	Install(srv, queryFields)

	return srv
}

func (s Server) Resolve(ctx context.Context, root Node, q Query) (map[string]any, error) {
	results := make(map[string]any, len(q.Selections))

	for _, sel := range q.Selections {
		typeDef, ok := s.Schema.Types[root.TypeName()]
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
		chain.Constructor = append(chain.Constructor, &idproto.Selector{
			Field: sel.Selector.Field,
			Args:  idArgs,
			// TODO: how is this conveyed?
			// Tainted: !field.Pure,
			// Meta:    field.Meta,
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
		} else {
			// scalar, ok := resolver.(ScalarType)
			// if !ok {
			// 	// TODO better error
			// 	return nil, fmt.Errorf("cannot convert %T to scalar", resolver)
			// }

			// val, err = scalar.ConvertToResponse(val)
			// if err != nil {
			// 	// TODO better error
			// 	return nil, err
			// }
		}

		results[sel.Name()] = val
	}

	return results, nil
}

func Install[T Typed](server *Server, fields ObjectFields[T]) {
	var t T
	typeName := t.TypeName()

	schemaType, ok := server.Schema.Types[typeName]
	if !ok {
		schemaType = &ast.Definition{
			Kind:        ast.Object,
			Description: "TODO", // t.Description()
			Name:        typeName,
		}
		server.Schema.Types[typeName] = schemaType

		fields["id"] = Field[T]{
			Spec: FieldSpec{
				Type: &ast.Type{
					NamedType: typeName + "ID",
					NonNull:   true,
				},
			},
			NodeFunc: func(ctx context.Context, self Node, args map[string]Literal) (any, error) {
				return self.ID(), nil
			},
		}
	}

	for fieldName, field := range fields {
		schemaField := schemaType.Fields.ForName(fieldName)
		if schemaField != nil {
			panic(fmt.Sprintf("field %s.%q already defined", typeName, fieldName))
			panic("TODO: decide how to handle field already defined")
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

	if _, ok := server.types[typeName+"ID"]; !ok {
		server.types[typeName+"ID"] = ScalarResolver[*ID[T]]{}
	}
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
