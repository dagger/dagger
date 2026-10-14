package templates

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/internal/testnames"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

// identifierFuncs returns template funcs for a schema whose names the engine
// formatted.
func identifierFuncs(names ...string) typescriptTemplateFuncs {
	schema := &introspection.Schema{FormattedNames: map[introspection.NameFormat]map[string]string{}}
	for _, f := range NameFormats {
		schema.FormattedNames[f] = testnames.Format(names, f)
	}
	return typescriptTemplateFuncs{fullSchema: schema}
}

func TestIdentifierNames(t *testing.T) {
	funcs := identifierFuncs(
		"shortSha", "parentShas", "withGPU", "http", "sshfsVolume", "llm",
		"pushUrl", "callId", "default", "function",
		"TCP", "OBJECT_KIND", "GITHUB_REPO", "PerSession", "DIRECTORY_TYPE",
		"LLMContentBlockKind", "JSONValue", "_internal",
	)

	// methods: CAMEL / UPPERCASE, escaped
	require.Equal(t, "shortSHA", funcs.formatMethodName("shortSha"))
	require.Equal(t, "parentSHAs", funcs.formatMethodName("parentShas"))
	require.Equal(t, "withGPU", funcs.formatMethodName("withGPU"))
	require.Equal(t, "function_", funcs.formatMethodName("function"))
	require.Equal(t, "shortSha", funcs.legacyMethodName("shortSha"))

	// args: CAMEL / UPPERCASE; escaped only as parameters
	require.Equal(t, "pushURL", funcs.argName("pushUrl"))
	require.Equal(t, "callID", funcs.formatArgName("callId"))
	require.Equal(t, "default", funcs.argName("default"))
	require.Equal(t, "default_", funcs.formatArgName("default"))

	// a leading underscore is never dropped
	require.Equal(t, "_internal", funcs.formatMethodName("_internal"))

	// Opts types and enum converters: PASCAL / UPPERCASE
	require.Equal(t, "ClientHTTPOpts", funcs.optsTypeName("Client", "http"))
	require.Equal(t, "ClientHttpOpts", funcs.legacyOptsTypeName("Client", "http"))
	require.Equal(t, "ClientSSHFSVolumeOpts", funcs.optsTypeName("Client", "sshfsVolume"))
	require.Equal(t, "ClientLLMOpts", funcs.optsTypeName("Client", "llm"))
	require.Equal(t, "LLMContentBlockKind", funcs.pascalCase("LLMContentBlockKind"))
	require.Equal(t, "JSONValue", funcs.pascalCase("JSONValue"))

	// enum members: PASCAL / CAPITALIZED, like the legacy converter
	require.Equal(t, "Tcp", funcs.formatEnum("TCP"))
	require.Equal(t, "ObjectKind", funcs.formatEnum("OBJECT_KIND"))
	require.Equal(t, "GitHubRepo", funcs.formatEnum("GITHUB_REPO"))
	require.Equal(t, "PerSession", funcs.formatEnum("PerSession"))
	require.Equal(t, "DirectoryType", funcs.formatEnum("DIRECTORY_TYPE"))

	// only renamed args are mapped back to their schema name
	optional := &introspection.TypeRef{Kind: introspection.TypeKindScalar, Name: "String"}
	renamed := funcs.renamedOpts(introspection.Field{Name: "f", Args: introspection.InputValues{
		{Name: "pushUrl", TypeRef: optional}, {Name: "default", TypeRef: optional}, {Name: "notInSchema", TypeRef: optional},
	}})
	require.Equal(t, []optsKey{{Key: "pushURL", Wire: "pushUrl", Legacy: "pushUrl"}}, renamed)
}

// A renamed core field or argument keeps its old schema name's method and
// opts key as deprecated aliases; renamed args send the new name.
func TestRenamedCoreNames(t *testing.T) {
	funcs := identifierFuncs("shortSHA", "pushURL", "callID", "url")
	optional := &introspection.TypeRef{Kind: introspection.TypeKindScalar, Name: "String"}
	commit := &introspection.Type{Name: "GitCommit"}
	require.Equal(t, "shortSha", funcs.legacyMethodName(funcs.legacyFieldName(introspection.Field{Name: "shortSHA", ParentObject: commit})))
	require.Equal(t, "shortSHA", funcs.legacyMethodName(funcs.legacyFieldName(introspection.Field{Name: "shortSHA"})))

	withRemote := introspection.Field{Name: "withRemote", ParentObject: &introspection.Type{Name: "GitRepository"}, Args: introspection.InputValues{
		{Name: "url", TypeRef: optional}, {Name: "pushURL", TypeRef: optional},
	}}
	require.Equal(t, []optsKey{{Key: "pushURL", Wire: "pushURL", Legacy: "pushUrl"}}, funcs.renamedOpts(withRemote))
	opts := optsFieldList(withRemote, withRemote.Args)
	require.Equal(t, "pushUrl", opts.LegacyKey(withRemote.Args[1], "pushURL"))
	require.Empty(t, opts.LegacyKey(withRemote.Args[0], "url"))

	input := inputFieldList("LLMContentBlockInput", introspection.InputValues{{Name: "callID"}})
	require.Equal(t, "callId", input.LegacyKey(introspection.InputValue{Name: "callID"}, "callID"))
	require.Empty(t, inputFieldList("Other", nil).LegacyKey(introspection.InputValue{Name: "callID"}, "callID"))
}

// Without engine-formatted names, every name goes through the legacy
// converters.
func TestIdentifierNamesFallback(t *testing.T) {
	for _, funcs := range []typescriptTemplateFuncs{
		{},
		{fullSchema: &introspection.Schema{}},
		identifierFuncs("somethingElse"),
	} {
		require.Equal(t, "shortSha", funcs.formatMethodName("shortSha"))
		require.Equal(t, "function_", funcs.formatMethodName("function"))
		require.Equal(t, "pushUrl", funcs.argName("pushUrl"))
		require.Equal(t, "default_", funcs.formatArgName("default"))
		require.Equal(t, "ClientHttpOpts", funcs.optsTypeName("Client", "http"))
		require.Equal(t, "ClientLLMOpts", funcs.optsTypeName("Client", "llm"))
		require.Equal(t, "LLMContentBlockKind", funcs.pascalCase("LLMContentBlockKind"))
		require.Equal(t, "Tcp", funcs.formatEnum("TCP"))
		require.Empty(t, funcs.renamedOpts(introspection.Field{Args: introspection.InputValues{
			{Name: "pushUrl", TypeRef: &introspection.TypeRef{Kind: introspection.TypeKindScalar, Name: "String"}},
		}}))
	}
}

// Generated classes are named after their GraphQL type, and the runtime looks
// them up by that name, so the generator never reformats type names. That is
// only consistent because type names in schemas with Query.formatIdentifiers
// are already canonical: the engine formats them unchanged.
func TestCoreTypeNamesAreCanonical(t *testing.T) {
	schema := currentSchema
	if !schema.HasFormatIdentifiers() {
		t.Skip("engine schema has no Query.formatIdentifiers")
	}
	var names []string
	for _, typ := range schema.Types {
		if strings.HasPrefix(typ.Name, "_") {
			// internal types, never generated
			continue
		}
		names = append(names, typ.Name)
	}
	formatted, ok, err := introspection.FormatNames(t.Context(), currentDag, schema, currentSchemaVersion, names, pascalNames.Casing, pascalNames.Acronyms)
	require.NoError(t, err)
	require.True(t, ok)
	for _, name := range names {
		require.Equal(t, name, formatted[name])
	}
}
