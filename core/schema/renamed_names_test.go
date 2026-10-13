package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
	gqlparser "github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

// renamedCoreNames are the core names the naming rules renamed, by type: the
// canonical name of each field, argument (field.arg) or input field, and its
// old spelling.
var renamedCoreNames = []struct {
	typeName, canonical, old string
}{
	{"GitCommit", "shortSHA", "shortSha"},
	{"GitCommit", "parentSHAs", "parentShas"},
	{"Artifacts", "filterURI", "filterUri"},
	{"Artifacts", "withoutURI", "withoutUri"},
	{"LLMContentBlock", "callID", "callId"},
	{"LLMContentBlockInput", "callID", "callId"},
	{"LLM", "withToolResult.callID", "withToolResult.callId"},
	{"GitRepository", "withRemote.pushURL", "withRemote.pushUrl"},
	{"Workspace", "withSDK.asSDKName", "withSDK.asSdkName"},
}

// coreSchemaName finds a field, an argument (field.arg) or an input field of
// typeName in schema.
func coreSchemaName(schema *ast.Schema, typeName, name string) *ast.Directive {
	def := schema.Types[typeName]
	if def == nil {
		return nil
	}
	field, arg, isArg := cutDot(name)
	fieldDef := def.Fields.ForName(field)
	if fieldDef == nil {
		return nil
	}
	dirs := fieldDef.Directives
	if isArg {
		argDef := fieldDef.Arguments.ForName(arg)
		if argDef == nil {
			return nil
		}
		dirs = argDef.Directives
	}
	if dep := dirs.ForName("deprecated"); dep != nil {
		return dep
	}
	return &ast.Directive{Name: "present"}
}

func cutDot(s string) (string, string, bool) {
	for i := range s {
		if s[i] == '.' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

func enumValueNames(def *ast.Definition) []string {
	var names []string
	for _, value := range def.EnumValues {
		names = append(names, value.Name)
	}
	return names
}

// The v1 views have the canonical names, with the old spellings as hidden
// aliases: in no schema clients read, but in the one queries are validated
// against, deprecated.
func TestCoreRenamedNames(t *testing.T) {
	_, dag := newNestingTestServer(t, "v1.0.0")
	schema := dag.SchemaForView("v1.0.0")
	aliased := dag.SchemaWithAliasesForView("v1.0.0")
	for _, name := range renamedCoreNames {
		require.NotNil(t, coreSchemaName(schema, name.typeName, name.canonical), "%s.%s", name.typeName, name.canonical)
		require.Nil(t, coreSchemaName(schema, name.typeName, name.old), "%s.%s", name.typeName, name.old)
		dep := coreSchemaName(aliased, name.typeName, name.old)
		require.NotNil(t, dep, "%s.%s", name.typeName, name.old)
		require.Equal(t, "deprecated", dep.Name, "%s.%s", name.typeName, name.old)
	}

	policy := schema.Types["FunctionCachePolicy"]
	require.Equal(t, []string{"DEFAULT", "PER_SESSION", "NEVER"}, enumValueNames(policy))
	for _, value := range policy.EnumValues {
		require.Empty(t, value.Directives, value.Name)
	}
	require.Equal(t, []string{"DEFAULT", "Default", "PER_SESSION", "PerSession", "NEVER", "Never"},
		enumValueNames(aliased.Types["FunctionCachePolicy"]))

	// Queries using the old spellings validate.
	for _, query := range []string{
		`query($id: ID!) { node(id: $id) { ... on GitCommit { shortSha parentShas } } }`,
		`query($id: ID!) { node(id: $id) { ... on Artifacts { filterUri(uri: "x") { withoutUri(uri: "y") { uri } } } } }`,
		`query($id: ID!) { node(id: $id) { ... on LLM { withToolResult(callId: "c", content: "", errored: false, blocks: [{kind: TEXT, callId: "c"}]) { id } } } }`,
		`query($id: ID!) { node(id: $id) { ... on LLMContentBlock { callId } } }`,
		`query($id: ID!) { node(id: $id) { ... on GitRepository { withRemote(name: "origin", url: "u", pushUrl: "p") { id } } } }`,
		`query($id: ID!) { node(id: $id) { ... on Workspace { withSDK(ref: "r", asSdkName: "s") { id } } } }`,
		`query($id: ID!) { node(id: $id) { ... on Function { withCachePolicy(policy: PerSession) { id } } } }`,
	} {
		_, errs := gqlparser.LoadQuery(aliased, query)
		require.Empty(t, errs, query)
		_, errs = gqlparser.LoadQuery(schema, query)
		require.NotEmpty(t, errs, "the schema itself doesn't accept %s", query)
	}

	// Both spellings of a policy decode to the same value, and so the same
	// literal: same IDs and cache keys.
	for canonical, old := range map[string]string{"DEFAULT": "Default", "PER_SESSION": "PerSession", "NEVER": "Never"} {
		decoded, err := core.FunctionCachePolicyEnum.DecodeInput(canonical)
		require.NoError(t, err)
		require.Equal(t, core.FunctionCachePolicy(old), decoded)
		require.Equal(t, call.NewLiteralEnum(old), decoded.ToLiteral())
		decoded, err = core.FunctionCachePolicyEnum.DecodeInput(old)
		require.NoError(t, err)
		require.Equal(t, core.FunctionCachePolicy(old), decoded)
	}

	// An LLMContentBlockInput decodes its old callId field as callID.
	for _, key := range []string{"callID", "callId"} {
		decoded, err := (dagql.InputObject[core.LLMContentBlockInput]{}).Decoder().DecodeInput(map[string]any{
			"kind": "TOOL_CALL", key: "call-1", "toolName": "t", "arguments": "{}",
		})
		require.NoError(t, err, key)
		block := decoded.(dagql.InputObject[core.LLMContentBlockInput])
		require.Equal(t, "call-1", block.Value.CallID, key)
		require.Contains(t, block.ToLiteral().Display(), "callID", key)
	}
}

// Views before the naming rules keep the old names exactly: there,
// FunctionCachePolicy's values are the PascalCase ones, with no aliases, and
// the other renamed names are on fields and types only v1 views have.
func TestCoreRenamedNamesPreV1(t *testing.T) {
	_, dag := newNestingTestServer(t, "v0.21.0")
	schema := dag.SchemaForView("v0.21.0")
	aliased := dag.SchemaWithAliasesForView("v0.21.0")
	require.Equal(t, []string{"Default", "PerSession", "Never"}, enumValueNames(schema.Types["FunctionCachePolicy"]))
	require.Equal(t, schema.Types["FunctionCachePolicy"], aliased.Types["FunctionCachePolicy"])
	for _, value := range schema.Types["FunctionCachePolicy"].EnumValues {
		require.Empty(t, value.Directives, value.Name)
	}
	for _, name := range renamedCoreNames {
		require.Nil(t, coreSchemaName(schema, name.typeName, name.canonical), "%s.%s is a v1 name", name.typeName, name.canonical)
		require.Nil(t, coreSchemaName(schema, name.typeName, name.old), "%s.%s is only in v1 views", name.typeName, name.old)
	}
	_, errs := gqlparser.LoadQuery(aliased, `query($id: ID!) { node(id: $id) { ... on Function { withCachePolicy(policy: PER_SESSION) { id } } } }`)
	require.NotEmpty(t, errs, "the new names aren't in the old views")
}
