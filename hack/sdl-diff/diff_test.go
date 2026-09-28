package main

import (
	"fmt"
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
			want: `# Added: implements Syncer
extend type Workspace implements Syncer {
  # Added
  sync: ID! @expectedType(name: "Workspace")
  # Added
  withConfigEnvironment(name: String!): Workspace!
  # Added
  withConfigPaths(configFile: String!, lockFile: String!): Workspace!
  # Removed: reloaded: Workspace!
}`,
		},
		{
			name:   "new types and enum input union extensions",
			before: `enum Mode { ON } input Options { x: Int } union Result = Foo type Foo { id: ID! }`,
			after:  `enum Mode { ON OFF } input Options { x: Int y: Int = 2 } union Result = Foo | Bar type Bar { id: ID! } type Foo { id: ID! }`,
			want: `extend enum Mode {
  # Added
  OFF
}

extend input Options {
  # Added
  y: Int = 2
}

# Added: union member Bar
extend union Result = Bar

# Added
type Bar {
  id: ID!
}`,
		},
		{
			name:   "changed field signatures",
			before: `type Foo { field(a: Int = 1): String @deprecated(reason: "old") same: ID }`,
			after:  `type Foo { field(a: Int! = 2): String! same: ID }`,
			want: `extend type Foo {
  # Changed: previously field(a: Int = 1): String @deprecated(reason: "old")
  field(a: Int! = 2): String!
}`,
		},
		{
			name: "removed type", before: `scalar Old type Query { same: ID }`, after: `type Query { same: ID }`,
			want: `# Removed: scalar Old`,
		},
		{
			name: "new partial extension", before: `scalar ID`, after: `scalar ID extend type Foo { added: ID }`,
			want: `# Added
extend type Foo {
  added: ID
}`,
		},
		{
			name:   "schema order for APIs fields and arguments",
			before: `type Alpha { unchanged: ID } type Zebra { goneZ: ID goneA: ID z: Int a: Int } scalar RemovedZ scalar RemovedA`,
			after:  `type Zebra { a: String z: String newZ(z: Int, a: Int): ID newA: ID } type Alpha { unchanged: ID z: ID a: ID } type NewZ { z: ID a: ID } scalar NewA`,
			want: `extend type Zebra {
  # Changed: previously a: Int
  a: String
  # Changed: previously z: Int
  z: String
  # Added
  newZ(z: Int, a: Int): ID
  # Added
  newA: ID
  # Removed: goneZ: ID
  # Removed: goneA: ID
}

extend type Alpha {
  # Added
  z: ID
  # Added
  a: ID
}

# Added
type NewZ {
  z: ID
  a: ID
}

# Added
scalar NewA

# Removed: scalar RemovedZ

# Removed: scalar RemovedA`,
		},
		{
			name:   "changed signatures preserve argument and default order",
			before: `type Foo { f(z: Input = {z: 1, a: 2}, a: Int): ID @tag(z: 1, a: 2) }`,
			after:  `type Foo { f(a: Int, z: Input = {a: 2, z: 1}): String @tag(a: 2, z: 1) }`,
			want: `extend type Foo {
  # Changed: previously f(z: Input = {z:1,a:2}, a: Int): ID @tag(z: 1, a: 2)
  f(a: Int, z: Input = {a:2,z:1}): String @tag(a: 2, z: 1)
}`,
		},
		{
			name:   "kind edit omits unchanged members",
			before: `type Foo implements Z & A { z: ID a: ID }`,
			after:  `interface Foo implements Z & A { z: ID a: ID }`,
			want: `# Changed: previously type Foo
interface Foo`,
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
			if _, err := parser.ParseSchema(&ast.Source{Input: got}); err != nil {
				t.Fatalf("summary must be parseable SDL: %v", err)
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
		{"input default", `input Options { x: Int = 1 }`, `input Options { x: Int = 2 }`},
		{"input default added", `input Options { x: Int }`, `input Options { x: Int = 2 }`},
		{"schema extension directives", `extend schema @old`, `extend schema @new`},
		{"schema extension", `schema { query: Query } extend schema { mutation: Before }`, `schema { mutation: After query: Query }`},
		{"object to scalar", `type Foo { x: ID }`, `scalar Foo`},
		{"scalar to object", `scalar Foo`, `type Foo { x: ID }`},
		{"object to enum", `type Foo { x: ID }`, `enum Foo { ON }`},
		{"enum to object", `enum Foo { ON }`, `type Foo { x: ID }`},
		{"type kind", `type Foo { x: ID }`, `interface Foo { x: ID }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := testComparison(t, tc.before, tc.after, false)
			if _, err := parseDocument("summary", got.summary, false); err != nil {
				t.Fatalf("invalid after fragment: %v\n%s", err, got.summary)
			}
			if !strings.Contains(got.summary, "# Changed: previously ") ||
				(!strings.HasPrefix(got.details, "-") && !strings.Contains(got.details, "\n-")) ||
				!strings.Contains(got.details, "\n+") {
				t.Fatalf("expected annotation and detailed replacement: %+v", got)
			}
		})
	}
}

func TestSemanticDiffDescriptions(t *testing.T) {
	before := `type Workspace { "old field" export("old path" path: String!, "old from" from: Directory): String! unchanged: ID }`
	after := `type Workspace { "new field" export("new path" path: String!, "new from" from: Directory): String! unchanged: ID }`
	if got := testDiff(t, before, after, false); got != "" {
		t.Fatalf("unexpected description change:\n%s", got)
	}
	got := testComparison(t, before, after, true)
	want := `extend type Workspace {
  # Description changed: field, argument path, argument from
  """
  new field
  """
  export(
    """
    new path
    """
    path: String!

    """
    new from
    """
    from: Directory
  ): String!
}`
	if strings.TrimSpace(got.summary) != want {
		t.Fatalf("summary:\n%s\nwant:\n%s", got.summary, want)
	}
	for _, text := range []string{"old field", "old path", "old from", "unchanged", "# Changed"} {
		if strings.Contains(got.summary, text) {
			t.Errorf("unexpected %q in summary", text)
		}
	}
	for _, text := range []string{"-  old field\n+  new field", "-    old path\n+    new path", "-    old from\n+    new from"} {
		if !strings.Contains(got.details, text) {
			t.Errorf("missing %q in details:\n%s", text, got.details)
		}
	}
	if strings.Count(got.details, "export(") != 1 || strings.Contains(got.details, "unchanged") {
		t.Fatal(got.details)
	}
	// Comments must not alter the actual description text on reparse.
	doc, err := parseDocument("summary", got.summary, true)
	if err != nil {
		t.Fatal(err)
	}
	field := doc.Extensions[0].Fields[0]
	if field.Description != "new field" || field.Arguments.ForName("path").Description != "new path" {
		t.Fatal("annotation corrupted SDL descriptions")
	}
}

func TestDetailedDiffIgnoresReorderingWithRealChanges(t *testing.T) {
	before := `directive @tag(z: Int, a: Int) on OBJECT | FIELD_DEFINITION
schema { mutation: Mutation query: Query }
type Foo implements Z & A {
 f(z: Input = {z: 1, a: 2}, a: Int): ID @tag(z: 1, a: 2)
 "old" g: ID
 unchanged: ID
}
enum Mode { Z A }`
	after := `enum Mode { A Z }
schema { query: Query mutation: Mutation }
directive @tag(a: Int, z: Int) on FIELD_DEFINITION | OBJECT
type Foo implements A & Z {
 unchanged: ID
 "new" g: ID
 f(a: Int, z: Input = {a: 2, z: 1}): String @tag(a: 2, z: 1)
}`
	got := testComparison(t, before, after, true)
	want := ` extend type Foo {
-  f(a: Int, z: Input = {a:2,z:1}): ID @tag(a: 2, z: 1)
+  f(a: Int, z: Input = {a:2,z:1}): String @tag(a: 2, z: 1)
   """
-  old
+  new
   """
   g: ID
 }

`
	if got.details != want {
		t.Fatalf("details:\n%s\nwant:\n%s", got.details, want)
	}
	for _, text := range []string{"unchanged", "schema", "directive", "Mode", "implements"} {
		if strings.Contains(got.summary+got.details, text) {
			t.Errorf("unchanged %q leaked", text)
		}
	}
}

func TestDescriptionKindsAndInternalFields(t *testing.T) {
	for _, tc := range []struct{ name, before, after string }{
		{"type", `"old" type Foo { same: ID }`, `"new" type Foo { same: ID }`},
		{"scalar", `"old" scalar Foo`, `"new" scalar Foo`},
		{"union", `"old" union Foo = Bar`, `"new" union Foo = Bar`},
		{"input", `input Foo { "old" x: Int = 1 }`, `input Foo { "new" x: Int = 1 }`},
		{"enum", `enum Foo { "old" X }`, `enum Foo { "new" X }`},
		{"directive", `"old" directive @tag on OBJECT`, `"new" directive @tag on OBJECT`},
		{"directive argument", `directive @tag("old" x: Int) on OBJECT`, `directive @tag("new" x: Int) on OBJECT`},
		{"schema", `"old" schema { query: Query }`, `"new" schema { query: Query }`},
		{"internal extension", `type Foo { same: ID } extend type Foo { "old" __internal: ID }`, `type Foo { "new" __internal: ID same: ID }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := testComparison(t, tc.before, tc.after, true)
			if !strings.Contains(got.summary, "# Description changed") || strings.Contains(got.summary, "old") || !strings.Contains(got.details, "old") || !strings.Contains(got.details, "new") {
				t.Fatalf("unexpected comparison: %+v", got)
			}
			if got := testComparison(t, tc.before, tc.after, false); got != (schemaDiff{}) {
				t.Fatalf("descriptions disabled: %+v", got)
			}
		})
	}
}

func TestRemovalDetails(t *testing.T) {
	got := testComparison(t, `type Foo { gone: ID same: ID } enum Mode { ON OFF } union Result = Foo | Bar`, `type Foo { same: ID } enum Mode { ON } union Result = Foo`, false)
	for _, text := range []string{"# Removed: Foo.gone: ID", "# Removed: Mode.OFF", "# Removed: Result union member Bar"} {
		if !strings.Contains(got.summary, text) {
			t.Errorf("missing %q: %s", text, got.summary)
		}
	}
	if !strings.Contains(got.details, " extend type Foo {\n-  gone: ID\n }\n") {
		t.Fatal(got.details)
	}
	if _, err := parseDocument("removal summary", got.summary, false); err != nil {
		t.Fatalf("invalid removal summary: %v\n%s", err, got.summary)
	}
	if strings.Contains(got.summary+got.details, "same:") {
		t.Fatal("unchanged field leaked")
	}
}

func TestLineDiff(t *testing.T) {
	for _, tc := range []struct{ before, after, want string }{
		{"", "new\n", "+new\n"},
		{"old\n", "", "-old\n"},
		{"a\nb\nc\nd\n", "a\nB\nc\nD\n", " a\n-b\n+B\n c\n-d\n+D\n"},
		{"same\n", "same\n", " same\n"},
	} {
		if got := lineDiff(tc.before, tc.after); got != tc.want {
			t.Fatalf("got %q want %q", got, tc.want)
		}
	}
	// Exercise the bounded fallback, verifying that neither side loses content.
	var a, b strings.Builder
	for i := range 2000 {
		fmt.Fprintf(&a, "old %d\n", i)
		fmt.Fprintf(&b, "new %d\n", i)
	}
	got := lineDiff(a.String(), b.String())
	var old, new strings.Builder
	for line := range strings.SplitSeq(strings.TrimSuffix(got, "\n"), "\n") {
		if line[0] != '+' {
			old.WriteString(line[1:] + "\n")
		}
		if line[0] != '-' {
			new.WriteString(line[1:] + "\n")
		}
	}
	if old.String() != a.String() || new.String() != b.String() {
		t.Fatal("line diff lost content")
	}
}

func testComparison(t *testing.T, before, after string, descriptions bool) schemaDiff {
	t.Helper()
	a, err := parseDocument("before", before, descriptions)
	if err != nil {
		t.Fatal(err)
	}
	b, err := parseDocument("after", after, descriptions)
	if err != nil {
		t.Fatal(err)
	}
	return compareSchemas(a, b)
}

func testDiff(t *testing.T, before, after string, descriptions bool) string {
	t.Helper()
	return testComparison(t, before, after, descriptions).summary
}
