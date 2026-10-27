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

	"github.com/dagger/dagger/engine/naming"
	"github.com/dagger/dagger/internal/testutil"
)

type IdentifierSuite struct{}

func TestIdentifier(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(IdentifierSuite{})
}

func (IdentifierSuite) TestWords(ctx context.Context, t *testctx.T) {
	c := core.NewQuery(connect(ctx, t))

	id := c.Identifier("prerequisiteSHAs")
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

	words, err = c.Identifier("IPv6Address").Words(ctx)
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
	c := core.NewQuery(connect(ctx, t))

	id := c.Identifier("http_api_client")
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
	c := core.NewQuery(connect(ctx, t))

	names := []string{"HTTPClient", "userIds", "GitHubRepo", "E2ETest", "md5sum"}
	got, err := c.FormatIdentifiers(ctx, names, core.CasingPascal)
	require.NoError(t, err)
	require.Equal(t, []string{"HTTPClient", "UserIDs", "GitHubRepo", "E2ETest", "MD5Sum"}, got)

	got, err = c.FormatIdentifiers(ctx, names, core.CasingPascal, core.FormatIdentifiersOpts{
		Acronyms: core.AcronymStyleCapitalized,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"HttpClient", "UserIds", "GitHubRepo", "E2eTest", "Md5Sum"}, got)

	got, err = c.FormatIdentifiers(ctx, names, core.CasingSnake)
	require.NoError(t, err)
	require.Equal(t, []string{"http_client", "user_ids", "github_repo", "e2e_test", "md5_sum"}, got)
}

func (IdentifierSuite) TestErrors(ctx context.Context, t *testctx.T) {
	c := core.NewQuery(connect(ctx, t))

	_, err := c.Identifier("Café").Name(ctx)
	require.ErrorContains(t, err, "identifier must be ASCII")

	_, err = c.Identifier("__").Name(ctx)
	require.ErrorContains(t, err, "identifier must contain a letter or digit")

	_, err = c.FormatIdentifiers(ctx, []string{"ok", "Café"}, core.CasingSnake)
	require.ErrorContains(t, err, "identifier must be ASCII")

	tooLong := strings.Repeat("a", naming.MaxLength+1)
	_, err = c.Identifier(tooLong).Name(ctx)
	require.ErrorContains(t, err, "identifier must be at most 256 bytes")

	_, err = c.FormatIdentifiers(ctx, []string{"ok", tooLong}, core.CasingSnake)
	require.ErrorContains(t, err, "identifier must be at most 256 bytes")
}

func (IdentifierSuite) TestNamingDictionary(ctx context.Context, t *testctx.T) {
	c := core.NewQuery(connect(ctx, t))

	terms, err := c.NamingDictionary(ctx)
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

// The API is served on every v1 view, including the views whose modules keep
// the legacy strcase names (before core.IdentifierNamingVersion,
// v1.0.0-beta.21): SDK codegen picks its naming rules by the engine version a
// module declares, not by whether the schema has Query.formatIdentifiers.
func (IdentifierSuite) TestViews(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		name string
		opts []dagger.ClientOpt
		want bool
	}{
		{name: "current", want: true},
		{name: "v1.0.0-beta.21", opts: []dagger.ClientOpt{dagger.WithVersionOverride("v1.0.0-beta.21")}, want: true},
		{name: "v1.0.0-beta.20", opts: []dagger.ClientOpt{dagger.WithVersionOverride("v1.0.0-beta.20")}, want: true},
		{name: "v1.0.0-0", opts: []dagger.ClientOpt{dagger.WithVersionOverride("v1.0.0-0")}, want: true},
		{name: "v0.21.0", opts: []dagger.ClientOpt{dagger.WithVersionOverride("v0.21.0")}},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			res, err := testutil.Query[struct {
				FormatIdentifiers []string
			}](t, `{ formatIdentifiers(names: ["prerequisiteSHAs"], casing: SNAKE) }`, nil, tc.opts...)
			if !tc.want {
				require.ErrorContains(t, err, "formatIdentifiers")
				return
			}
			require.NoError(t, err)
			require.Equal(t, []string{"prerequisite_shas"}, res.FormatIdentifiers)
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

	c := core.NewQuery(connect(ctx, t))
	formatted, err := c.FormatIdentifiers(ctx, names, core.CasingSnake)
	require.NoError(t, err)
	require.Len(t, formatted, len(names))
	for i, name := range names {
		id, err := naming.Parse(name)
		require.NoError(t, err)
		require.Equal(t, id.Format(naming.Snake, naming.Uppercase), formatted[i], name)
	}
}
