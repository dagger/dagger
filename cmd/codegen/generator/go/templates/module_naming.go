package templates

import (
	"strings"

	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/engine/naming"
)

// This file names the module's own types, fields and arguments as the engine
// does, for the strings the module side sends over the wire or merges into
// the schema. naming.go formats Go identifiers from schema names; this is the
// other direction, from Go source names to schema names.

// identifierNamingVersion is the first engine version whose modules the
// engine names with engine/naming (core.IdentifierNamingVersion); older
// modules keep the strcase rules. The -0 prerelease floor includes every
// v1.0.0 prerelease, like the engine's view gate.
const identifierNamingVersion = "v1.0.0-0"

// moduleNamer names the module's own types, fields, arguments and enum
// members the way the engine does for the module's engine version (see
// core.Namer in core/gqlformat.go): engine/naming from
// identifierNamingVersion, strcase before. The bindings the module calls
// itself through, and the schema it merges its types into, must use the
// engine's names or the references break. cmd/codegen cannot import core
// (core imports cmd/codegen/introspection), so keep this in sync.
//
// The zero moduleNamer is the legacy strcase one.
type moduleNamer struct {
	dict *naming.Dictionary
	// schema is the schema of the module's dependencies (core included),
	// for resolving references to their types like the engine does.
	schema *introspection.Schema
}

func (funcs goTemplateFuncs) moduleNamer() moduleNamer {
	schema := funcs.fullSchema
	if schema == nil {
		schema = funcs.schema
	}
	if funcs.schemaVersion == "" || funcs.CommonFunctions == nil {
		// No version is the latest, as for the engine's views.
		return moduleNamer{dict: naming.Latest, schema: schema}
	}
	if !funcs.CheckVersionCompatibility(identifierNamingVersion) {
		return moduleNamer{schema: schema}
	}
	return moduleNamer{dict: naming.DictionaryFor(funcs.schemaVersion), schema: schema}
}

// typeName maps a parsed type name to the name the engine installs, or
// resolves, it under. Module-local types (moduleName != "") are namespaced
// with the module name, as the engine does when installing the module's
// typedefs. A core or dependency type is referred to by its Go binding's
// name, which the engine normalizes; if that misses the schema, the engine
// falls back to the dependency type with the same normalized name (see
// Module.depTypeReference), which a legacy module only does for types of
// dependency modules, not core types.
func (n moduleNamer) typeName(name, moduleName string) string {
	if moduleName != "" {
		return n.namespaceTypeName(name, moduleName)
	}
	normalized := n.objectName(name)
	if n.schema == nil || n.schema.Types.Get(normalized) != nil {
		return normalized
	}
	for _, t := range n.schema.Types {
		if t.Name != name && n.objectName(t.Name) != normalized {
			continue
		}
		if n.dict == nil {
			if sourceMap := t.Directives.SourceMap(); sourceMap == nil || sourceMap.Module == "" {
				// a core type
				continue
			}
		}
		return t.Name
	}
	return normalized
}

func (n moduleNamer) format(name string, casing naming.Casing) string {
	id, err := n.dict.Parse(name)
	if err != nil {
		return name
	}
	return id.Format(casing, naming.Uppercase)
}

// objectName is the schema name of an object, interface or enum.
func (n moduleNamer) objectName(name string) string {
	if n.dict == nil {
		return strcase.ToCamel(name)
	}
	return n.format(name, naming.Pascal)
}

// fieldName is the schema name of a field, function or argument.
func (n moduleNamer) fieldName(name string) string {
	if n.dict == nil {
		return strcase.ToLowerCamel(name)
	}
	return n.format(name, naming.Camel)
}

// enumMemberName is the schema name of an enum member: names already in
// GraphQL's member convention (HTTP2, P_256) are kept, everything else
// becomes SCREAMING_SNAKE.
func (n moduleNamer) enumMemberName(name string) string {
	if isConventionalGraphQLEnumMemberName(name) {
		return name
	}
	if n.dict == nil {
		return strcase.ToScreamingSnake(name)
	}
	return n.format(name, naming.ScreamingSnake)
}

// namespaceTypeName mirrors the engine's NamespaceObject, which the engine
// applies to module objects, interfaces and enums alike, for the case where
// the module's final and original names are equal — always true for the
// module's own view of itself.
func (n moduleNamer) namespaceTypeName(typeName, moduleName string) string {
	if n.dict == nil {
		typeName = strcase.ToCamel(typeName)
		modName := strcase.ToCamel(moduleName)
		if rest := strings.TrimPrefix(typeName, modName); rest != typeName {
			if len(rest) == 0 {
				// The main module object keeps the module's name.
				return modName
			}
			// Only treat the prefix as a namespace on a word boundary: type
			// "Postman" in module "post" must become "PostPostman", while
			// "PostMan" is already namespaced.
			if 'A' <= rest[0] && rest[0] <= 'Z' {
				return typeName
			}
		}
		return strcase.ToCamel(modName + "_" + typeName)
	}
	typ, err := n.dict.Parse(typeName)
	if err != nil {
		return n.objectName(moduleName + "_" + typeName)
	}
	mod, err := n.dict.Parse(moduleName)
	if err != nil {
		return n.objectName(moduleName + "_" + typeName)
	}
	// The prefix check compares words, so it only matches on a word
	// boundary.
	words := typ.Words
	if hasWordPrefix(typ.Words, mod.Words) {
		words = typ.Words[len(mod.Words):]
	}
	words = append(append([]naming.Word(nil), mod.Words...), words...)
	return naming.Identifier{Words: words}.Format(naming.Pascal, naming.Uppercase)
}

func hasWordPrefix(words, prefix []naming.Word) bool {
	if len(prefix) > len(words) {
		return false
	}
	for i, w := range prefix {
		if !strings.EqualFold(words[i].Text, w.Text) || !strings.EqualFold(words[i].Suffix, w.Suffix) {
			return false
		}
	}
	return true
}
