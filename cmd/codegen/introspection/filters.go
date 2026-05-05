package introspection

import (
	"slices"
)

// Core types that can be extended by the engine itself when
// installing a dependency.
var ExtendableTypes = []string{
	"Query",
	"Binding",
	"Env",
}

// DependencyNames returns the unique list of module names that appear in
// the schema's sourceMap directives, excluding the built-in extendable types
// (Query, Binding, Env) whose fields are contributed by multiple modules.
func (s *Schema) DependencyNames() []string {
	seen := map[string]struct{}{}
	var names []string

	for _, t := range s.Types {
		// For regular types, look at the type-level source map.
		if sm := t.Directives.SourceMap(); sm != nil && sm.Module != "" {
			if _, ok := seen[sm.Module]; !ok {
				seen[sm.Module] = struct{}{}
				names = append(names, sm.Module)
			}
		}
	}

	slices.Sort(names)
	return names
}

// Return a copy of the current schema but with only types that comes
// from the  given moduleName.
// This include the actual typedef exposed by the module, enum, interface but
// also any types that may have been extended by the engine itself for that
// dependency.
// For example: `Binding.AsXXX` etc...
func (s *Schema) Include(moduleNames ...string) *Schema {
	filteredSchema := &Schema{
		QueryType:  s.QueryType,
		Directives: s.Directives,
	}

	for _, i := range s.Types {
		if slices.Contains(ExtendableTypes, i.Name) {
			filteredSchema.Types = append(filteredSchema.Types, keepFieldsFromModules(i, moduleNames))
			continue
		}

		if isOwnedByModules(i.Directives, moduleNames) {
			filteredSchema.Types = append(filteredSchema.Types, i)
		}
	}

	return filteredSchema
}

// Return a copy of the current schema but without types that comes
// from the given moduleName.
// This exclude the actual typedef exposed by the module, enum, interface but
// also any types that may have been extended by the engine itself for that
// dependency.
// For example: `Binding.AsXXX` etc...
func (s *Schema) Exclude(moduleNames ...string) *Schema {
	filteredSchema := &Schema{
		QueryType:  s.QueryType,
		Directives: s.Directives,
	}

	for _, i := range s.Types {
		if slices.Contains(ExtendableTypes, i.Name) {
			filteredSchema.Types = append(filteredSchema.Types, dropFieldFromModules(i, moduleNames))
			continue
		}

		if !isOwnedByModules(i.Directives, moduleNames) {
			filteredSchema.Types = append(filteredSchema.Types, i)
		}
	}

	return filteredSchema
}

func keepFieldsFromModules(t *Type, moduleNames []string) *Type {
	filteredType := &Type{
		Kind:        t.Kind,
		Name:        t.Name,
		Description: t.Description,
		Fields:      []*Field{},
		InputFields: t.InputFields,
		Directives:  t.Directives,
		Interfaces:  t.Interfaces,
		EnumValues:  t.EnumValues,
	}

	for _, field := range t.Fields {
		if isOwnedByModules(field.Directives, moduleNames) {
			filteredType.Fields = append(filteredType.Fields, field)
		}
	}

	return filteredType
}

func dropFieldFromModules(t *Type, moduleNames []string) *Type {
	filteredType := &Type{
		Kind:        t.Kind,
		Name:        t.Name,
		Description: t.Description,
		Fields:      []*Field{},
		InputFields: t.InputFields,
		Directives:  t.Directives,
		Interfaces:  t.Interfaces,
		EnumValues:  t.EnumValues,
	}

	for _, field := range t.Fields {
		if !isOwnedByModules(field.Directives, moduleNames) {
			filteredType.Fields = append(filteredType.Fields, field)
		}
	}

	return filteredType
}

// Return true if the directives given by the typedef is
// originating from the list of given module.
func isOwnedByModules(directives Directives, moduleNames []string) bool {
	sourceMap := directives.SourceMap()
	if sourceMap == nil {
		return false
	}

	return slices.Contains(moduleNames, sourceMap.Module)
}
