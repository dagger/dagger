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
	renamed := funcs.renamedArgs(introspection.InputValues{
		{Name: "pushUrl"}, {Name: "default"}, {Name: "notInSchema"},
	})
	require.Len(t, renamed, 1)
	require.Equal(t, "pushUrl", renamed[0].Name)
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
		require.Empty(t, funcs.renamedArgs(introspection.InputValues{{Name: "pushUrl"}}))
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
