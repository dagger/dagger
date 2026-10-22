package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
	gqlparser "github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vektah/gqlparser/v2/validator/rules"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
)

// renamedCoreNames are the core names renamed at
// engine.IdentifierCasingVersion, by type: the canonical name of a field, an
// argument (field.arg) or an input field, and its old spelling.
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

// renamedPolicyValues are FunctionCachePolicy's values: old spelling to
// canonical.
var renamedPolicyValues = []struct{ old, canonical string }{
	{"Default", "DEFAULT"},
	{"PerSession", "PER_SESSION"},
	{"Never", "NEVER"},
}

// hasCoreSchemaName reports whether schema has a field, an argument
// (field.arg) or an input field of typeName.
func hasCoreSchemaName(schema *ast.Schema, typeName, name string) bool {
	def := schema.Types[typeName]
	if def == nil {
		return false
	}
	fieldName, argName, isArg := cutDot(name)
	field := def.Fields.ForName(fieldName)
	if field == nil {
		return false
	}
	if isArg {
		return field.Arguments.ForName(argName) != nil
	}
	return true
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

func validationErrors(schema *ast.Schema, query string) gqlerror.List {
	_, errs := gqlparser.LoadQueryWithRules(schema, query, rules.NewDefaultRules())
	return errs
}

// Queries using the old names, and the same queries with the canonical ones.
var (
	oldNameQueries = []string{
		`query($id: ID!) { node(id: $id) { ... on GitCommit { shortSha parentShas } } }`,
		`query($id: ID!) { node(id: $id) { ... on Artifacts { filterUri(uri: "x") { withoutUri(uri: "y") { uri } } } } }`,
		`query($id: ID!) { node(id: $id) { ... on LLM { withToolResult(callId: "c", content: "", errored: false, blocks: [{kind: TEXT, callId: "c"}]) { id } } } }`,
		`query($id: ID!) { node(id: $id) { ... on LLMContentBlock { callId } } }`,
		`query($id: ID!) { node(id: $id) { ... on GitRepository { withRemote(name: "origin", url: "u", pushUrl: "p") { id } } } }`,
		`query($id: ID!) { node(id: $id) { ... on Workspace { withSDK(ref: "r", asSdkName: "s") { id } } } }`,
	}
	canonicalNameQueries = []string{
		`query($id: ID!) { node(id: $id) { ... on GitCommit { shortSHA parentSHAs } } }`,
		`query($id: ID!) { node(id: $id) { ... on Artifacts { filterURI(uri: "x") { withoutURI(uri: "y") { uri } } } } }`,
		`query($id: ID!) { node(id: $id) { ... on LLM { withToolResult(callID: "c", content: "", errored: false, blocks: [{kind: TEXT, callID: "c"}]) { id } } } }`,
		`query($id: ID!) { node(id: $id) { ... on LLMContentBlock { callID } } }`,
		`query($id: ID!) { node(id: $id) { ... on GitRepository { withRemote(name: "origin", url: "u", pushURL: "p") { id } } } }`,
		`query($id: ID!) { node(id: $id) { ... on Workspace { withSDK(ref: "r", asSDKName: "s") { id } } } }`,
		`query($id: ID!) { node(id: $id) { ... on Function { withCachePolicy(policy: PER_SESSION) { id } } } }`,
	}
	// The old policy values stay valid in every view.
	oldPolicyQuery = `query($id: ID!) { node(id: $id) { ... on Function { withCachePolicy(policy: PerSession) { id } } } }`
)

// Callers declaring a version before engine.IdentifierCasingVersion see only
// the old names; from it on, including dev builds and the v1.0.0 release,
// only the canonical ones. FunctionCachePolicy keeps its old values in every
// view, and adds the canonical ones from the cut on. Versions are declared
// client versions, mapped to views as the engine does.
func TestCoreRenamedNamesViews(t *testing.T) {
	for _, tc := range []struct {
		version   string
		canonical bool
	}{
		{version: "v1.0.0-0"},
		{version: "v1.0.0-beta.17"},
		{version: "v1.0.0-beta.20"},
		{version: engine.IdentifierCasingVersion, canonical: true},
		{version: "v1.0.0-beta.22", canonical: true},
		{version: "v1.0.0-dev-0123456789ab", canonical: true},
		{version: "v1.0.0", canonical: true},
	} {
		t.Run(tc.version, func(t *testing.T) {
			_, dag := newNestingTestServer(t, call.View(engine.APIViewVersion(tc.version)))
			schema := dag.SchemaForView(dag.View)
			for _, name := range renamedCoreNames {
				require.Equal(t, tc.canonical, hasCoreSchemaName(schema, name.typeName, name.canonical), "%s.%s", name.typeName, name.canonical)
				require.Equal(t, !tc.canonical, hasCoreSchemaName(schema, name.typeName, name.old), "%s.%s", name.typeName, name.old)
			}

			policy := schema.Types["FunctionCachePolicy"]
			require.NotNil(t, policy)
			want := []string{"Default", "PerSession", "Never"}
			if tc.canonical {
				want = []string{"Default", "PerSession", "Never", "DEFAULT", "PER_SESSION", "NEVER"}
			}
			require.Equal(t, want, enumValueNames(policy))

			valid, invalid := oldNameQueries, canonicalNameQueries
			if tc.canonical {
				valid, invalid = invalid, valid
			}
			for _, query := range append(valid, oldPolicyQuery) {
				require.Empty(t, validationErrors(schema, query), query)
			}
			for _, query := range invalid {
				require.NotEmpty(t, validationErrors(schema, query), query)
			}
		})
	}
}

// The internal view "" is the latest API: canonical names. Engines before
// the cut recorded workspace checkpoints' withRemote(pushUrl:) and agent tool
// results' withToolResult(callId:) there, so it keeps accepting those old
// spellings as hidden internal arguments, and their recipes still load
// (TestLLMCommittedLeafRestore loads one).
func TestCoreRenamedNamesInternalView(t *testing.T) {
	_, dag := newNestingTestServer(t, "")
	schema := dag.SchemaForView("")
	recorded := map[string]string{"withRemote.pushUrl": "GitRepository", "withToolResult.callId": "LLM"}
	for _, name := range renamedCoreNames {
		require.True(t, hasCoreSchemaName(schema, name.typeName, name.canonical), "%s.%s", name.typeName, name.canonical)
		if _, ok := recorded[name.old]; ok {
			continue // internal: checked below
		}
		require.False(t, hasCoreSchemaName(schema, name.typeName, name.old), "%s.%s", name.typeName, name.old)
	}
	for name, typeName := range recorded {
		fieldName, argName, _ := cutDot(name)
		objType, ok := dag.ObjectType(typeName)
		require.True(t, ok)
		field, ok := objType.FieldSpec(fieldName, "")
		require.True(t, ok)
		arg, ok := field.Args.Input(argName, "")
		require.True(t, ok, name)
		require.True(t, arg.Internal, name)
		_, ok = field.Args.Input(argName, call.View(engine.APIViewVersion("v1.0.0")))
		require.False(t, ok, "clients past the cut can't send %s", name)
	}
}

// Views before v1 keep FunctionCachePolicy's old values. The other renamed
// names are on types and fields only v1 views have.
func TestCoreRenamedNamesPreV1(t *testing.T) {
	_, dag := newNestingTestServer(t, "v0.21.0")
	schema := dag.SchemaForView(dag.View)
	require.Equal(t, []string{"Default", "PerSession", "Never"}, enumValueNames(schema.Types["FunctionCachePolicy"]))
	for _, name := range renamedCoreNames {
		require.False(t, hasCoreSchemaName(schema, name.typeName, name.canonical), "%s.%s", name.typeName, name.canonical)
	}
}

// Both spellings of a policy decode to the same value and literal, so IDs and
// cache keys don't depend on the view.
func TestFunctionCachePolicySpellings(t *testing.T) {
	for _, value := range renamedPolicyValues {
		for _, spelling := range []string{value.old, value.canonical} {
			decoded, err := core.FunctionCachePolicyEnum.DecodeInput(spelling)
			require.NoError(t, err, spelling)
			require.Equal(t, core.FunctionCachePolicy(value.old), decoded, spelling)
			require.Equal(t, call.NewLiteralEnum(value.old), decoded.ToLiteral(), spelling)
			looked, err := core.FunctionCachePolicyEnum.Lookup(spelling)
			require.NoError(t, err, spelling)
			require.Equal(t, core.FunctionCachePolicy(value.old), looked, spelling)
		}
	}
}

// Each spelling of a renamed argument or input field reaches the resolver.
func TestCoreRenamedArgsDecode(t *testing.T) {
	for _, key := range []string{"callID", "callId"} {
		decoded, err := (dagql.InputObject[core.LLMContentBlockInput]{}).Decoder().DecodeInput(map[string]any{
			"kind": "TOOL_CALL", key: "call-1", "toolName": "t", "arguments": "{}",
		})
		require.NoError(t, err, key)
		block := decoded.(dagql.InputObject[core.LLMContentBlockInput])
		require.Equal(t, "call-1", block.Value.ToLLMContentBlock().CallID, key)
	}

	s := &gitSchema{}
	for _, args := range []withRemoteArgs{
		{Name: "origin", URL: "https://example.com/a.git", PushURL: "https://example.com/b.git"},
		{Name: "origin", URL: "https://example.com/a.git", LegacyPushURL: "https://example.com/b.git"},
	} {
		repo, err := s.withRemote(t.Context(), &core.GitRepository{}, args)
		require.NoError(t, err)
		require.Len(t, repo.Remotes, 1)
		require.Equal(t, "https://example.com/b.git", repo.Remotes[0].PushURL)
	}
}
