package core

import (
	"context"
	"strings"
	"sync"

	"github.com/iancoleman/strcase"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/naming"
)

/*
This formats comments in the schema as:
"""
comment
"""

Which avoids corner cases where the comment ends in a `"`.
*/
func formatGqlDescription(desc string) string {
	if desc == "" {
		return ""
	}
	return "\n" + strings.TrimSpace(desc) + "\n"
}

// IdentifierNamingVersion is the first engine version whose modules have
// their names normalized by engine/naming. Modules that declare an older
// engineVersion keep the strcase rules they were built against, byte for
// byte. See hack/designs/identifier-casing.md.
const IdentifierNamingVersion = "v1.0.0-0"

// Namer normalizes module-declared names (types, fields, functions,
// arguments, enum members) into schema names, and schema names into CLI
// names.
//
// A Namer is selected by the engine version of the module whose names it
// produces: engine/naming with that version's dictionary from
// IdentifierNamingVersion on, and the legacy strcase rules before it. The
// zero Namer is the legacy one.
type Namer struct {
	// dict is the naming dictionary, or nil for the legacy strcase rules.
	dict *naming.Dictionary
}

var (
	// LegacyNamer is the strcase normalization of modules older than
	// IdentifierNamingVersion.
	LegacyNamer = Namer{}
	// LatestNamer is the normalization of the newest engine version.
	LatestNamer = Namer{dict: naming.Latest}
)

// NamerForView returns the Namer for an API view. The empty view is the
// latest, as it is for AfterVersion.
func NamerForView(view call.View) Namer {
	if !AfterVersion(IdentifierNamingVersion).Contains(view) {
		return LegacyNamer
	}
	return Namer{dict: naming.DictionaryFor(string(view))}
}

// NamerForEngineVersion returns the Namer for a module that declares
// engineVersion in its dagger.json.
func NamerForEngineVersion(engineVersion string) Namer {
	return NamerForView(call.View(engine.APIViewVersion(engineVersion)))
}

// CallerView is the API view of the caller of the current field: the view
// recorded in the call when the field is view-scoped, else the view of the
// current server. Empty when neither is known.
func CallerView(ctx context.Context) call.View {
	if cur := dagql.CurrentCall(ctx); cur != nil && cur.View != "" {
		return cur.View
	}
	if srv := dagql.CurrentDagqlServer(ctx); srv != nil {
		return srv.View
	}
	return ""
}

// NamerFromContext returns the Namer for the view of the current call. Only
// fields with a view filter record the caller's view in their call (and
// cache key); the typedef constructors that normalize names have one, so a
// module's runtime gets the Namer of the engine version the module declares.
func NamerFromContext(ctx context.Context) Namer {
	if cur := dagql.CurrentCall(ctx); cur != nil {
		return NamerForView(cur.View)
	}
	return LatestNamer
}

// Legacy reports whether n uses the strcase rules.
func (n Namer) Legacy() bool {
	return n.dict == nil
}

// Dictionary returns n's naming dictionary, or nil for the legacy rules.
func (n Namer) Dictionary() *naming.Dictionary {
	return n.dict
}

// format parses name with n's dictionary and formats it. A name the parser
// rejects (non-ASCII, or no letters or digits) is returned unchanged:
// schema validation reports it.
func (n Namer) format(name string, casing naming.Casing) string {
	key := formatKey{dict: n.dict, casing: casing, name: name}
	if formatted, ok := formatCache.Load(key); ok {
		return formatted.(string)
	}
	formatted := name
	if id, err := n.dict.Parse(name); err == nil {
		formatted = id.Format(casing, naming.Uppercase)
	}
	formatCache.Store(key, formatted)
	return formatted
}

// formatCache memoizes Namer.format: the same schema names are normalized
// over and over (every lookup of a module's types and fields), and
// dictionaries never change.
var formatCache sync.Map

type formatKey struct {
	dict   *naming.Dictionary
	casing naming.Casing
	name   string
}

// ObjectName is the schema name of an object, interface, enum or scalar:
// PascalCase.
func (n Namer) ObjectName(name string) string {
	if n.Legacy() {
		return strcase.ToCamel(name)
	}
	return n.format(name, naming.Pascal)
}

// FieldName is the schema name of a field or function: camelCase.
func (n Namer) FieldName(name string) string {
	if n.Legacy() {
		return strcase.ToLowerCamel(name)
	}
	return n.format(name, naming.Camel)
}

// ArgName is the schema name of an argument: camelCase.
func (n Namer) ArgName(name string) string {
	return n.FieldName(name)
}

// EnumMemberName is the schema name of an enum member: SCREAMING_SNAKE, with
// names already written in GraphQL's member convention (HTTP2, P_256) kept
// as they are.
func (n Namer) EnumMemberName(name string) string {
	if isConventionalGraphQLEnumMemberName(name) {
		return name
	}
	if n.Legacy() {
		return strcase.ToScreamingSnake(name)
	}
	return n.format(name, naming.ScreamingSnake)
}

// CLIName is the CLI spelling of a name: kebab-case. Characters that can't
// be part of a name (glob metacharacters, ':' path separators) stay where
// they are, as they do with strcase, so path patterns convert too.
func (n Namer) CLIName(name string) string {
	if n.Legacy() {
		return strcase.ToKebab(name)
	}
	var b strings.Builder
	start := -1
	flush := func(end int) {
		if start >= 0 {
			b.WriteString(n.format(name[start:end], naming.Kebab))
			start = -1
		}
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if isNameByte(c) {
			if start < 0 {
				start = i
			}
			continue
		}
		flush(i)
		b.WriteByte(c)
	}
	flush(len(name))
	return b.String()
}

// isNameByte reports whether c can be part of a name in some casing: a
// letter, a digit, or a word separator.
func isNameByte(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return true
	case c == '-', c == '_', c == '.', c == ' ':
		return true
	default:
		return false
	}
}

// NamespaceObject maps a module-local type name to its namespaced schema
// name: the module's final name, followed by the type name unless the type
// name already starts with the module's original name.
func (n Namer) NamespaceObject(objOriginalName, modFinalName, modOriginalName string) string {
	if n.Legacy() {
		return legacyNamespaceObject(objOriginalName, modFinalName, modOriginalName)
	}
	obj, err := n.dict.Parse(objOriginalName)
	if err != nil {
		return n.ObjectName(modFinalName + "_" + objOriginalName)
	}
	final, err := n.dict.Parse(modFinalName)
	if err != nil {
		return n.ObjectName(modFinalName + "_" + objOriginalName)
	}
	rest := obj.Words
	// The prefix check compares words, not characters, so it only matches on
	// a word boundary: "Postman" in module "post" becomes "PostPostman",
	// while "PostMan" is already namespaced.
	if mod, err := n.dict.Parse(modOriginalName); err == nil && hasWordPrefix(obj.Words, mod.Words) {
		rest = obj.Words[len(mod.Words):]
	}
	words := append(append([]naming.Word(nil), final.Words...), rest...)
	return naming.Identifier{Words: words}.Format(naming.Pascal, naming.Uppercase)
}

// hasWordPrefix reports whether words starts with prefix, comparing words by
// their spelling, ignoring case.
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

// FieldNameCandidates are the field names a name typed in another casing
// (a CLI address, a pattern, a module name from a config file) may have in a
// schema: its spelling by the latest naming rules, then by the legacy ones
// when that differs. Which applies depends on the engine version of the
// module that installed the field, which a caller resolving the name doesn't
// know yet; it looks up each in order.
func FieldNameCandidates(name string) []string {
	latest := LatestNamer.FieldName(name)
	legacy := LegacyNamer.FieldName(name)
	if legacy == latest {
		return []string{latest}
	}
	return []string{latest, legacy}
}

// SameCLIName reports whether two names are the same once spelled the CLI
// way (kebab-case) by the latest or the legacy naming rules: "myMod",
// "my-mod" and "MyMod" are the same module. Comparisons of names whose
// module, and so naming rules, aren't known use it.
func SameCLIName(a, b string) bool {
	return a == b ||
		LatestNamer.CLIName(a) == LatestNamer.CLIName(b) ||
		LegacyNamer.CLIName(a) == LegacyNamer.CLIName(b)
}

// anyNamerMatches reports whether name, in any casing, normalizes to
// schemaName under the latest or the legacy rules. Lookups that don't know
// which rules produced schemaName (a name a user typed, matched before the
// owning module is known) use it, so the spellings both rules produce keep
// resolving.
func anyNamerMatches(schemaName, name string, normalize func(Namer, string) string) bool {
	return schemaName == name ||
		schemaName == normalize(LatestNamer, name) ||
		schemaName == normalize(LegacyNamer, name)
}

// legacyNamespaceObject is NamespaceObject by the legacy strcase rules,
// unchanged from before engine/naming.
func legacyNamespaceObject(
	objOriginalName string,
	modFinalName string,
	modOriginalName string,
) string {
	objOriginalName = strcase.ToCamel(objOriginalName)
	if rest := strings.TrimPrefix(objOriginalName, strcase.ToCamel(modOriginalName)); rest != objOriginalName {
		if len(rest) == 0 {
			// Main module object with same original name as module original name, give it
			// the same name as the module's final name
			return strcase.ToCamel(modFinalName)
		}
		// we have this case check here to check for a boundary
		// e.g. if objName="Postman" and namespace="Post", then we should still namespace
		// this to "PostPostman" instead of just going for "Postman" (but we should do that
		// if objName="PostMan")
		if 'A' <= rest[0] && rest[0] <= 'Z' {
			// objName has original module name prefixed, just make sure it has the final
			// module name as prefix
			return strcase.ToCamel(modFinalName + rest)
		}
	}

	// need to namespace object with final module name
	return strcase.ToCamel(modFinalName + "_" + objOriginalName)
}

func isConventionalGraphQLEnumMemberName(name string) bool {
	if name == "" || strings.HasPrefix(name, "__") {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'A' && c <= 'Z' {
			continue
		}
		if i > 0 && ((c >= '0' && c <= '9') || c == '_') {
			continue
		}
		return false
	}
	return true
}
