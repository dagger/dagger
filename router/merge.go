package router

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrMergeTypeConflict   = errors.New("object type re-defined")
	ErrMergeFieldConflict  = errors.New("field re-defined")
	ErrMergeScalarConflict = errors.New("scalar re-defined")
)

func MergeLoadedSchemas(name string, schemas ...LoadedSchema) LoadedSchema {
	staticSchemas := make([]StaticSchemaParams, len(schemas))
	for i, s := range schemas {
		staticSchemas[i] = StaticSchemaParams{
			Schema: s.Schema(),
		}
	}
	return StaticSchema(mergeSchemas(name, staticSchemas...))
}

func MergeExecutableSchemas(name string, schemas ...ExecutableSchema) (ExecutableSchema, error) {
	staticSchemas := make([]StaticSchemaParams, len(schemas))
	for i, s := range schemas {
		staticSchemas[i] = StaticSchemaParams{
			Name:         s.Name(),
			Schema:       s.Schema(),
			Resolvers:    s.Resolvers(),
			Dependencies: s.Dependencies(),
		}
	}
	merged := mergeSchemas(name, staticSchemas...)

	merged.Resolvers = Resolvers{}
	for _, s := range schemas {
		for name, resolver := range s.Resolvers() {
			switch resolver := resolver.(type) {
			case ObjectResolver:
				var objResolver ObjectResolver
				if r, ok := merged.Resolvers[name]; ok {
					objResolver, ok = r.(ObjectResolver)
					if !ok {
						return nil, fmt.Errorf("conflict on type %q: %w", name, ErrMergeTypeConflict)
					}
				} else {
					objResolver = ObjectResolver{}
					merged.Resolvers[name] = objResolver
				}

				for fieldName, fn := range resolver {
					if _, ok := objResolver[fieldName]; ok {
						return nil, fmt.Errorf("conflict on type %q: %q: %w", name, fieldName, ErrMergeFieldConflict)
					}
					objResolver[fieldName] = fn
				}
			case ScalarResolver:
				if existing, ok := merged.Resolvers[name]; ok {
					if _, ok := existing.(ScalarResolver); !ok {
						return nil, fmt.Errorf("conflict on type %q: %w", name, ErrMergeTypeConflict)
					}
					return nil, fmt.Errorf("conflict on type %q: %w", name, ErrMergeScalarConflict)
				}
				merged.Resolvers[name] = resolver
			default:
				panic(resolver)
			}
		}
	}

	return StaticSchema(merged), nil
}

func mergeSchemas(name string, schemas ...StaticSchemaParams) StaticSchemaParams {
	merged := StaticSchemaParams{Name: name}

	defs := []string{}
	for _, r := range schemas {
		defs = append(defs, r.Schema)
	}
	merged.Schema = strings.Join(defs, "\n")

	return merged
}
