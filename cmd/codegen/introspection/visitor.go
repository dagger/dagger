package introspection

import (
	"sort"
	"strings"
)

type Visitor struct {
	schema *Schema
}

func (v *Visitor) Run() []*Type {
	sequence := []struct {
		Kind   TypeKind
		Ignore map[string]any
	}{
		{
			Kind: TypeKindScalar,
			Ignore: map[string]any{
				"String":   struct{}{},
				"Float":    struct{}{},
				"Int":      struct{}{},
				"Boolean":  struct{}{},
				"DateTime": struct{}{},
				"ID":       struct{}{},
			},
		},
		{
			Kind: TypeKindInputObject,
		},
		{
			Kind: TypeKindObject,
		},
		{
			Kind: TypeKindEnum,
		},
	}

	var types []*Type
	for _, i := range sequence {
		types = append(types, v.visit(i.Kind, i.Ignore)...)
	}
	return types
}

func (v *Visitor) visit(kind TypeKind, ignore map[string]any) []*Type {
	types := []*Type{}
	for _, t := range v.schema.Types {
		if t.Kind == kind {
			// internal GraphQL type
			if strings.HasPrefix(t.Name, "_") {
				continue
			}
			if ignore != nil {
				if _, ok := ignore[t.Name]; ok {
					continue
				}
			}
			types = append(types, t)
		}
	}

	// Sort types
	sort.Slice(types, func(i, j int) bool {
		return types[i].Name < types[j].Name
	})

	// Sort within the type
	for _, t := range types {
		sort.Slice(t.Fields, func(i, j int) bool {
			return t.Fields[i].Name < t.Fields[j].Name
		})

		sort.Slice(t.InputFields, func(i, j int) bool {
			return t.InputFields[i].Name < t.InputFields[j].Name
		})
	}

	return types
}
