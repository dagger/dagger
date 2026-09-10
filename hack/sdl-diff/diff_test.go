package main

import (
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

func TestSemanticDiff(t *testing.T) {
	for _, tc := range []struct {
		name, before, after, want string
	}{
		{
			name:   "workspace additions and removal",
			before: `type Workspace implements Node { id: ID! reloaded: Workspace! }`,
			after: `type Workspace implements Node & Syncer {
  id: ID!
  sync: ID! @expectedType(name: "Workspace")
  withConfigEnvironment(name: String!): Workspace!
  withConfigPaths(configFile: String!, lockFile: String!): Workspace!
}`,
			want: `extend type Workspace implements Syncer {
  sync: ID! @expectedType(name: "Workspace")
  withConfigEnvironment(name: String!): Workspace!
  withConfigPaths(configFile: String!, lockFile: String!): Workspace!
}

# Removed:
# extend type Workspace {
#   reloaded: Workspace!
# }`,
		},
		{
			name:   "new types and enum input union extensions",
			before: `enum Mode { ON } input Options { x: Int } union Result = Foo type Foo { id: ID! }`,
			after:  `enum Mode { ON OFF } input Options { x: Int y: Int = 2 } union Result = Foo | Bar type Bar { id: ID! } type Foo { id: ID! }`,
			want: `extend enum Mode {
  OFF
}

extend input Options {
  y: Int = 2
}

extend union Result = Bar

type Bar {
  id: ID!
}`,
		},
		{
			name:   "changed field signatures",
			before: `type Foo { field(a: Int = 1): String @deprecated(reason: "old") same: ID }`,
			after:  `type Foo { field(a: Int! = 2): String! same: ID }`,
			want: `# Changed (before):
# extend type Foo {
#   field(a: Int = 1): String @deprecated(reason: "old")
# }

# Changed (after):
# extend type Foo {
#   field(a: Int! = 2): String!
# }`,
		},
		{
			name:   "removed type",
			before: `scalar Old type Query { same: ID }`,
			after:  `type Query { same: ID }`,
			want: `# Removed:
# scalar Old`,
		},
		{
			name:   "new partial extension",
			before: `scalar ID`,
			after:  `scalar ID extend type Foo { added: ID }`,
			want: `extend type Foo {
  added: ID
}`,
		},
		{
			name:   "schema order for APIs fields and arguments",
			before: `type Alpha { unchanged: ID } type Zebra { goneZ: ID goneA: ID z: Int a: Int } scalar RemovedZ scalar RemovedA`,
			after:  `type Zebra { a: String z: String newZ(z: Int, a: Int): ID newA: ID } type Alpha { unchanged: ID z: ID a: ID } type NewZ { z: ID a: ID } scalar NewA`,
			want: `extend type Zebra {
  newZ(z: Int, a: Int): ID
  newA: ID
}

# Removed:
# extend type Zebra {
#   goneZ: ID
#   goneA: ID
# }

# Changed (before):
# extend type Zebra {
#   z: Int
#   a: Int
# }

# Changed (after):
# extend type Zebra {
#   a: String
#   z: String
# }

extend type Alpha {
  z: ID
  a: ID
}

type NewZ {
  z: ID
  a: ID
}

scalar NewA

# Removed:
# scalar RemovedZ

# Removed:
# scalar RemovedA`,
		},
		{
			name:   "changed signatures preserve argument and default order",
			before: `type Foo { f(z: Input = {z: 1, a: 2}, a: Int): ID @tag(z: 1, a: 2) }`,
			after:  `type Foo { f(a: Int, z: Input = {a: 2, z: 1}): String @tag(a: 2, z: 1) }`,
			want: `# Changed (before):
# extend type Foo {
#   f(z: Input = {z:1,a:2}, a: Int): ID @tag(z: 1, a: 2)
# }

# Changed (after):
# extend type Foo {
#   f(a: Int, z: Input = {a:2,z:1}): String @tag(a: 2, z: 1)
# }`,
		},
		{
			name:   "replacement preserves full declaration order",
			before: `type Foo implements Z & A { z: ID a: ID }`,
			after:  `interface Foo implements Z & A { z: ID a: ID }`,
			want: `# Changed (before):
# type Foo implements Z & A {
#   z: ID
#   a: ID
# }

# Changed (after):
# interface Foo implements Z & A {
#   z: ID
#   a: ID
# }`,
		},
		{
			name: "ignore ordering descriptions and extension placement",
			before: `"docs" type Foo implements A & B {
  "field" f("arg" x: Input = {a: 1, b: [2, 3]}, y: Int): ID @tag(a: 1, b: 2)
  z: ID
}
enum Mode { ON OFF }
union Result = Foo | Bar
directive @tag(a: Int, b: Int) on FIELD_DEFINITION | OBJECT`,
			after: `directive @tag(b: Int, a: Int) on OBJECT | FIELD_DEFINITION
union Result = Bar | Foo
enum Mode { OFF ON }
type Foo implements B & A { z: ID }
extend type Foo { f(y: Int, x: Input = {b: [2, 3], a: 1}): ID @tag(b: 2, a: 1) }`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := testDiff(t, tc.before, tc.after, false)
			if strings.TrimSpace(got) != tc.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, tc.want)
			}
			if got != "" && !strings.HasPrefix(got, "#") {
				if _, err := parser.ParseSchema(&ast.Source{Input: got}); err != nil {
					t.Fatalf("additions must be parseable SDL: %v", err)
				}
			}
		})
	}
}

func TestSemanticDiffChanges(t *testing.T) {
	for _, tc := range []struct{ name, before, after string }{
		{"list default order", `type Foo { f(x: [Int] = [1,2]): ID }`, `type Foo { f(x: [Int] = [2,1]): ID }`},
		{"directive order", `type Foo @a @b { f: ID }`, `type Foo @b @a { f: ID }`},
		{"directive definition", `directive @tag(x: Int) on OBJECT`, `directive @tag(x: Int!) repeatable on OBJECT`},
		{"schema root", `schema { query: Foo }`, `schema { query: Bar }`},
		{"enum metadata", `enum Mode { ON }`, `enum Mode { ON @deprecated }`},
		{"type kind", `type Foo { x: ID }`, `interface Foo { x: ID }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := testDiff(t, tc.before, tc.after, false)
			if !strings.Contains(got, "# Changed (before):") || !strings.Contains(got, "# Changed (after):") {
				t.Fatalf("expected both sides of replacement, got:\n%s", got)
			}
		})
	}
}

func TestSemanticDiffDescriptions(t *testing.T) {
	before, after := `type Foo { "old" f: ID }`, `type Foo { "new" f: ID }`
	if got := testDiff(t, before, after, false); got != "" {
		t.Fatalf("unexpected description change:\n%s", got)
	}
	if got := testDiff(t, before, after, true); !strings.Contains(got, "old") || !strings.Contains(got, "new") {
		t.Fatalf("missing descriptions:\n%s", got)
	}
}

func testDiff(t *testing.T, before, after string, descriptions bool) string {
	t.Helper()
	a, err := parseDocument("before", before, descriptions)
	if err != nil {
		t.Fatal(err)
	}
	b, err := parseDocument("after", after, descriptions)
	if err != nil {
		t.Fatal(err)
	}
	return semanticDiff(a, b)
}
