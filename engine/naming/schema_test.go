package naming

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// Core schema names are written by hand, not normalized, so they're the
// largest real-world corpus of canonical names. These are the field and
// argument names that aren't canonical with the Initial dictionary.
var nonCanonicalCoreNames = map[string]bool{
	"asSdkName":  true,
	"callId":     true,
	"filterUri":  true,
	"parentShas": true,
	"pushUrl":    true,
	"shortSha":   true,
	"withoutUri": true,
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

func TestCoreSchemaCanonical(t *testing.T) {
	names := collectCoreNames(t)
	t.Logf("%d type names, %d field/argument names, %d enum values",
		len(names.types), len(names.members), len(names.enumValues))

	for _, name := range names.types {
		if got := mustParse(t, name).Format(Pascal, Uppercase); got != name {
			t.Errorf("type %q is not canonical: %q", name, got)
		}
	}

	var nonCanonical []string
	for _, name := range names.members {
		if got := mustParse(t, name).Format(Camel, Uppercase); got != name {
			nonCanonical = append(nonCanonical, name)
			if !nonCanonicalCoreNames[name] {
				t.Errorf("field/argument %q is not canonical: %q", name, got)
			}
		}
	}
	t.Logf("%d non-canonical field/argument names: %v", len(nonCanonical), nonCanonical)
	for name := range nonCanonicalCoreNames {
		if got := mustParse(t, name).Format(Camel, Uppercase); got == name {
			t.Errorf("%q is canonical now; drop it from the allowlist", name)
		}
	}

	var legacyEnumValues []string
	for _, name := range names.enumValues {
		if got := mustParse(t, name).Format(ScreamingSnake, Uppercase); got != name {
			legacyEnumValues = append(legacyEnumValues, name)
		}
	}
	t.Logf("%d non-canonical enum values: %v", len(legacyEnumValues), legacyEnumValues)
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
