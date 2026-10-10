package templates

import (
	"strings"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

// TypeScript identifiers for schema names.
//
// When the schema has Query.formatIdentifiers (schema views at v1.0.0-0 and
// above), names are the schema's names as the engine formats them:
//
//   - methods, args and opts keys: CAMEL / UPPERCASE (httpClient, withGPU);
//   - "Opts" types and enum converters: PASCAL / UPPERCASE (ClientHTTPOpts);
//   - enum members: PASCAL / CAPITALIZED, the closest match to what the
//     generator has always written (Tcp, ObjectKind).
//
// Otherwise each name falls back to the legacy converter, so older schemas
// generate byte-identical code.
//
// Only TypeScript identifiers change. Everything sent over the wire (field
// names in select, arg keys, input object fields, enum values) and every type
// name stays exactly as the schema has it: the runtime looks generated classes
// up by their GraphQL type name (clientGen[typedef.name], __loadCoreObject),
// and type names in views with Query.formatIdentifiers are already canonical
// anyway.

var (
	camelNames             = introspection.NameFormat{Casing: introspection.CasingCamel, Acronyms: introspection.AcronymsUppercase}
	pascalNames            = introspection.NameFormat{Casing: introspection.CasingPascal, Acronyms: introspection.AcronymsUppercase}
	pascalCapitalizedNames = introspection.NameFormat{Casing: introspection.CasingPascal, Acronyms: introspection.AcronymsCapitalized}
)

// NameFormats are the formats TypeScript codegen asks the engine for: load
// them onto the schema with LoadFormattedNames before rendering.
var NameFormats = []introspection.NameFormat{camelNames, pascalNames, pascalCapitalizedNames}

// engineName returns a schema name as the engine formatted it in f, if the
// schema has engine formats. Names with a leading underscore (internal names,
// like _DirectiveApplication) keep the legacy conversion, which keeps the
// underscore.
func (funcs typescriptTemplateFuncs) engineName(name string, f introspection.NameFormat) (string, bool) {
	if strings.HasPrefix(name, "_") {
		return "", false
	}
	schema := funcs.fullSchema
	if schema == nil {
		schema = generator.GetSchema()
	}
	return schema.FormattedName(name, f)
}

// formatMethodName returns the TS name of the method generated for a field.
func (funcs typescriptTemplateFuncs) formatMethodName(name string) string {
	if formatted, ok := funcs.engineName(name, camelNames); ok {
		return funcs.formatName(formatted)
	}
	return funcs.formatName(name)
}

// legacyMethodName returns the TS name the generator gave a field's method
// before the engine formatted names, kept as a deprecated alias when it
// differs.
func (funcs typescriptTemplateFuncs) legacyMethodName(name string) string {
	return funcs.formatName(name)
}

// argName returns the TS name of an argument, unescaped: it is the key of the
// argument in the method's Opts type.
func (funcs typescriptTemplateFuncs) argName(name string) string {
	if formatted, ok := funcs.engineName(name, camelNames); ok {
		return formatted
	}
	return name
}

// formatArgName returns the TS name of an argument used as a parameter,
// escaped against reserved words.
func (funcs typescriptTemplateFuncs) formatArgName(name string) string {
	return funcs.formatName(funcs.argName(name))
}

// pascalCase converts a schema name into a PascalCase identifier part, for
// the names the generator derives from schema names: "<Parent><Field>Opts"
// types and the "<Enum>ValueToName" / "<Enum>NameToValue" converters.
func (funcs typescriptTemplateFuncs) pascalCase(name string) string {
	if formatted, ok := funcs.engineName(name, pascalNames); ok {
		return formatted
	}
	return toPascalCase(name)
}

// optsTypeName returns the name of a field's Opts type; parent is the TS name
// of the parent type (Client for Query).
func (funcs typescriptTemplateFuncs) optsTypeName(parent, field string) string {
	return parent + funcs.pascalCase(field) + "Opts"
}

// legacyOptsTypeName returns the name the generator gave a field's Opts type
// before the engine formatted names, kept as a deprecated alias when it
// differs.
func (funcs typescriptTemplateFuncs) legacyOptsTypeName(parent, field string) string {
	return parent + toPascalCase(field) + "Opts"
}

// formatEnum returns the TS name of an enum member.
func (funcs typescriptTemplateFuncs) formatEnum(name string) string {
	if formatted, ok := funcs.engineName(name, pascalCapitalizedNames); ok {
		return formatted
	}
	return toPascalCase(name)
}

// renamedArgs returns the args whose TS name differs from their schema name.
// Their Opts keys are mapped back to the schema name before the call.
func (funcs typescriptTemplateFuncs) renamedArgs(values introspection.InputValues) introspection.InputValues {
	var renamed introspection.InputValues
	for _, v := range values {
		if funcs.argName(v.Name) != v.Name {
			renamed = append(renamed, v)
		}
	}
	return renamed
}

// optsFields is the dot of the "field" template: the fields of an Opts or
// input type. Opts keys are TS identifiers, mapped back to the schema name in
// the method body; input object fields go over the wire as they are.
type optsFields struct {
	Fields introspection.InputValues
	Opts   bool
}

func optsFieldList(fields introspection.InputValues) optsFields {
	return optsFields{Fields: fields, Opts: true}
}

func inputFieldList(fields introspection.InputValues) optsFields {
	return optsFields{Fields: fields}
}
