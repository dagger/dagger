package core

import (
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
)

func TestNamespaceObjects(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		testCase  string
		namespace string
		obj       string
		legacy    string
		latest    string
	}{
		{
			testCase:  "namespace",
			namespace: "Foo",
			obj:       "Bar",
			legacy:    "FooBar",
			latest:    "FooBar",
		},
		{
			testCase:  "namespace into camel case",
			namespace: "foo",
			obj:       "bar-baz",
			legacy:    "FooBarBaz",
			latest:    "FooBarBaz",
		},
		{
			testCase:  "don't namespace when equal",
			namespace: "foo",
			obj:       "Foo",
			legacy:    "Foo",
			latest:    "Foo",
		},
		{
			testCase:  "don't namespace when prefixed",
			namespace: "foo",
			obj:       "FooBar",
			legacy:    "FooBar",
			latest:    "FooBar",
		},
		{
			testCase:  "still namespace when prefixed if not full",
			namespace: "foo",
			obj:       "Foobar",
			legacy:    "FooFoobar",
			latest:    "FooFoobar",
		},
		{
			testCase:  "acronym type",
			namespace: "my-mod",
			obj:       "HTTPClient",
			legacy:    "MyModHttpclient",
			latest:    "MyModHTTPClient",
		},
		{
			testCase:  "acronym type already prefixed",
			namespace: "my-mod",
			obj:       "MyModHTTPClient",
			legacy:    "MyModHttpclient",
			latest:    "MyModHTTPClient",
		},
		{
			testCase:  "prefix compares words, not letters",
			namespace: "post",
			obj:       "Postman",
			legacy:    "PostPostman",
			latest:    "PostPostman",
		},
		{
			testCase:  "single capital word",
			namespace: "module",
			obj:       "ModuleAOverlay",
			legacy:    "ModuleAoverlay",
			latest:    "ModuleAOverlay",
		},
		{
			testCase:  "acronym module name",
			namespace: "my-llm-mod",
			obj:       "MyLlmMod",
			legacy:    "MyLlmMod",
			latest:    "MyLLMMod",
		},
		{
			testCase:  "dictionary spelling wins over author casing",
			namespace: "github-tools",
			obj:       "GithubToolsRepo",
			legacy:    "GithubToolsRepo",
			latest:    "GitHubToolsRepo",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.testCase, func(t *testing.T) {
			require.Equal(t, tc.legacy, LegacyNamer.NamespaceObject(tc.obj, tc.namespace, tc.namespace))
			require.Equal(t, tc.latest, LatestNamer.NamespaceObject(tc.obj, tc.namespace, tc.namespace))
		})
	}

	t.Run("renamed module", func(t *testing.T) {
		require.Equal(t, "OtherHTTPClient", LatestNamer.NamespaceObject("MyModHTTPClient", "other", "my-mod"))
		require.Equal(t, "Other", LatestNamer.NamespaceObject("MyMod", "other", "my-mod"))
		require.Equal(t, "OtherHttpclient", LegacyNamer.NamespaceObject("MyModHTTPClient", "other", "my-mod"))
	})
}

func TestNamerForView(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		view   call.View
		legacy bool
	}{
		{"", false},
		{"v1.0.0", false},
		{"v1.2.3", false},
		{"v0.21.5", true},
		{"v0.9.9", true},
	} {
		require.Equal(t, tc.legacy, NamerForView(tc.view).Legacy(), "view %q", tc.view)
	}
	require.False(t, NamerForEngineVersion("v1.0.0-beta.15").Legacy())
	require.True(t, NamerForEngineVersion("v0.19.4").Legacy())
}

func TestNamerNames(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		object, field [2]string
		enum, cli     [2]string
	}{
		{
			name:   "HTTPClient",
			object: [2]string{"Httpclient", "HTTPClient"},
			field:  [2]string{"httpclient", "httpClient"},
			enum:   [2]string{"HTTP_CLIENT", "HTTP_CLIENT"},
			cli:    [2]string{"http-client", "http-client"},
		},
		{
			name:   "LLMMessage",
			object: [2]string{"Llmmessage", "LLMMessage"},
			field:  [2]string{"llmmessage", "llmMessage"},
			enum:   [2]string{"LLM_MESSAGE", "LLM_MESSAGE"},
			cli:    [2]string{"llm-message", "llm-message"},
		},
		{
			name:   "E2ETest",
			object: [2]string{"E2Etest", "E2ETest"},
			field:  [2]string{"e2Etest", "e2eTest"},
			enum:   [2]string{"E_2_E_TEST", "E2E_TEST"},
			cli:    [2]string{"e-2-e-test", "e2e-test"},
		},
		{
			name:   "e2eTest",
			object: [2]string{"E2ETest", "E2ETest"},
			field:  [2]string{"e2ETest", "e2eTest"},
			enum:   [2]string{"E_2_E_TEST", "E2E_TEST"},
			cli:    [2]string{"e-2-e-test", "e2e-test"},
		},
		{
			name:   "userIds",
			object: [2]string{"UserIds", "UserIDs"},
			field:  [2]string{"userIds", "userIDs"},
			enum:   [2]string{"USER_IDS", "USER_IDS"},
			cli:    [2]string{"user-ids", "user-ids"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i, namer := range []Namer{LegacyNamer, LatestNamer} {
				require.Equal(t, tc.object[i], namer.ObjectName(tc.name), "legacy=%v", namer.Legacy())
				require.Equal(t, tc.field[i], namer.FieldName(tc.name), "legacy=%v", namer.Legacy())
				require.Equal(t, tc.enum[i], namer.EnumMemberName(tc.name), "legacy=%v", namer.Legacy())
				require.Equal(t, tc.cli[i], namer.CLIName(tc.name), "legacy=%v", namer.Legacy())
			}
		})
	}
}

func TestNamerFixedPoints(t *testing.T) {
	t.Parallel()

	// Canonical names come back unchanged, which is what makes
	// withFinalTypeName unnecessary on the latest rules.
	for _, name := range []string{"ModuleAOverlay", "ModuleBFileOverlay", "LLMMessage", "JSONValue", "MyModHTTPClient", "LLMTokenUsage"} {
		require.Equal(t, name, LatestNamer.ObjectName(name))
	}
	for _, name := range []string{"loadFooFromID", "withExec", "prerequisiteSHAs", "e2eTest"} {
		require.Equal(t, name, LatestNamer.FieldName(name))
	}
}

func TestNamerCLINamePatterns(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		pattern, legacy, latest string
	}{
		{"foo*", "foo*", "foo*"},
		{"*:e2eTest", "*:e-2-e-test", "*:e2e-test"},
		{"{fooBar,bazQux}", "{foo-bar,baz-qux}", "{foo-bar,baz-qux}"},
		{"**", "**", "**"},
	} {
		require.Equal(t, tc.legacy, LegacyNamer.CLIName(tc.pattern), tc.pattern)
		require.Equal(t, tc.latest, LatestNamer.CLIName(tc.pattern), tc.pattern)
	}
}

func TestLoadFromIDFunctionName(t *testing.T) {
	t.Parallel()

	for _, namer := range []Namer{LegacyNamer, LatestNamer} {
		require.Equal(t, "loadFooFromID", namer.NewFunction("loadFooFromID", dagql.ObjectResult[*TypeDef]{}).Name)
	}
}

// TestTypeDefWithNameVerbatim guards the invariant that the internal __withName
// rename stores an already-final GraphQL name as-is. NamespaceObject produces
// the final namespaced name; re-normalizing it here (the legacy strcase rules
// are not idempotent) corrupted already-cased multi-word names, e.g. turning
// "ModuleAOverlay" into "ModuleAoverlay".
func TestTypeDefWithNameVerbatim(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"ModuleAOverlay", "ModuleBFileOverlay"} {
		require.Equal(t, name, (&ObjectTypeDef{}).WithName(name).Name)
		require.Equal(t, name, (&InterfaceTypeDef{}).WithName(name).Name)
		require.Equal(t, name, (&EnumTypeDef{}).WithName(name).Name)
		require.Equal(t, name, (&EnumMemberTypeDef{}).WithName(name).Name)
	}
}

func TestEnumMemberNameFormatting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		legacy string
		latest string
	}{
		{name: "ACTIVE", legacy: "ACTIVE", latest: "ACTIVE"},
		{name: "P256", legacy: "P256", latest: "P256"},
		{name: "P_256", legacy: "P_256", latest: "P_256"},
		{name: "X25519", legacy: "X25519", latest: "X25519"},
		{name: "ED25519", legacy: "ED25519", latest: "ED25519"},
		{name: "fooBar", legacy: "FOO_BAR", latest: "FOO_BAR"},
		{name: "foo-bar", legacy: "FOO_BAR", latest: "FOO_BAR"},
		{name: "p256", legacy: "P_256", latest: "P256"},
		{name: "httpClient", legacy: "HTTP_CLIENT", latest: "HTTP_CLIENT"},
		{name: "userIDs", legacy: "USER_I_DS", latest: "USER_IDS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			legacy := LegacyNamer.NewEnumMemberTypeDef(tt.name, "", "", nil, dagql.ObjectResult[*SourceMap]{})
			require.Equal(t, tt.legacy, legacy.Name)
			require.Equal(t, tt.name, legacy.OriginalName)
			latest := LatestNamer.NewEnumMemberTypeDef(tt.name, "", "", nil, dagql.ObjectResult[*SourceMap]{})
			require.Equal(t, tt.latest, latest.Name)
			require.Equal(t, tt.name, latest.OriginalName)
		})
	}
}
