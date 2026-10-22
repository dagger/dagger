package core

import (
	"testing"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
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
		{"v1.0.0-beta.21", false},
		{"v1.0.0-beta.22", false},
		{"v1.0.0-rc.1", false},
		{"v1.0.0-beta.20", true},
		{"v0.21.5", true},
		{"v0.9.9", true},
	} {
		require.Equal(t, tc.legacy, NamerForView(tc.view).Legacy(), "view %q", tc.view)
	}
}

// The identifier API is installed on exactly the views that name modules by
// engine/naming: SDK codegen picks its naming rules by whether the schema
// has Query.formatIdentifiers.
func TestIdentifierNamingView(t *testing.T) {
	t.Parallel()

	require.Equal(t, engine.IdentifierCasingVersion, IdentifierNamingVersion)
	for _, view := range []call.View{"", "v0.21.5", "v1.0.0-beta.20", "v1.0.0-beta.21", "v1.0.0", "v1.2.3"} {
		require.Equal(t, !NamerForView(view).Legacy(), IdentifierNamingView.Contains(view), "view %q", view)
	}
}

// A module gets the new naming rules from IdentifierNamingVersion
// (v1.0.0-beta.21) on. Every earlier v1.0.0 prerelease, v1.0.0-0 included,
// shares the v1.0.0-beta.20 view and keeps the legacy rules; dev builds of
// v1.0.0 share the release's view and get the new ones. A module declaring
// exactly v1.0.0 gets the pre-cut view, and so the legacy rules, while this
// engine is a v1.0.0 prerelease (engine.ModuleAPIViewVersion).
func TestNamerForEngineVersion(t *testing.T) {
	t.Parallel()

	releaseClamped := engine.ModuleAPIViewVersion("v1.0.0") != "v1.0.0"
	for _, tc := range []struct {
		engineVersion string
		legacy        bool
	}{
		{"v0.19.4", true},
		{"v0.21.9", true},
		{"v1.0.0-0", true},
		{"v1.0.0-beta.15", true},
		{"v1.0.0-beta.19", true},
		{"v1.0.0-beta.20", true},
		{"v1.0.0-beta.21", false},
		{"v1.0.0-beta.22", false},
		{"v1.0.0-beta.17-dev-123", false},
		{"v1.0.0-dev-123", false},
		{"v1.0.0-rc.1", false},
		{"v1.0.0", releaseClamped},
		{"v1.2.3", false},
		{engine.Version, false},
		{"not-a-version", true},
	} {
		require.Equal(t, tc.legacy, NamerForEngineVersion(tc.engineVersion).Legacy(), "engine version %q", tc.engineVersion)
		require.Equal(t, !tc.legacy, engine.APIVersionAtLeast(tc.engineVersion, IdentifierNamingVersion), "engine version %q", tc.engineVersion)
		src := &ModuleSource{EngineVersion: tc.engineVersion}
		require.Equal(t, tc.legacy, src.Namer().Legacy(), "engine version %q", tc.engineVersion)
	}
}

func TestIsNamingField(t *testing.T) {
	t.Parallel()

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

// A module is served at the view of its engine version, and named by its
// rules. A workspace main module (manifest v2) is no different: it's named
// by whatever engine version its ModuleSource has.
func TestModuleSourceNaming(t *testing.T) {
	t.Parallel()

	entrypoint := &ModuleSource{
		EngineVersion: "v1.0.0-beta.21",
		Entrypoint:    &modules.ModuleEntrypointConfig{Kind: "dang", Source: "./sdk/entrypoint"},
	}
	require.False(t, entrypoint.Namer().Legacy())
	require.Equal(t, call.View("v1.0.0-beta.21"), entrypoint.NamingView())
	require.Equal(t, "getURL", entrypoint.Namer().FieldName("getUrl"))
	require.Equal(t, "MyModHTTPClient", entrypoint.Namer().NamespaceObject("HttpClient", "my-mod", "my-mod"))

	legacyEntrypoint := &ModuleSource{
		EngineVersion: "v1.0.0-beta.19",
		Entrypoint:    &modules.ModuleEntrypointConfig{Kind: "dang", Source: "./sdk/entrypoint"},
	}
	require.True(t, legacyEntrypoint.Namer().Legacy())

	beta := &ModuleSource{EngineVersion: "v1.0.0-beta.21"}
	require.False(t, beta.Namer().Legacy())
	require.Equal(t, call.View("v1.0.0-beta.21"), beta.NamingView())
	require.Equal(t, "getURL", beta.Namer().FieldName("getUrl"))

	oldBeta := &ModuleSource{EngineVersion: "v1.0.0-beta.19"}
	require.True(t, oldBeta.Namer().Legacy())
	require.Equal(t, call.View("v1.0.0-beta.20"), oldBeta.NamingView())
	require.Equal(t, "getUrl", oldBeta.Namer().FieldName("getUrl"))

	legacy := &ModuleSource{EngineVersion: "v0.21.9"}
	require.True(t, legacy.Namer().Legacy())
	require.Equal(t, call.View("v0.21.9"), legacy.NamingView())
	require.Equal(t, "getUrl", legacy.Namer().FieldName("getUrl"))

	var none *ModuleSource
	require.Equal(t, call.View(""), none.NamingView())
	require.False(t, none.Namer().Legacy())

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
		{"test-*", "test-*", "test-*"},
		{"*-lint", "*-lint", "*-lint"},
		{"*_e2eTest", "*-e-2-e-test", "*-e2e-test"},
		{"go:*-*", "go:*-*", "go:*-*"},
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
		{name: "arm64", legacy: "ARM_64", latest: "ARM64"},
		{name: "IPv6", legacy: "I_PV_6", latest: "IPV6"},
		{name: "GitHub", legacy: "GIT_HUB", latest: "GITHUB"},
		{name: "E2ETest", legacy: "E_2_E_TEST", latest: "E2E_TEST"},
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
