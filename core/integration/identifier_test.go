package core

// These tests cover the identifier casing API, which exposes engine/naming:
// parsing a name into words, formatting it in another casing, and reading the
// naming dictionary. The algorithm itself is covered by engine/naming's tests.

import (
	"context"
	"strings"
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/naming"
	"github.com/dagger/dagger/internal/testutil"
)

type IdentifierSuite struct{}

func TestIdentifier(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(IdentifierSuite{})
}

func (IdentifierSuite) TestWords(ctx context.Context, t *testctx.T) {
	n := core.NewQuery(connect(ctx, t)).Naming()

	id := n.Identifier("prerequisiteSHAs")
	name, err := id.Name(ctx)
	require.NoError(t, err)
	require.Equal(t, "prerequisiteSHAs", name)

	words, err := id.Words(ctx)
	require.NoError(t, err)
	require.Len(t, words, 2)

	text, err := words[0].Text(ctx)
	require.NoError(t, err)
	require.Equal(t, "prerequisite", text)
	kind, err := words[0].Kind(ctx)
	require.NoError(t, err)
	require.Equal(t, core.IdentifierWordKindWord, kind)
	term, err := words[0].Term(ctx)
	require.NoError(t, err)
	require.Nil(t, term)

	text, err = words[1].Text(ctx)
	require.NoError(t, err)
	require.Equal(t, "SHA", text)
	suffix, err := words[1].Suffix(ctx)
	require.NoError(t, err)
	require.Equal(t, "s", suffix)
	kind, err = words[1].Kind(ctx)
	require.NoError(t, err)
	require.Equal(t, core.IdentifierWordKindAcronym, kind)
	term, err = words[1].Term(ctx)
	require.NoError(t, err)
	require.NotNil(t, term)
	spelling, err := term.Spelling(ctx)
	require.NoError(t, err)
	require.Equal(t, "SHA", spelling)

	words, err = n.Identifier("IPv6Address").Words(ctx)
	require.NoError(t, err)
	require.Len(t, words, 2)
	kind, err = words[0].Kind(ctx)
	require.NoError(t, err)
	require.Equal(t, core.IdentifierWordKindTerm, kind)
	term, err = words[0].Term(ctx)
	require.NoError(t, err)
	require.NotNil(t, term)
	capitalized, err := term.Capitalized(ctx)
	require.NoError(t, err)
	require.Equal(t, "Ipv6", capitalized)
}

func (IdentifierSuite) TestFormat(ctx context.Context, t *testctx.T) {
	n := core.NewQuery(connect(ctx, t)).Naming()

	id := n.Identifier("http_api_client")
	for _, tc := range []struct {
		casing   core.Casing
		acronyms core.AcronymStyle
		want     string
	}{
		{core.CasingPascal, "", "HTTPAPIClient"},
		{core.CasingPascal, core.AcronymStyleCapitalized, "HttpApiClient"},
		{core.CasingCamel, core.AcronymStyleUppercase, "httpAPIClient"},
		{core.CasingSnake, "", "http_api_client"},
		{core.CasingScreamingSnake, "", "HTTP_API_CLIENT"},
		{core.CasingKebab, "", "http-api-client"},
		{core.CasingFlat, "", "httpapiclient"},
	} {
		got, err := id.Format(ctx, tc.casing, core.IdentifierFormatOpts{Acronyms: tc.acronyms})
		require.NoError(t, err)
		require.Equal(t, tc.want, got, "%s/%s", tc.casing, tc.acronyms)
	}
}

func (IdentifierSuite) TestFormatIdentifiers(ctx context.Context, t *testctx.T) {
	n := core.NewQuery(connect(ctx, t)).Naming()

	names := []string{"HTTPClient", "userIds", "GitHubRepo", "E2ETest", "md5sum"}
	got, err := n.FormatIdentifiers(ctx, names, core.CasingPascal)
	require.NoError(t, err)
	require.Equal(t, []string{"HTTPClient", "UserIDs", "GitHubRepo", "E2ETest", "MD5Sum"}, got)

	got, err = n.FormatIdentifiers(ctx, names, core.CasingPascal, core.NamingFormatIdentifiersOpts{
		Acronyms: core.AcronymStyleCapitalized,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"HttpClient", "UserIds", "GitHubRepo", "E2eTest", "Md5Sum"}, got)

	got, err = n.FormatIdentifiers(ctx, names, core.CasingSnake)
	require.NoError(t, err)
	require.Equal(t, []string{"http_client", "user_ids", "github_repo", "e2e_test", "md5_sum"}, got)
}

func (IdentifierSuite) TestErrors(ctx context.Context, t *testctx.T) {
	n := core.NewQuery(connect(ctx, t)).Naming()

	_, err := n.Identifier("Café").Name(ctx)
	require.ErrorContains(t, err, "identifier must be ASCII")

	_, err = n.Identifier("__").Name(ctx)
	require.ErrorContains(t, err, "identifier must contain a letter or digit")

	_, err = n.FormatIdentifiers(ctx, []string{"ok", "Café"}, core.CasingSnake)
	require.ErrorContains(t, err, "identifier must be ASCII")

	tooLong := strings.Repeat("a", naming.MaxLength+1)
	_, err = n.Identifier(tooLong).Name(ctx)
	require.ErrorContains(t, err, "identifier must be at most 256 bytes")

	_, err = n.FormatIdentifiers(ctx, []string{"ok", tooLong}, core.CasingSnake)
	require.ErrorContains(t, err, "identifier must be at most 256 bytes")
}

func (IdentifierSuite) TestDictionary(ctx context.Context, t *testctx.T) {
	n := core.NewQuery(connect(ctx, t)).Naming()

	terms, err := n.Dictionary(ctx)
	require.NoError(t, err)
	capitalized := map[string]string{}
	for _, term := range terms {
		spelling, err := term.Spelling(ctx)
		require.NoError(t, err)
		capitalized[spelling], err = term.Capitalized(ctx)
		require.NoError(t, err)
	}
	require.Equal(t, "Http", capitalized["HTTP"])
	require.Equal(t, "Ipv6", capitalized["IPv6"])
	require.Equal(t, "GitHub", capitalized["GitHub"])
	require.Equal(t, "Grpc", capitalized["gRPC"])
	require.Equal(t, "3D", capitalized["3D"])
}

// Query.naming answers for the version it's given, normalized as a module's
// declared version is; without one, for the caller's.
func (IdentifierSuite) TestNamingVersion(ctx context.Context, t *testctx.T) {
	q := core.NewQuery(connect(ctx, t))

	version, err := q.Naming().Version(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, version)
	// The version a Naming reports answers for itself.
	again, err := q.Naming(core.NamingOpts{Version: version}).Version(ctx)
	require.NoError(t, err)
	require.Equal(t, version, again)

	for _, v := range []string{"v0.21.0", "v1.0.0-beta.15", "v1.0.0-beta.20", engine.IdentifierCasingVersion, "v1.0.0", "v1.999.0"} {
		n := q.Naming(core.NamingOpts{Version: v})
		version, err := n.Version(ctx)
		require.NoError(t, err)
		require.Equal(t, engine.ModuleAPIViewVersion(v), version, v)
		// Primitive formatting always uses the dictionary, not legacy
		// strcase, even when module declarations use the legacy rules.
		formatted, err := n.FormatIdentifiers(ctx, []string{"withGPU", "callID"}, core.CasingCamel, core.NamingFormatIdentifiersOpts{Acronyms: core.AcronymStyleCapitalized})
		require.NoError(t, err)
		require.Equal(t, []string{"withGpu", "callId"}, formatted, v)
	}

	// A version past the engine's is capped at the engine's own.
	capped, err := q.Naming(core.NamingOpts{Version: "v1.999.0"}).Version(ctx)
	require.NoError(t, err)
	require.NotEqual(t, "v1.999.0", capped)

	// The legacy rules apply before the cut, the engine's from it on.
	for v, want := range map[string]string{
		"v1.0.0-beta.20":               "MyModHttpClient",
		engine.IdentifierCasingVersion: "MyModHTTPClient",
	} {
		got, err := q.Naming(core.NamingOpts{Version: v}).Module("my-mod").TypeNames(ctx, []string{"HttpClient"})
		require.NoError(t, err)
		require.Equal(t, []string{want}, got, v)
	}
}

// Explicitly empty originalName is not omission: it preserves an empty
// intrinsic prefix. mainObject still falls back to the final module name.
func (IdentifierSuite) TestModuleIdentity(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	type moduleNaming struct {
		ID                          core.ID
		Version, Name, OriginalName string
		TypeNames                   []string
		MainObject                  string
	}
	for _, version := range []string{"v1.0.0-beta.20", engine.IdentifierCasingVersion} {
		var res struct {
			Naming struct{ Omitted, Empty moduleNaming }
		}
		require.NoError(t, c.Do(ctx, &dagger.Request{
			Query: `query($version: String!) {
  naming(version: $version) {
    omitted: module(name: "my-mod") {
      id version name originalName typeNames(names: ["MyMod"]) mainObject(candidates: ["MyMod"])
    }
    empty: module(name: "my-mod", originalName: "") {
      id version name originalName typeNames(names: ["MyMod"]) mainObject(candidates: ["MyMod"])
    }
  }
}`,
			Variables: map[string]any{"version": version},
		}, &dagger.Response{Data: &res}))
		require.Equal(t, "my-mod", res.Naming.Omitted.OriginalName)
		require.Empty(t, res.Naming.Empty.OriginalName)
		require.Equal(t, []string{"MyMod"}, res.Naming.Omitted.TypeNames)
		require.Equal(t, []string{"MyModMyMod"}, res.Naming.Empty.TypeNames)
		for _, original := range []moduleNaming{res.Naming.Omitted, res.Naming.Empty} {
			require.Equal(t, engine.ModuleAPIViewVersion(version), original.Version)
			require.Equal(t, "my-mod", original.Name)
			require.Equal(t, "MyMod", original.MainObject)
			var loaded struct{ Node moduleNaming }
			require.NoError(t, c.Do(ctx, &dagger.Request{
				Query: `query($id: ID!) { node(id: $id) { ... on ModuleNaming {
  id version name originalName typeNames(names: ["MyMod"]) mainObject(candidates: ["MyMod"])
} } }`,
				Variables: map[string]any{"id": original.ID},
			}, &dagger.Response{Data: &loaded}))
			require.Equal(t, original, loaded.Node)
		}
	}
}

// The API is served on every v1 view, including the pre-cut view whose
// modules keep the legacy strcase names (before
// engine.IdentifierCasingVersion): SDK codegen picks its naming rules by the
// engine version a module declares, not by whether the schema has
// Query.naming. Without a version, it answers for the caller's.
func (IdentifierSuite) TestViews(ctx context.Context, t *testctx.T) {
	cut := engine.IdentifierCasingVersion
	preCut := engine.APIViewVersion("v1.0.0-0")
	for _, tc := range []struct {
		name    string
		opts    []dagger.ClientOpt
		want    bool
		version string
	}{
		{name: "current", want: true},
		{name: cut, opts: []dagger.ClientOpt{dagger.WithVersionOverride(cut)}, want: true, version: engine.ModuleAPIViewVersion(cut)},
		{name: preCut, opts: []dagger.ClientOpt{dagger.WithVersionOverride(preCut)}, want: true, version: engine.ModuleAPIViewVersion(preCut)},
		{name: "v1.0.0-beta.20", opts: []dagger.ClientOpt{dagger.WithVersionOverride("v1.0.0-beta.20")}, want: true, version: engine.ModuleAPIViewVersion("v1.0.0-beta.20")},
		{name: "v1.0.0-0", opts: []dagger.ClientOpt{dagger.WithVersionOverride("v1.0.0-0")}, want: true, version: engine.ModuleAPIViewVersion("v1.0.0-0")},
		{name: "v0.21.0", opts: []dagger.ClientOpt{dagger.WithVersionOverride("v0.21.0")}},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			res, err := testutil.Query[struct {
				Naming struct {
					Version           string
					FormatIdentifiers []string
				}
			}](t, `{ naming { version formatIdentifiers(names: ["prerequisiteSHAs"], casing: SNAKE) } }`, nil, tc.opts...)
			if !tc.want {
				require.ErrorContains(t, err, "naming")
				return
			}
			require.NoError(t, err)
			if tc.version != "" {
				require.Equal(t, tc.version, res.Naming.Version)
			} else {
				require.NotEmpty(t, res.Naming.Version)
			}
			require.Equal(t, []string{"prerequisite_shas"}, res.Naming.FormatIdentifiers)
		})
	}
}

// formatIdentifiers formats every core schema name the way engine/naming does.
func (IdentifierSuite) TestFormatSchemaNames(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
		Schema struct {
			Types []struct {
				Name   string
				Fields []struct {
					Name string
				}
			}
		} `json:"__schema"`
	}](t, `{ __schema { types { name fields { name } } } }`, nil)
	require.NoError(t, err)
	var names []string
	for _, typ := range res.Schema.Types {
		if strings.HasPrefix(typ.Name, "__") {
			continue
		}
		names = append(names, typ.Name)
		for _, field := range typ.Fields {
			if !strings.HasPrefix(field.Name, "__") {
				names = append(names, field.Name)
			}
		}
	}

	n := core.NewQuery(connect(ctx, t)).Naming()
	formatted, err := n.FormatIdentifiers(ctx, names, core.CasingSnake)
	require.NoError(t, err)
	require.Len(t, formatted, len(names))
	for i, name := range names {
		id, err := naming.Parse(name)
		require.NoError(t, err)
		require.Equal(t, id.Format(naming.Snake, naming.Uppercase), formatted[i], name)
	}
}
