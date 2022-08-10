package router

import (
	"fmt"

	tools "github.com/bhoriuchi/graphql-go-tools"
	"github.com/graphql-go/graphql"
)

func Stitch(schemas []ExecutableSchema) (*graphql.Schema, error) {
	defs := []string{}
	for _, r := range schemas {
		defs = append(defs, r.Schema())
	}

	typeResolvers := tools.ResolverMap{}
	for _, s := range schemas {
		for name, resolver := range s.Resolvers() {

			switch resolver := resolver.(type) {
			case ObjectResolver:
				obj, ok := typeResolvers[name].(*tools.ObjectResolver)
				if !ok {
					obj = &tools.ObjectResolver{
						Fields: tools.FieldResolveMap{},
					}
					typeResolvers[name] = obj
				}
				for fieldName, fn := range resolver {
					if _, ok := obj.Fields[fieldName]; ok {
						return nil, fmt.Errorf("conflict on type %q: field %q re-defined", name, fieldName)
					}
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
	}

	schema, err := tools.MakeExecutableSchema(tools.ExecutableSchema{
		TypeDefs:  defs,
		Resolvers: typeResolvers,
	})
	if err != nil {
		return nil, err
	}

	return &schema, nil
}
