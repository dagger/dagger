package introspection

import (
	"sort"
	"strings"
)

type Visitor struct {
	schema   *Schema
	handlers VisitHandlers
}

type VisitFunc func(*Type) error

type VisitHandlers struct {
	Scalar VisitFunc
	Object VisitFunc
	Input  VisitFunc
}

func (v *Visitor) Run() error {
	sequence := []struct {
		Kind    TypeKind
		Handler VisitFunc
		Ignore  map[string]any
	}{
		{
			Kind:    TypeKindScalar,
			Handler: v.handlers.Scalar,
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
			Kind:    TypeKindInputObject,
			Handler: v.handlers.Input,
		},
		{
			Kind:    TypeKindObject,
			Handler: v.handlers.Object,
		},
	}

	for _, i := range sequence {
		if err := v.visit(i.Kind, i.Handler, i.Ignore); err != nil {
			return err
		}
	}

	return nil
}

func (v *Visitor) visit(kind TypeKind, h VisitFunc, ignore map[string]any) error {
	if h == nil {
		return nil
	}

	types := []*Type{}
	for _, t := range v.schema.Types {
		if t.Kind == kind {
			// internal GraphQL type
			if strings.HasPrefix(t.Name, "__") {
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

		for _, f := range t.Fields {
			sort.Slice(f.Args, func(i, j int) bool {
				return f.Args[i].Name < f.Args[j].Name
			})
		}

		sort.Slice(t.InputFields, func(i, j int) bool {
			return t.InputFields[i].Name < t.InputFields[j].Name
		})
	}

	for _, typ := range types {
		if err := h(typ); err != nil {
			return err
		}
	}

	return nil
}
