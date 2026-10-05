package core

// These tests cover the identifier casing API, which exposes engine/naming:
// parsing a name into words, formatting it in another casing, and reading the
// naming dictionary. The algorithm itself is covered by engine/naming's tests.

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
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
