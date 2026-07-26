package core

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// objectToolsTestSchema builds a small module-shaped schema for exercising the
// object-tools generation helpers. Object arguments cross the wire as `ID`
// scalars carrying an @expectedType directive, mirroring the real schema.
func objectToolsTestSchema(t *testing.T) *ast.Schema {
	t.Helper()
	schema, err := gqlparser.LoadSchema(&ast.Source{
		Name: "test.graphql",
		Input: `
directive @expectedType(name: String!) on ARGUMENT_DEFINITION

type Query { doug: Doug! }

type Workspace { id: ID! }
type Changeset { id: ID! }
type LLM { id: ID! }

"A coding agent."
type Doug {
  id: ID!
  sync: Doug!

  "Read a file."
  read(
    source: ID! @expectedType(name: "Workspace"),
    filePath: String!,
    offset: Int! = 0,
  ): String!

  "Write a file."
  write(
    source: ID! @expectedType(name: "Workspace"),
    filePath: String!,
    contents: String!,
  ): Changeset!

  "Update the TODO list."
  todoWrite(pending: [String!]! = []): Doug!

  "Build an agent — requires an object arg, so ineligible."
  agent(base: ID! @expectedType(name: "LLM")): LLM!

  old: String! @deprecated(reason: "gone")
}
`,
	})
	require.NoError(t, err)
	return schema
}

func fieldByName(def *ast.Definition, name string) *ast.FieldDefinition {
	for _, f := range def.Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func TestObjectToolEligible(t *testing.T) {
	schema := objectToolsTestSchema(t)
	doug := schema.Types["Doug"]

	// Methods whose required args are all scalars (or the auto-injected Workspace)
	// are eligible.
	require.True(t, objectToolEligible(fieldByName(doug, "read"), nil))
	require.True(t, objectToolEligible(fieldByName(doug, "write"), nil))
	require.True(t, objectToolEligible(fieldByName(doug, "todoWrite"), nil))

	// A required object-typed argument (LLM, not the auto-injected Workspace)
	// disqualifies the method — the model has no handle to pass.
	require.False(t, objectToolEligible(fieldByName(doug, "agent"), nil))

	// except drops a method by name.
	require.False(t, objectToolEligible(fieldByName(doug, "read"), []string{"read"}))

	// Reserved / internal / deprecated fields are never tools.
	require.False(t, objectToolEligible(fieldByName(doug, "id"), nil))
	require.False(t, objectToolEligible(fieldByName(doug, "sync"), nil))
	require.False(t, objectToolEligible(fieldByName(doug, "old"), nil))
}

func TestObjectMethodSchema(t *testing.T) {
	schema := objectToolsTestSchema(t)
	doug := schema.Types["Doug"]

	readSchema, err := objectMethodSchema(schema, fieldByName(doug, "read"))
	require.NoError(t, err)
	props := readSchema["properties"].(map[string]any)

	// The auto-injected Workspace argument is hidden from the model.
	require.NotContains(t, props, "source")

	// Scalar args are surfaced with their JSON types; required tracks non-null
	// args without a default.
	require.Equal(t, "string", props["filePath"].(map[string]any)["type"])
	require.Equal(t, "integer", props["offset"].(map[string]any)["type"])
	require.EqualValues(t, 0, props["offset"].(map[string]any)["default"])
	require.Equal(t, []string{"filePath"}, readSchema["required"])
	require.Equal(t, false, readSchema["additionalProperties"])

	// A list arg with a default is optional and rendered as an array of scalars.
	todoSchema, err := objectMethodSchema(schema, fieldByName(doug, "todoWrite"))
	require.NoError(t, err)
	todoProps := todoSchema["properties"].(map[string]any)
	pending := todoProps["pending"].(map[string]any)
	require.Equal(t, "array", pending["type"])
	require.Equal(t, "string", pending["items"].(map[string]any)["type"])
	require.NotContains(t, todoSchema, "required") // pending has a default
}

func TestArgTypeToJSONSchema(t *testing.T) {
	schema := objectToolsTestSchema(t)

	// An `ID` scalar (object handle) renders as a plain string.
	idType := &ast.Type{NamedType: "ID", NonNull: true}
	got, err := argTypeToJSONSchema(schema, idType)
	require.NoError(t, err)
	require.Equal(t, "string", got["type"])

	// A nested list of scalars recurses.
	listType := &ast.Type{Elem: &ast.Type{NamedType: "String", NonNull: true}, NonNull: true}
	got, err = argTypeToJSONSchema(schema, listType)
	require.NoError(t, err)
	require.Equal(t, "array", got["type"])
	require.Equal(t, "string", got["items"].(map[string]any)["type"])
}
