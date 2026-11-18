package naming

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// Core schema names are written by hand, not normalized, so they're the
// largest real-world corpus of conventional names. docs/docs-graphql's
// schema is the latest view, which follows the GraphQL casing convention
// (from engine.IdentifierCasingVersion on): type names are PascalCase with
// uppercase acronyms (HTTPClient), field, argument and input field names
// camelCase with capitalized acronyms (asJson), and enum values
// SCREAMING_SNAKE_CASE. These names don't, and are allowed to; keep it
// empty, and give the reason for any name added. (core/schema's
// TestCoreSchemaGraphQLCasing checks the live schema's views too.)
var nonConventionalCoreNames = map[string]bool{}

// legacyLoaderName reports a legacy load<Type>FromID root field, kept as it
// is for old clients and replaced by node(id:). They're not in the latest
// view, but would be exempt.
func legacyLoaderName(name string) bool {
	return strings.HasPrefix(name, "load") && strings.HasSuffix(name, "FromID")
}

func loadCoreSchema(t *testing.T) *ast.SchemaDocument {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "docs-graphql", "schema.graphqls")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("core schema not available: %v", err)
	}
	doc, err := parser.ParseSchema(&ast.Source{Name: path, Input: string(src)})
	if err != nil {
		t.Fatalf("parse core schema: %v", err)
	}
	return doc
}

type coreNames struct {
	types, members, enumValues []string
}

func collectCoreNames(t *testing.T) coreNames {
	doc := loadCoreSchema(t)
	types := map[string]bool{}
	members := map[string]bool{}
	enumValues := map[string]bool{}
	for _, defs := range []ast.DefinitionList{doc.Definitions, doc.Extensions} {
		for _, def := range defs {
			types[def.Name] = true
			for _, f := range def.Fields {
				members[f.Name] = true
				for _, arg := range f.Arguments {
					members[arg.Name] = true
				}
			}
			for _, v := range def.EnumValues {
				enumValues[v.Name] = true
			}
		}
	}
	sorted := func(m map[string]bool) []string {
		var s []string
		for k := range m {
			s = append(s, k)
		}
		sort.Strings(s)
		return s
	}
	return coreNames{sorted(types), sorted(members), sorted(enumValues)}
}

func TestCoreSchemaConventional(t *testing.T) {
	names := collectCoreNames(t)
	t.Logf("%d type names, %d field/argument names, %d enum values",
		len(names.types), len(names.members), len(names.enumValues))

	for _, name := range names.types {
		if got := mustParse(t, name).Format(Pascal, Uppercase); got != name && !nonConventionalCoreNames[name] {
			t.Errorf("type %q is not conventional: %q", name, got)
		}
	}
	for _, name := range names.members {
		if legacyLoaderName(name) {
			continue
		}
		if got := mustParse(t, name).Format(Camel, Capitalized); got != name && !nonConventionalCoreNames[name] {
			t.Errorf("field/argument %q is not conventional: %q", name, got)
		}
	}
	for _, name := range names.enumValues {
		if got := mustParse(t, name).Format(ScreamingSnake, Uppercase); got != name && !nonConventionalCoreNames[name] {
			t.Errorf("enum value %q is not conventional: %q", name, got)
		}
	}
	for name := range nonConventionalCoreNames {
		id := mustParse(t, name)
		if id.Format(Pascal, Uppercase) == name || id.Format(Camel, Capitalized) == name || id.Format(ScreamingSnake, Uppercase) == name {
			t.Errorf("%q is conventional now; drop it from the allowlist", name)
		}
	}
}

// The guarantees hold across the core schema, not just the test vectors.
func TestCoreSchemaGuarantees(t *testing.T) {
	names := collectCoreNames(t)
	all := append(append(append([]string{}, names.types...), names.members...), names.enumValues...)
	for _, name := range all {
		checkIdempotent(t, name)
		if !hasHeuristicAcronym(mustParse(t, name)) {
			checkRoundTrip(t, name)
		}
	}
}

func hasHeuristicAcronym(id Identifier) bool {
	for _, w := range id.Words {
		if w.Kind == KindAcronym && w.Term == nil {
			return true
		}
	}
	return false
}
