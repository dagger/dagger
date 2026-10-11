package core

import (
	"testing"

	"github.com/dagger/dagger/core/modules"
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
}

// A module keeps the legacy naming rules unless its full engine version,
// prerelease included, is at least IdentifierNamingVersion: released v1.0.0
// prereleases were built against strcase.
func TestNamerForEngineVersion(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		engineVersion string
		legacy        bool
	}{
		{"v0.19.4", true},
		{"v0.21.9", true},
		{"v1.0.0-0", true},
		{"v1.0.0-beta.15", true},
		{"v1.0.0-beta.16", true},
		{"v1.0.0-beta.17", false},
		{"v1.0.0-beta.17-dev-123", false},
		{"v1.0.0-dev-123", false},
		{"v1.0.0-rc.1", false},
		{"v1.0.0", false},
		{"v1.2.3", false},
		{"not-a-version", true},
	} {
		require.Equal(t, tc.legacy, NamerForEngineVersion(tc.engineVersion).Legacy(), "engine version %q", tc.engineVersion)
		src := &ModuleSource{EngineVersion: tc.engineVersion}
		require.Equal(t, tc.legacy, src.LegacyNaming(), "engine version %q", tc.engineVersion)
		require.Equal(t, tc.legacy, src.Namer().Legacy(), "engine version %q", tc.engineVersion)
	}
}

// A view marked for legacy naming is still the same API view (build metadata
// is ignored by semver precedence), but names by the legacy rules.
func TestLegacyNamingView(t *testing.T) {
	t.Parallel()

	for _, view := range []call.View{"v1.0.0", "v1.0.0-beta.17", "v1.2.3+abc"} {
		marked := LegacyNamingView(view)
		require.True(t, IsLegacyNamingView(marked), "view %q", view)
		require.Equal(t, marked, LegacyNamingView(marked), "view %q", view)
		require.True(t, NamerForView(marked).Legacy(), "view %q", view)
		require.False(t, NamerForView(view).Legacy(), "view %q", view)

		for _, filter := range []dagql.ViewFilter{
			AfterVersion("v1.0.0-0"),
			AfterVersion("v1.0.0-beta.15"),
			BeforeVersion("v1.0.0-0"),
			BeforeVersion("v2.0.0"),
			AllVersion,
		} {
			require.Equal(t, filter.Contains(view), filter.Contains(marked), "view %q, filter %v", view, filter)
		}
	}
	require.Equal(t, call.View("v1.0.0+legacy-naming"), LegacyNamingView("v1.0.0"))

	// The latest view and a non-semver view can't be marked.
	require.Equal(t, call.View(""), LegacyNamingView(""))
	require.False(t, IsLegacyNamingView(""))
	require.False(t, IsLegacyNamingView("v1.0.0+other"))

	// __schemaVersion reports the version without the mark.
	require.Equal(t, call.View("v1.0.0"), UnmarkedView(LegacyNamingView("v1.0.0")))
	require.Equal(t, call.View("v1.0.0"), UnmarkedView("v1.0.0"))
	require.Equal(t, call.View(""), UnmarkedView(""))
}

// A module that keeps the legacy naming rules at a view that has the new
// ones is served at the marked view; older views need no mark.
func TestModuleNamingView(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		engineVersion string
		entrypoint    bool
		view          call.View
	}{
		{"v1.0.0-beta.17", false, "v1.0.0"},
		{"v1.0.0", false, "v1.0.0"},
		{"v1.0.0-beta.16", false, "v1.0.0+legacy-naming"},
		{"v1.0.0-0", false, "v1.0.0+legacy-naming"},
		{"v1.0.0-beta.17", true, "v1.0.0+legacy-naming"},
		{"v0.21.9", false, "v0.21.9"},
		{"v0.21.9", true, "v0.21.9"},
	} {
		require.Equal(t, tc.view, ModuleNamingView(tc.engineVersion, tc.entrypoint),
			"engine version %q, entrypoint %v", tc.engineVersion, tc.entrypoint)
		require.True(t, NamerForView(tc.view).Legacy() == (tc.entrypoint || legacyNamingEngineVersion(tc.engineVersion)),
			"engine version %q, entrypoint %v", tc.engineVersion, tc.entrypoint)
	}

	for _, tc := range []struct{ typeName, field string }{
		{"Query", "function"},
		{"Function", "withArg"},
		{"TypeDef", "withObject"},
		{"TypeDef", "withCollectionKeys"},
	} {
		require.True(t, IsNamingField(tc.typeName, tc.field), "%s.%s", tc.typeName, tc.field)
	}
	for _, tc := range []struct{ typeName, field string }{
		{"Container", "withExec"},
		{"Module", "withObject"},
		{"TypeDef", "withFunction"},
	} {
		require.False(t, IsNamingField(tc.typeName, tc.field), "%s.%s", tc.typeName, tc.field)
	}
}

// A module entrypoint (manifest v2) is served at the engine's version, but
// keeps the legacy naming rules: its names must not change with the engine.
func TestModuleSourceNaming(t *testing.T) {
	t.Parallel()

	entrypoint := &ModuleSource{
		EngineVersion: "v1.0.0",
		Entrypoint:    &modules.ModuleEntrypointConfig{Kind: "dang", Source: "./sdk/entrypoint"},
	}
	require.True(t, entrypoint.LegacyNaming())
	require.True(t, entrypoint.Namer().Legacy())
	require.Equal(t, call.View("v1.0.0+legacy-naming"), entrypoint.NamingView())
	require.Equal(t, "getUrl", entrypoint.Namer().FieldName("getUrl"))
	require.Equal(t, "MyModHttpClient", entrypoint.Namer().NamespaceObject("HttpClient", "my-mod", "my-mod"))

	v1 := &ModuleSource{EngineVersion: "v1.0.0"}
	require.False(t, v1.LegacyNaming())
	require.False(t, v1.Namer().Legacy())
	require.Equal(t, call.View("v1.0.0"), v1.NamingView())
	require.Equal(t, "getURL", v1.Namer().FieldName("getUrl"))

	beta := &ModuleSource{EngineVersion: "v1.0.0-beta.16"}
	require.True(t, beta.LegacyNaming())
	require.Equal(t, call.View("v1.0.0+legacy-naming"), beta.NamingView())
	require.Equal(t, "getUrl", beta.Namer().FieldName("getUrl"))

	legacy := &ModuleSource{EngineVersion: "v0.21.9"}
	require.True(t, legacy.LegacyNaming())
	require.True(t, legacy.Namer().Legacy())
	require.Equal(t, call.View("v0.21.9"), legacy.NamingView())

	var none *ModuleSource
	require.False(t, none.LegacyNaming())

	require.False(t, (&Module{}).Namer().Legacy(), "a module without a source is named by the latest rules")
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
