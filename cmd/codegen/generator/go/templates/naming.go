package templates

import (
	"cmp"
	"slices"
	"strings"

	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/engine/naming"
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

// Legacy names
//
// A renamed Go identifier keeps its old name as a deprecated alias wherever Go
// allows one: a wrapper method, a type alias, an option struct alias, an
// extra option struct field or an enum constant. The old name is the legacy
// conversion (legacyFormatName) of the schema name the declaration had before
// engine/naming:
//
//   - Core names are written by hand, so the schema name didn't change: only
//     its Go formatting did (`parentShas` was ParentShas, is ParentSHAs).
//   - A module's names are its declarations' original names normalized by
//     its naming rules, which since engine/naming are no longer strcase for
//     modules at v1.0.0 and later: `daggerCli` became `daggerCLI`. The engine
//     still accepts the old schema name as a hidden alias, but the schema
//     doesn't show it, so it's recovered here by applying the legacy strcase
//     namer to the current name: the new name only recases the words of the
//     original, and strcase recases them the same way either way. That's a
//     heuristic: when it misses, there's just no alias. Names of modules still
//     named by strcase are strcase's own output, which it leaves as is.
//
// See hack/designs/identifier-casing.md, "Transition aliases".

// moduleOwned reports whether directives mark a module's declaration (a
// dependency's, or the module's own), as opposed to a core one: the engine
// gives every module typedef a @sourceMap naming its module.
func moduleOwned(directives introspection.Directives) bool {
	sourceMap := directives.SourceMap()
	return sourceMap != nil && sourceMap.Module != ""
}

// isModuleType reports whether t is declared by a module.
func isModuleType(t *introspection.Type) bool {
	return t != nil && moduleOwned(t.Directives)
}

// isModuleField reports whether f is declared by a module: a field of a
// module type, or a field a module adds to Query (its constructor).
func isModuleField(f introspection.Field) bool {
	return moduleOwned(f.Directives) || isModuleType(f.ParentObject)
}

// legacyFieldSchemaName is the schema name a field had before engine/naming:
// the name itself for core fields, strcase's for module fields.
func legacyFieldSchemaName(f introspection.Field) string {
	if !isModuleField(f) {
		return f.Name
	}
	return legacyRename(f.Name, strcase.ToLowerCamel(f.Name), naming.Camel)
}

// legacyArgSchemaName is legacyFieldSchemaName for an argument of f.
func legacyArgSchemaName(f introspection.Field, arg introspection.InputValue) string {
	if !isModuleField(f) && !moduleOwned(arg.Directives) {
		return arg.Name
	}
	return legacyRename(arg.Name, strcase.ToLowerCamel(arg.Name), naming.Camel)
}

// legacyRename returns legacy, the name strcase gives name, if name can be a
// rename by the new rules: if it's their own spelling of it. A module named by
// strcase has no renames, but strcase doesn't always leave its own output as
// is (e2ETest becomes e2Etest), and the new rules spell such names otherwise
// (e2eTest). Otherwise it's name.
func legacyRename(name, legacy string, casing naming.Casing) string {
	if legacy == name {
		return name
	}
	id, err := naming.Parse(name)
	if err != nil || id.Format(casing, naming.Uppercase) != name {
		return name
	}
	return legacy
}

// legacyTypeSchemaName is legacyFieldSchemaName for a type: strcase's
// PascalCase for module types. The engine namespaced a module type by
// applying strcase to the module name and the type name joined, so the part
// after the module's name is put back that way. A module's ID scalar (FooID,
// in views that have them) is named after its type.
func legacyTypeSchemaName(t *introspection.Type) string {
	if t == nil {
		return ""
	}
	if isModuleType(t) {
		return legacyNamespacedName(t.Name, t.Directives.SourceMap().Module)
	}
	if t.Kind == introspection.TypeKindScalar {
		if base, ok := strings.CutSuffix(t.Name, "ID"); ok && base != "" {
			if schema := generator.GetSchema(); schema != nil {
				if parent := schema.Types.Get(base); isModuleType(parent) {
					return legacyTypeSchemaName(parent) + "ID"
				}
			}
		}
	}
	return t.Name
}

// legacyNamespacedName is the name the legacy strcase rules gave the type of
// module modName that the new rules named name: e.g. DaggerCliHttpclient for
// DaggerCLIHTTPClient of module dagger-cli, as strcase.ToCamel of
// "dagger-cli_HTTPClient".
func legacyNamespacedName(name, modName string) string {
	legacy := strcase.ToCamel(name)
	if id, err := naming.Parse(modName); err == nil && modName != "" {
		prefix := id.Format(naming.Pascal, naming.Uppercase)
		if rest, ok := strings.CutPrefix(name, prefix); ok {
			if rest == "" {
				legacy = strcase.ToCamel(modName)
			} else {
				legacy = strcase.ToCamel(modName + "_" + rest)
			}
		}
	}
	return legacyRename(name, legacy, naming.Pascal)
}

// typeGoName is the Go name of a type: enums keep their schema name.
func typeGoName(t *introspection.Type) string {
	if t.Kind == introspection.TypeKindEnum {
		return t.Name
	}
	return formatName(t.Name)
}

// legacyTypeGoName is the Go name a type had before engine/naming.
func legacyTypeGoName(t *introspection.Type) string {
	legacy := legacyTypeSchemaName(t)
	if t.Kind == introspection.TypeKindEnum {
		return legacy
	}
	return legacyFormatName(legacy)
}

// legacyTypeName returns the Go name t had before engine/naming, when it
// differs from the current one and from the current and earlier legacy names
// of every other type of the schema, so it can be kept as a deprecated alias.
func (funcs goTemplateFuncs) legacyTypeName(t *introspection.Type) string {
	if t == nil {
		return ""
	}
	switch t.Kind {
	case introspection.TypeKindObject, introspection.TypeKindInterface,
		introspection.TypeKindEnum, introspection.TypeKindScalar:
	default:
		return ""
	}
	legacy := legacyTypeGoName(t)
	if legacy == "" || legacy == typeGoName(t) || funcs.fullSchema == nil {
		return ""
	}
	for _, other := range funcs.fullSchema.Types {
		if other.Name == t.Name {
			continue
		}
		if typeGoName(other) == legacy {
			return ""
		}
		if t.Kind == introspection.TypeKindInterface && typeGoName(other) == legacy+"Client" {
			return ""
		}
		if other.Kind == introspection.TypeKindInterface && funcs.interfaceClientName(other.Name) == legacy {
			return ""
		}
		// Two types with the same old name: keep the first one's.
		if other.Name < t.Name && legacyTypeGoName(other) == legacy {
			return ""
		}
	}
	return legacy
}

// legacyOptionalArgName returns the Go name of an optional argument's field
// in f's options struct before engine/naming, when it differs from its
// current name and from every other field of the struct, so it can be kept
// as a deprecated field.
func (funcs goTemplateFuncs) legacyOptionalArgName(f introspection.Field, arg introspection.InputValue) string {
	if !funcs.isArgOptional(arg) {
		return ""
	}
	legacy := legacyFormatName(legacyArgSchemaName(f, arg))
	if legacy == formatName(arg.Name) {
		return ""
	}
	for _, other := range f.Args {
		if other.Name == arg.Name || !funcs.isArgOptional(other) {
			continue
		}
		if formatName(other.Name) == legacy {
			return ""
		}
		// Two arguments with the same old name: keep the first one's.
		if other.Name < arg.Name && legacyFormatName(legacyArgSchemaName(f, other)) == legacy {
			return ""
		}
	}
	return legacy
}

// legacyMethodName returns the name a field's method had before the engine
// formatted names, when it differs from the current one and doesn't collide
// with any other method of the parent, so the old name can be kept as a
// deprecated wrapper.
func legacyMethodName(f introspection.Field) (string, bool) {
	name := formatName(f.Name)
	legacy := legacyFormatName(legacyFieldSchemaName(f))
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
		// Two fields with the same old name: keep the first one's.
		if other.Name < f.Name && legacyFormatName(legacyFieldSchemaName(*other)) == legacy {
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
// enum, so the old names can be kept as deprecated aliases. A renamed module
// enum keeps them under its old name too.
func (funcs goTemplateFuncs) legacyEnumValueNames(enum introspection.Type, value introspection.EnumValue) []string {
	current := map[string]bool{}
	for _, other := range enum.EnumValues {
		current[funcs.formatEnum(enum.Name, other.Name)] = true
	}
	prefixes := []string{enum.Name}
	if legacyEnum := funcs.legacyTypeName(&enum); legacyEnum != "" {
		prefixes = append(prefixes, legacyEnum)
	}
	wireValue := cmp.Or(value.Directives.EnumValue(), value.Name)
	var legacy []string
	for _, prefix := range prefixes {
		for _, other := range enum.EnumValues {
			if cmp.Or(other.Directives.EnumValue(), other.Name) != wireValue {
				continue
			}
			name := prefix + strcase.ToCamel(other.Name)
			if current[name] || slices.Contains(legacy, name) {
				continue
			}
			legacy = append(legacy, name)
		}
	}
	return legacy
}
