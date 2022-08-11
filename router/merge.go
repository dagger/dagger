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

func Merge(schemas ...ExecutableSchema) (ExecutableSchema, error) {
	merged := &staticSchema{}

	defs := []string{}
	for _, r := range schemas {
		defs = append(defs, r.Schema())
	}
	merged.schema = strings.Join(defs, "\n")

	ops := []string{}
	for _, r := range schemas {
		ops = append(ops, r.Operations())
	}
	merged.operations = strings.Join(ops, "\n")

	merged.resolvers = Resolvers{}
	for _, s := range schemas {
		for name, resolver := range s.Resolvers() {
			switch resolver := resolver.(type) {
			case ObjectResolver:
				var objResolver ObjectResolver
				if r, ok := merged.resolvers[name]; ok {
					objResolver, ok = r.(ObjectResolver)
					if !ok {
						return nil, fmt.Errorf("conflict on type %q: %w", name, ErrMergeTypeConflict)
					}
				} else {
					objResolver = ObjectResolver{}
					merged.resolvers[name] = objResolver
				}

				for fieldName, fn := range resolver {
					if _, ok := objResolver[fieldName]; ok {
						return nil, fmt.Errorf("conflict on type %q: %q: %w", name, fieldName, ErrMergeFieldConflict)
					}
					objResolver[fieldName] = fn
				}
			case ScalarResolver:
				if existing, ok := merged.resolvers[name]; ok {
					if _, ok := existing.(ScalarResolver); !ok {
						return nil, fmt.Errorf("conflict on type %q: %w", name, ErrMergeTypeConflict)
					}
					return nil, fmt.Errorf("conflict on type %q: %w", name, ErrMergeScalarConflict)
				}
				merged.resolvers[name] = resolver
			default:
				panic(resolver)
			}
		}
	}

	return merged, nil
}
