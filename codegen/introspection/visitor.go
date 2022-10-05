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
	Scalar  VisitFunc
	Object  VisitFunc
	Input   VisitFunc
	Allowed map[string]struct{}
}

func (v *Visitor) Run() error {
	sequence := []struct {
		Kind    TypeKind
		Handler VisitFunc
	}{
		{
			Kind:    TypeKindScalar,
			Handler: v.handlers.Scalar,
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
		if err := v.visit(i.Kind, i.Handler); err != nil {
			return err
		}
	}

	return nil
}

func (v *Visitor) visit(kind TypeKind, h VisitFunc) error {
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
			if v.handlers.Allowed != nil {
				if _, ok := v.handlers.Allowed[t.Name]; !ok {
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

	// Sort input fields
	for _, t := range types {
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
