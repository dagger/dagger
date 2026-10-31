package core

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

// moduleNamingFor is the ModuleNaming of a module source declaring
// engineVersion, named name (and originally so).
func moduleNamingFor(engineVersion, name, originalName string) *ModuleNaming {
	return (&ModuleSource{EngineVersion: engineVersion, ModuleName: name, ModuleOriginalName: originalName}).Naming()
}

// The two sides of the identifier casing cut: the last pre-cut beta, which
// SDK modules are pinned to, and the cut itself.
const legacyNamingVersion = "v1.0.0-beta.20"

func TestModuleNamingRules(t *testing.T) {
	t.Parallel()

	require.True(t, moduleNamingFor(legacyNamingVersion, "m", "m").Namer().Legacy())
	require.False(t, moduleNamingFor(cutVersion, "m", "m").Namer().Legacy())
	// A source without an entrypoint or anything else loaded still answers:
	// it needs only its names and engine version.
	require.Equal(t, (&ModuleSource{EngineVersion: cutVersion}).NamingView(), moduleNamingFor(cutVersion, "m", "m").View)
}

func TestModuleNamingTypeNames(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		module, original string
		names            []string
		legacy, latest   []string
	}{
		{
			module: "my-mod", original: "my-mod",
			names:  []string{"HttpClient", "HTTPClient", "MyMod", "MyModHTTPClient"},
			legacy: []string{"MyModHttpClient", "MyModHttpclient", "MyMod", "MyModHttpclient"},
			latest: []string{"MyModHTTPClient", "MyModHTTPClient", "MyMod", "MyModHTTPClient"},
		},
		{
			// The prefix check is on a word boundary.
			module: "post", original: "post",
			names:  []string{"Postman", "PostMan"},
			legacy: []string{"PostPostman", "PostMan"},
			latest: []string{"PostPostman", "PostMan"},
		},
		{
			// Words cut differently around digits and terms still match.
			module: "k8s-tools", original: "k8s-tools",
			names:  []string{"K8STools", "K8SToolsClient"},
			// strcase spells k8s-tools K8STools, and K8STools K8Stools: a
			// main object spelled as the module's schema name keeps it, but
			// a prefixed one isn't recognized as prefixed, as it never was.
			legacy: []string{"K8STools", "K8SToolsK8StoolsClient"},
			latest: []string{"K8sTools", "K8sToolsClient"},
		},
		{
			// A renamed module namespaces under its final name, matching
			// its original name.
			module: "other", original: "my-mod",
			names:  []string{"MyModHTTPClient", "MyMod", "Helper"},
			legacy: []string{"OtherHttpclient", "Other", "OtherHelper"},
			latest: []string{"OtherHTTPClient", "Other", "OtherHelper"},
		},
	} {
		t.Run(tc.module, func(t *testing.T) {
			latest, err := moduleNamingFor(cutVersion, tc.module, tc.original).TypeNames(tc.names)
			require.NoError(t, err)
			require.Equal(t, tc.latest, latest)

			legacy, err := moduleNamingFor(legacyNamingVersion, tc.module, tc.original).TypeNames(tc.names)
			require.NoError(t, err)
			require.Equal(t, tc.legacy, legacy)
		})
	}
}

func TestModuleNamingFieldNames(t *testing.T) {
	t.Parallel()

	names := []string{"getURL", "baseURL", "HTTPClient", "e2eTest", "loadFooFromID", "name"}

	latest := moduleNamingFor(cutVersion, "m", "m")
	fields, err := latest.FieldNames(names)
	require.NoError(t, err)
	require.Equal(t, []string{"getUrl", "baseUrl", "httpClient", "e2eTest", "loadFooFromId", "name"}, fields)

	legacy := moduleNamingFor(legacyNamingVersion, "m", "m")
	fields, err = legacy.FieldNames(names)
	require.NoError(t, err)
	require.Equal(t, []string{"getUrl", "baseUrl", "httpclient", "e2ETest", "loadFooFromId", "name"}, fields)

	// They're the names typedef registration gives functions, fields and
	// arguments.
	var noTypeDef dagql.ObjectResult[*TypeDef]
	for _, mn := range []*ModuleNaming{latest, legacy} {
		namer := mn.Namer()
		fields, err := mn.FieldNames(names)
		require.NoError(t, err)
		for i, name := range names {
			require.Equal(t, namer.NewFunction(name, noTypeDef).Name, fields[i], name)
			require.Equal(t, namer.NewFieldTypeDef(name, noTypeDef, "", nil).Name, fields[i], name)
			require.Equal(t, namer.NewFunctionArg(name, noTypeDef, "", nil, "", "", nil, nil).Name, fields[i], name)
		}
		members, err := mn.EnumMemberNames(names)
		require.NoError(t, err)
		for i, name := range names {
			require.Equal(t, namer.NewEnumMemberTypeDef(name, "", "", nil, dagql.ObjectResult[*SourceMap]{}).Name, members[i], name)
		}
	}
}

func TestModuleNamingEnumMemberNames(t *testing.T) {
	t.Parallel()

	names := []string{"Arm64", "GitHub", "e2eTest", "HTTP2", "P_256", "CALM"}

	latest, err := moduleNamingFor(cutVersion, "m", "m").EnumMemberNames(names)
	require.NoError(t, err)
	require.Equal(t, []string{"ARM64", "GITHUB", "E2E_TEST", "HTTP2", "P_256", "CALM"}, latest)

	legacy, err := moduleNamingFor(legacyNamingVersion, "m", "m").EnumMemberNames(names)
	require.NoError(t, err)
	require.Equal(t, []string{"ARM_64", "GIT_HUB", "E_2_E_TEST", "HTTP2", "P_256", "CALM"}, legacy)
}

func TestModuleNamingMainObject(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name             string
		module, original string
		candidates       []string
		latest, legacy   string // "" for null
	}{
		{name: "exact", module: "my-mod", original: "my-mod", candidates: []string{"Helper", "MyMod"}, latest: "MyMod", legacy: "MyMod"},
		{name: "first exact in input order", module: "my-mod", original: "my-mod", candidates: []string{"my_mod", "MyMod"}, latest: "my_mod", legacy: "my_mod"},
		{name: "exact wins over an earlier flat match", module: "postman", original: "postman", candidates: []string{"PostMan", "Postman"}, latest: "Postman", legacy: "Postman"},
		{name: "flat", module: "sha256sum", original: "sha256sum", candidates: []string{"Helper", "Sha256Sum"}, latest: "Sha256Sum", legacy: "Sha256Sum"},
		// strcase spells K8STools K8Stools, but k8s-tools K8STools: the
		// legacy rules find it spelled as the module's schema name.
		{name: "term", module: "k8s-tools", original: "k8s-tools", candidates: []string{"K8SToolsClient", "K8STools"}, latest: "K8STools", legacy: "K8STools"},
		// Neither is an exact match by the new rules, and both are flat
		// ones; strcase normalizes both like e2eapi, so the first wins.
		{name: "ambiguous flat", module: "e2eapi", original: "e2eapi", candidates: []string{"E2Eapi", "E2EApi"}, legacy: "E2Eapi"},
		{name: "none", module: "my-mod", original: "my-mod", candidates: []string{"Helper", "MyModClient"}},
		{name: "empty", module: "my-mod", original: "my-mod"},
		{name: "by original name", module: "other", original: "my-mod", candidates: []string{"Other", "MyMod"}, latest: "MyMod", legacy: "MyMod"},
		{name: "by final name without an original", module: "my-mod", candidates: []string{"MyMod"}, latest: "MyMod", legacy: "MyMod"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, side := range []struct {
				version, want string
			}{
				{cutVersion, tc.latest},
				{legacyNamingVersion, tc.legacy},
			} {
				got, ok, err := moduleNamingFor(side.version, tc.module, tc.original).MainObject(tc.candidates)
				require.NoError(t, err, side.version)
				require.Equal(t, side.want != "", ok, side.version)
				require.Equal(t, side.want, got, side.version)
			}
		})
	}
}

// A cached ModuleNaming decodes to the same rules and names.
func TestModuleNamingPersisted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, version := range []string{legacyNamingVersion, cutVersion} {
		mn := moduleNamingFor(version, "other", "my-mod")
		encoded, err := mn.EncodePersistedObject(ctx, nil)
		require.NoError(t, err)
		decoded, err := (*ModuleNaming)(nil).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(nil, 0, nil), encoded.JSON)
		require.NoError(t, err)
		require.Equal(t, mn, decoded)
		require.Equal(t, mn.Namer(), decoded.(*ModuleNaming).Namer())
	}
}

// The new rules fail on a name they can't parse, naming it, as
// formatIdentifiers does; the legacy rules name anything.
func TestModuleNamingUnparseable(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{"héllo", "--", "", strings.Repeat("a", 257)} {
		latest := moduleNamingFor(cutVersion, "m", "m")
		legacy := moduleNamingFor(legacyNamingVersion, "m", "m")
		names := []string{"fine", bad}
		for field, call := range map[string]func(*ModuleNaming) error{
			"typeNames":       func(mn *ModuleNaming) error { _, err := mn.TypeNames(names); return err },
			"fieldNames":      func(mn *ModuleNaming) error { _, err := mn.FieldNames(names); return err },
			"enumMemberNames": func(mn *ModuleNaming) error { _, err := mn.EnumMemberNames(names); return err },
			"mainObject":      func(mn *ModuleNaming) error { _, _, err := mn.MainObject(names); return err },
		} {
			err := call(latest)
			require.Error(t, err, "%s(%q)", field, bad)
			quoted := bad
			if len(quoted) > 32 {
				quoted = quoted[:32]
			}
			require.Contains(t, err.Error(), quoted, "%s(%q) names the offending name", field, bad)
			require.NoError(t, call(legacy), "%s(%q)", field, bad)
		}
	}
}
