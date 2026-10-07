package core

// These tests cover the identifier casing API, which exposes engine/naming:
// parsing a name into words, formatting it in another casing, and reading the
// naming dictionary. The algorithm itself is covered by engine/naming's tests.

import (
	"context"
	"encoding/json"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/engine/naming"
	"github.com/dagger/dagger/internal/testutil"
)

type IdentifierSuite struct{}

func TestIdentifier(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(IdentifierSuite{})
}

func (IdentifierSuite) TestWords(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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
	require.Equal(t, dagger.IdentifierWordKindWord, kind)
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
	require.Equal(t, dagger.IdentifierWordKindAcronym, kind)
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
	require.Equal(t, dagger.IdentifierWordKindTerm, kind)
	term, err = words[0].Term(ctx)
	require.NoError(t, err)
	require.NotNil(t, term)
	capitalized, err := term.Capitalized(ctx)
	require.NoError(t, err)
	require.Equal(t, "Ipv6", capitalized)
}

func (IdentifierSuite) TestFormat(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	id := c.Identifier("http_api_client")
	for _, tc := range []struct {
		casing   dagger.Casing
		acronyms dagger.AcronymStyle
		want     string
	}{
		{dagger.CasingPascal, "", "HTTPAPIClient"},
		{dagger.CasingPascal, dagger.AcronymStyleCapitalized, "HttpApiClient"},
		{dagger.CasingCamel, dagger.AcronymStyleUppercase, "httpAPIClient"},
		{dagger.CasingSnake, "", "http_api_client"},
		{dagger.CasingScreamingSnake, "", "HTTP_API_CLIENT"},
		{dagger.CasingKebab, "", "http-api-client"},
		{dagger.CasingFlat, "", "httpapiclient"},
	} {
		got, err := id.Format(ctx, tc.casing, dagger.IdentifierFormatOpts{Acronyms: tc.acronyms})
		require.NoError(t, err)
		require.Equal(t, tc.want, got, "%s/%s", tc.casing, tc.acronyms)
	}
}

func (IdentifierSuite) TestFormatIdentifiers(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	names := []string{"HTTPClient", "userIds", "GitHubRepo", "E2ETest", "md5sum"}
	got, err := c.FormatIdentifiers(ctx, names, dagger.CasingPascal)
	require.NoError(t, err)
	require.Equal(t, []string{"HTTPClient", "UserIDs", "GitHubRepo", "E2ETest", "MD5Sum"}, got)

	got, err = c.FormatIdentifiers(ctx, names, dagger.CasingPascal, dagger.FormatIdentifiersOpts{
		Acronyms: dagger.AcronymStyleCapitalized,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"HttpClient", "UserIds", "GitHubRepo", "E2eTest", "Md5Sum"}, got)

	got, err = c.FormatIdentifiers(ctx, names, dagger.CasingSnake)
	require.NoError(t, err)
	require.Equal(t, []string{"http_client", "user_ids", "github_repo", "e2e_test", "md5_sum"}, got)
}

func (IdentifierSuite) TestErrors(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := c.Identifier("Café").Name(ctx)
	require.ErrorContains(t, err, "identifier must be ASCII")

	_, err = c.Identifier("__").Name(ctx)
	require.ErrorContains(t, err, "identifier must contain a letter or digit")

	_, err = c.FormatIdentifiers(ctx, []string{"ok", "Café"}, dagger.CasingSnake)
	require.ErrorContains(t, err, "identifier must be ASCII")
}

func (IdentifierSuite) TestNamingDictionary(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

// The schema JSON handed to SDK codegen carries every name's words from
// v1.0.0 on, and the live introspection path fetches the same words; older
// clients get neither, so their codegen keeps its own conversion.
func (IdentifierSuite) TestSchemaJSONIdentifiers(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		name string
		opts []dagger.ClientOpt
		want bool
	}{
		{name: "current", want: true},
		{name: "v0.21.0", opts: []dagger.ClientOpt{dagger.WithVersionOverride("v0.21.0")}},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			res, err := testutil.Query[struct {
				File struct {
					Contents string `json:"contents"`
				} `json:"__schemaJSONFile"`
			}](t, `{ __schemaJSONFile { contents } }`, nil, tc.opts...)
			require.NoError(t, err)
			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(res.File.Contents), &raw))
			var resp introspection.Response
			require.NoError(t, json.Unmarshal([]byte(res.File.Contents), &resp))

			live, _, err := introspection.Introspect(ctx, connect(ctx, t, tc.opts...))
			require.NoError(t, err)

			if !tc.want {
				require.NotContains(t, raw, "__identifiers")
				require.Nil(t, resp.Schema.Identifiers)
				require.Nil(t, live.Identifiers)
				return
			}

			require.Contains(t, raw, "__identifiers")
			require.Contains(t, resp.Schema.Identifiers, "Container")
			id, ok := resp.Schema.Identifier("experimentalWithAllGPUs")
			require.True(t, ok)
			require.Equal(t, "experimental_with_all_gpus", id.Format(naming.Snake, naming.Uppercase))
			id, ok = resp.Schema.Identifier("prerequisiteSHAs")
			require.True(t, ok)
			require.Equal(t, "PrerequisiteShas", id.Format(naming.Pascal, naming.Capitalized))

			require.NotEmpty(t, live.Identifiers)
			for name, words := range live.Identifiers {
				if fromJSON, ok := resp.Schema.Identifiers[name]; ok {
					require.Equal(t, fromJSON, words, name)
				}
			}
			require.Equal(t, resp.Schema.Identifiers["prerequisiteSHAs"], live.Identifiers["prerequisiteSHAs"])
		})
	}
}
