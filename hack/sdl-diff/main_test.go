package main

import (
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

func TestNormalizeDescriptions(t *testing.T) {
	described := `
"schema docs" schema { query: Query }
"directive docs" directive @tag("argument docs" name: String = "default") repeatable on FIELD_DEFINITION
"type docs" type Query {
  # ordinary comment
  "field docs" value("first argument" a: String, "second argument" b: Int = 1): String @tag
}
"input docs" input Options { "input field docs" enabled: Boolean = true }
"enum docs" enum Mode { "value docs" ON }
extend type Query { "extra docs" extra: String }
`
	plain := `
schema { query: Query }
directive @tag(name: String = "default") repeatable on FIELD_DEFINITION
type Query { value(a: String, b: Int = 1): String @tag }
input Options { enabled: Boolean = true }
enum Mode { ON }
extend type Query { extra: String }
`
	want, err := normalize("plain", plain, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := normalize("described", described, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("descriptions/formatting changed the API diff:\n%s\nwant:\n%s", got, want)
	}
	withDescriptions, err := normalize("described", described, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, description := range []string{"schema docs", "directive docs", "argument docs", "type docs", "field docs", "first argument", "second argument", "input docs", "input field docs", "enum docs", "value docs", "extra docs"} {
		if !strings.Contains(withDescriptions, description) {
			t.Errorf("missing description %q", description)
		}
	}
}

func TestNormalizePreservesAPI(t *testing.T) {
	input := `
directive @expectedType(name: String!) on FIELD_DEFINITION | ARGUMENT_DEFINITION
scalar WorkspaceID
interface Syncer { sync: ID! }
type Workspace implements Syncer {
  __internal: Workspace
  sync: ID! @expectedType(name: "Workspace")
  withConfigPaths(configFile: String!, lockFile: String!): Workspace!
  withConfigEnvironment(name: String!): Workspace!
  edit(paths: [String!]! = [], flag: Boolean = false, text: String = "a\nb", options: Options = {n: 1}): Workspace @deprecated(reason: "old")
}
input Options { n: Int = 2 }
enum Mode { ON OFF @deprecated }
union Result = Workspace | Other
extend type Workspace { extra: [String] }
`
	got, err := normalize("schema", input, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, signature := range []string{
		"directive @expectedType(name: String!) on FIELD_DEFINITION | ARGUMENT_DEFINITION",
		"scalar WorkspaceID", "interface Syncer", "type Workspace implements Syncer",
		"__internal: Workspace", `sync: ID! @expectedType(name: "Workspace")`,
		"withConfigPaths(configFile: String!, lockFile: String!): Workspace!",
		"withConfigEnvironment(name: String!): Workspace!",
		`edit(paths: [String!]! = [], flag: Boolean = false, text: String = "a\nb", options: Options = {n:1}): Workspace @deprecated(reason: "old")`,
		"n: Int = 2", "OFF @deprecated", "union Result = Workspace | Other", "extend type Workspace", "extra: [String]",
	} {
		if !strings.Contains(got, signature) {
			t.Errorf("missing signature %q in:\n%s", signature, got)
		}
	}
	if _, err := parser.ParseSchema(&ast.Source{Input: got}); err != nil {
		t.Fatalf("normalized output is invalid SDL: %v", err)
	}
}

func TestNormalizeRejectsInvalidSDL(t *testing.T) {
	if _, err := normalize("broken.graphql", "type Query { broken: }", false); err == nil {
		t.Fatal("expected a parse error")
	}
}
