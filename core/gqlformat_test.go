package core

import (
	"testing"

	"github.com/iancoleman/strcase"

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

// mainTypeSpellings are module names with a main type an SDK or template may
// spell differently around digits and acronyms, which engine/naming cuts into
// words differently than the module's name, so only a flat match finds them.
var mainTypeSpellings = []struct{ module, mainType string }{
	{"my2go", "My2Go"},
	{"sha256sum", "Sha256Sum"},
	{"go-1password", "Go1Password"},
	{"e2eapi", "E2Eapi"},
}

func TestSameDeclaredName(t *testing.T) {
	t.Parallel()

	// K8s is a dictionary term, so K8STools is k8s-tools by its normalized
	// name, with no flat match needed.
	require.Equal(t, LatestNamer.ObjectName("k8s-tools"), LatestNamer.ObjectName("K8STools"))
	require.Equal(t, "K8sTools", LatestNamer.NamespaceObject("K8STools", "k8s-tools", "k8s-tools"))
	require.Equal(t, "K8sToolsClient", LatestNamer.NamespaceObject("K8SToolsClient", "k8s-tools", "k8s-tools"))

	for _, tc := range mainTypeSpellings {
		t.Run(tc.module, func(t *testing.T) {
			require.NotEqual(t, LatestNamer.ObjectName(tc.module), LatestNamer.ObjectName(tc.mainType), "not a flat-only match")
			require.True(t, LatestNamer.SameDeclaredName(tc.mainType, tc.module))
			require.True(t, LatestNamer.SameDeclaredName(tc.module, tc.mainType))
			// The legacy rules compare exactly as they always did.
			require.Equal(t, strcase.ToCamel(tc.mainType) == strcase.ToCamel(tc.module), LegacyNamer.SameDeclaredName(tc.mainType, tc.module))
			// The main type is the module: it takes the module's name, and
			// a type prefixed with it is namespaced under the module once.
			require.Equal(t, LatestNamer.ObjectName(tc.module), LatestNamer.NamespaceObject(tc.mainType, tc.module, tc.module))
			require.Equal(t, LatestNamer.ObjectName(tc.module)+"Client", LatestNamer.NamespaceObject(tc.mainType+"Client", tc.module, tc.module))
			require.Equal(t, LatestNamer.ObjectName("other"), LatestNamer.NamespaceObject(tc.mainType, "other", tc.module))
		})
	}

	// Flat matching ends on word boundaries, and only applies to the new
	// rules: the legacy ones stay as they were.
	require.False(t, LatestNamer.SameDeclaredName("Postman", "post"))
	require.Equal(t, "PostPostman", LatestNamer.NamespaceObject("Postman", "post", "post"))
	require.Equal(t, "PostManPostManager", LatestNamer.NamespaceObject("PostManager", "post-man", "post-man"))
	require.False(t, LegacyNamer.SameDeclaredName("PostMan", "postman"))
	require.True(t, LatestNamer.SameDeclaredName("PostMan", "postman"))
	require.False(t, LatestNamer.SameDeclaredName("", "--"))
}

// A module finds its main object by its name in any casing: by its rules'
// normalization first, else the only object that's the same once flattened.
// Several flat matches are ambiguous and find nothing.
func TestModuleMainObjectSpelling(t *testing.T) {
	t.Parallel()

	dag := newTypeDefTestDag(t)
	objects := func(names ...string) dagql.ObjectResultArray[*TypeDef] {
		var defs dagql.ObjectResultArray[*TypeDef]
		for _, name := range names {
			obj := newTypeDefDetachedResult(t, dag, "obj-"+name, LatestNamer.NewObjectTypeDef(name, "", nil))
			defs = append(defs, newTypeDefDetachedResult(t, dag, "typedef-"+name, (&TypeDef{}).WithObjectTypeDef(obj)))
		}
		return defs
	}

	for _, tc := range mainTypeSpellings {
		t.Run(tc.module, func(t *testing.T) {
			mod := &Module{NameField: tc.module, OriginalName: tc.module, ObjectDefs: objects("Helper", tc.mainType)}
			main, ok := mod.MainObject()
			require.True(t, ok)
			require.Equal(t, tc.mainType, main.OriginalName)

			legacy := &Module{NameField: tc.module, OriginalName: tc.module, ObjectDefs: objects("Helper", tc.mainType),
				SourceOverrides: &SourceOverrides{EngineVersion: "v0.21.9"}}
			_, ok = legacy.MainObject()
			// The legacy rules find it exactly when strcase normalizes both
			// alike, as they always did.
			require.Equal(t, strcase.ToCamel(tc.mainType) == strcase.ToCamel(tc.module), ok)
		})
	}

	t.Run("k8s term", func(t *testing.T) {
		mod := &Module{NameField: "k8s-tools", OriginalName: "k8s-tools", ObjectDefs: objects("K8SToolsClient", "K8STools")}
		main, ok := mod.MainObject()
		require.True(t, ok)
		require.Equal(t, "K8STools", main.OriginalName)
	})

	t.Run("normalized match wins", func(t *testing.T) {
		mod := &Module{NameField: "postman", OriginalName: "postman", ObjectDefs: objects("PostMan", "Postman")}
		main, ok := mod.MainObject()
		require.True(t, ok)
		require.Equal(t, "Postman", main.OriginalName)
	})

	t.Run("ambiguous flat matches", func(t *testing.T) {
		// Neither normalizes to e2eapi's E2eapi; both flatten to e2eapi.
		require.NotEqual(t, LatestNamer.ObjectName("e2eapi"), LatestNamer.ObjectName("E2Eapi"))
		require.NotEqual(t, LatestNamer.ObjectName("e2eapi"), LatestNamer.ObjectName("E2EApi"))
		mod := &Module{NameField: "e2eapi", OriginalName: "e2eapi", ObjectDefs: objects("E2Eapi", "E2EApi")}
		_, ok := mod.MainObject()
		require.False(t, ok)
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

// Modules are named by engine/naming on exactly IdentifierNamingView. (The
// identifier API is served on every v1 view, regardless.)
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
// v1.0.0 share the release's view and get the new ones, as does v1.0.0
// itself.
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
		{"v1.0.0-beta.19", true},
		{"v1.0.0-beta.20", true},
		{"v1.0.0-beta.21", false},
		{"v1.0.0-beta.22", false},
		{"v1.0.0-beta.17-dev-123", false},
		{"v1.0.0-dev-123", false},
		{"v1.0.0-rc.1", false},
		{"v1.0.0", false},
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
	require.Equal(t, "getUrl", entrypoint.Namer().FieldName("getURL"))
	require.Equal(t, "httpClient", entrypoint.Namer().FieldName("HTTPClient"))
	require.Equal(t, "MyModHTTPClient", entrypoint.Namer().NamespaceObject("HttpClient", "my-mod", "my-mod"))

	legacyEntrypoint := &ModuleSource{
		EngineVersion: "v1.0.0-beta.19",
		Entrypoint:    &modules.ModuleEntrypointConfig{Kind: "dang", Source: "./sdk/entrypoint"},
	}
	require.True(t, legacyEntrypoint.Namer().Legacy())

	beta := &ModuleSource{EngineVersion: "v1.0.0-beta.21"}
	require.False(t, beta.Namer().Legacy())
	require.Equal(t, call.View("v1.0.0-beta.21"), beta.NamingView())
	require.Equal(t, "httpClient", beta.Namer().FieldName("HTTPClient"))

	oldBeta := &ModuleSource{EngineVersion: "v1.0.0-beta.19"}
	require.True(t, oldBeta.Namer().Legacy())
	require.Equal(t, call.View("v1.0.0-beta.20"), oldBeta.NamingView())
	require.Equal(t, "httpclient", oldBeta.Namer().FieldName("HTTPClient"))

	legacy := &ModuleSource{EngineVersion: "v0.21.9"}
	require.True(t, legacy.Namer().Legacy())
	require.Equal(t, call.View("v0.21.9"), legacy.NamingView())
	require.Equal(t, "httpclient", legacy.Namer().FieldName("HTTPClient"))

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
			field:  [2]string{"userIds", "userIds"},
			enum:   [2]string{"USER_IDS", "USER_IDS"},
			cli:    [2]string{"user-ids", "user-ids"},
		},
		// Fields write acronyms like words, types in capitals.
		{
			name:   "asJSON",
			object: [2]string{"AsJson", "AsJSON"},
			field:  [2]string{"asJson", "asJson"},
			enum:   [2]string{"AS_JSON", "AS_JSON"},
			cli:    [2]string{"as-json", "as-json"},
		},
		{
			name:   "baseURL",
			object: [2]string{"BaseUrl", "BaseURL"},
			field:  [2]string{"baseUrl", "baseUrl"},
			enum:   [2]string{"BASE_URL", "BASE_URL"},
			cli:    [2]string{"base-url", "base-url"},
		},
		{
			name:   "myK8sCluster",
			object: [2]string{"MyK8SCluster", "MyK8sCluster"},
			field:  [2]string{"myK8SCluster", "myK8sCluster"},
			enum:   [2]string{"MY_K_8_S_CLUSTER", "MY_K8S_CLUSTER"},
			cli:    [2]string{"my-k-8-s-cluster", "my-k8s-cluster"},
		},
		{
			name:   "fromGitHubIPv6",
			object: [2]string{"FromGitHubIpv6", "FromGitHubIPv6"},
			field:  [2]string{"fromGitHubIpv6", "fromGitHubIpv6"},
			enum:   [2]string{"FROM_GIT_HUB_I_PV_6", "FROM_GITHUB_IPV6"},
			cli:    [2]string{"from-git-hub-i-pv-6", "from-github-ipv6"},
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
	for _, name := range []string{"withExec", "prerequisiteShas", "e2eTest", "asJson", "httpClient", "fromGitHubIpv6", "k8sTools"} {
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
