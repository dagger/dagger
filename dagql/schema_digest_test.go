package dagql

import (
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/zeebo/xxh3"
)

type digestTestQuery struct{}

func (digestTestQuery) Type() *ast.Type { return &ast.Type{NamedType: "Query", NonNull: true} }

// The digest the eager code path used to compute at schema build time.
func eagerDigest(t *testing.T, schema *ast.Schema) digest.Digest {
	t.Helper()
	h := xxh3.New()
	require.NoError(t, json.NewEncoder(h).Encode(schema))
	return digest.NewDigest(hashutil.XXH3, h)
}

func TestSchemaDigestMatchesEagerDigest(t *testing.T) {
	srv, err := NewServer(t.Context(), digestTestQuery{})
	require.NoError(t, err)
	srv.InstallDirective(DirectiveSpec{Name: "futureOnly", ViewFilter: ExactView("future")})
	for _, view := range []call.View{"", "future"} {
		srv.View = view
		require.Equal(t, eagerDigest(t, srv.SchemaForView(view)), srv.SchemaDigest())
	}
	require.NotEqual(t, eagerDigest(t, srv.SchemaForView("")), eagerDigest(t, srv.SchemaForView("future")))
}

func TestSchemaDigestFollowsInvalidation(t *testing.T) {
	srv, err := NewServer(t.Context(), digestTestQuery{})
	require.NoError(t, err)
	first := srv.SchemaDigest()
	require.Equal(t, first, srv.SchemaDigest(), "memoized")

	// Invalidate after the digest was computed.
	srv.InstallDirective(DirectiveSpec{Name: "one"})
	second := srv.SchemaDigest()
	require.NotEqual(t, first, second)
	require.Equal(t, eagerDigest(t, srv.Schema()), second)

	// Invalidate before the digest was computed for the rebuilt schema.
	srv.InstallDirective(DirectiveSpec{Name: "two"})
	srv.Schema()
	srv.InstallDirective(DirectiveSpec{Name: "three"})
	require.Equal(t, eagerDigest(t, srv.Schema()), srv.SchemaDigest())
}
