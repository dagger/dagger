package templates

import (
	"cmp"
	"slices"
	"strings"

	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

// Go identifiers are schema names formatted by the engine (Query.
// formatIdentifiers, present for engine views v1.0.0-0 and later; see
// hack/designs/identifier-casing.md), loaded onto the schema in NameFormats
// before rendering. When a name has no engine format (an older schema, or a
// name that isn't one of the schema's names), the legacy conversions apply:
// golint's initialisms for names, strcase for enum values.
//
// Only Go identifiers go through here. Strings sent over the wire (selected
// fields, argument names, enum values, GraphQL type names) always use the
// schema name as is.

var (
	// pascalNames: types, methods, option struct fields and enum values.
	pascalNames = introspection.NameFormat{Casing: introspection.CasingPascal, Acronyms: introspection.AcronymsUppercase}
	// camelNames: parameters.
	camelNames = introspection.NameFormat{Casing: introspection.CasingCamel, Acronyms: introspection.AcronymsUppercase}
)

// NameFormats are the formats Go codegen asks the engine for: load them onto
// the schema with LoadFormattedNames before rendering.
var NameFormats = []introspection.NameFormat{pascalNames, camelNames}

// engineName returns a schema name as the engine formatted it in f, if the
// schema being generated has engine formats.
func engineName(name string, f introspection.NameFormat) (string, bool) {
	return generator.GetSchema().FormattedName(name, f)
}

// formatName formats a GraphQL name (an object, field, or an argument
// becoming an option struct field) into an exported Go identifier: PASCAL
// with uppercase acronyms, e.g. `parentShas` -> `ParentSHAs`. Without an
// engine format, it falls back to legacyFormatName.
func formatName(s string) string {
	if name, ok := engineName(s, pascalNames); ok {
		return name
	}
	return legacyFormatName(s)
}

// legacyFormatName is how names were formatted before the engine formatted
// them: capitalize the first letter, then apply golint's initialisms.
// Example: `fooId` -> `FooID`
func legacyFormatName(s string) string {
	if len(s) > 0 {
		s = strings.ToUpper(string(s[0])) + s[1:]
	}
	return lintName(s)
}

// formatArgName formats a GraphQL argument name into a Go parameter name:
// CAMEL with uppercase acronyms, e.g. `filterUri` -> `filterURI`, then
// escaped like formatParamName. Without an engine format the schema name is
// used as is.
func formatArgName(s string) string {
	if name, ok := engineName(s, camelNames); ok {
		s = name
	}
	return formatParamName(s)
}

// enumValueName formats an enum value into the Go suffix of its scoped
// constant name (`<Enum><Value>`): PASCAL with uppercase acronyms, like every
// other Go identifier, so `TCP` becomes `TCP` (strcase gave `Tcp`) and
// `LEAVE_CONFLICT_MARKERS` stays `LeaveConflictMarkers`.
func enumValueName(s string) string {
	if name, ok := engineName(s, pascalNames); ok {
		return name
	}
	return strcase.ToCamel(s)
}

// legacyMethodName returns the name a field's method had before the engine
// formatted names, when it differs from the current one and doesn't collide
// with any other method of the parent, so the old name can be kept as a
// deprecated wrapper.
func legacyMethodName(f introspection.Field) (string, bool) {
	name := formatName(f.Name)
	legacy := legacyFormatName(f.Name)
	if legacy == name || f.ParentObject == nil {
		return "", false
	}
	if generatedMethodNames[legacy] {
		return "", false
	}
	for _, other := range f.ParentObject.Fields {
		if formatName(other.Name) == legacy {
			return "", false
		}
	}
	for _, iface := range f.ParentObject.Interfaces {
		if "As"+formatName(iface.Name) == legacy {
			return "", false
		}
	}
	return legacy, true
}

// generatedMethodNames are methods the templates generate on every object
// besides its fields.
var generatedMethodNames = map[string]bool{
	"With":              true,
	"WithGraphQLQuery":  true,
	"XXX_GraphQLType":   true,
	"XXX_GraphQLIDType": true,
	"XXX_GraphQLID":     true,
	"MarshalJSON":       true,
	"UnmarshalJSON":     true,
	"Concrete":          true,
}

// legacyEnumValueNames returns the names the scoped constants of an enum
// value and its aliases (the values sharing its wire value) had before the
// engine formatted names, where they differ from every current constant of the
// enum, so the old names can be kept as deprecated aliases.
func (funcs goTemplateFuncs) legacyEnumValueNames(enum introspection.Type, value introspection.EnumValue) []string {
	current := map[string]bool{}
	for _, other := range enum.EnumValues {
		current[funcs.formatEnum(enum.Name, other.Name)] = true
	}
	wireValue := cmp.Or(value.Directives.EnumValue(), value.Name)
	var legacy []string
	for _, other := range enum.EnumValues {
		if cmp.Or(other.Directives.EnumValue(), other.Name) != wireValue {
			continue
		}
		name := enum.Name + strcase.ToCamel(other.Name)
		if current[name] || slices.Contains(legacy, name) {
			continue
		}
		legacy = append(legacy, name)
	}
	return legacy
}
