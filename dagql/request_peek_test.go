package dagql_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

func TestPeekRootFieldsPOSTJSON(t *testing.T) {
	t.Parallel()

	body := `{"query":"query Picked { alias: container { id } ...More } fragment More on Query { version }","operationName":"Picked"}`
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ok, peek, err := dagql.PeekRootFields(req)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"container", "version"}, peek.Fields)

	restored, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, body, string(restored))
}

func TestPeekRootFieldsGET(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/query?query=%7B+__schema+%7B+queryType+%7B+name+%7D+%7D+%7D", nil)
	ok, peek, err := dagql.PeekRootFields(req)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"__schema"}, peek.Fields)
}

func TestPeekRootFieldsAmbiguousOperation(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(`{"query":"query A { version } query B { container { id } }"}`))
	req.Header.Set("Content-Type", "application/json")

	ok, peek, err := dagql.PeekRootFields(req)
	require.NoError(t, err)
	require.False(t, ok)
	require.Zero(t, peek)
}

func TestPeekRootFieldsRejectsBatch(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(`[{"query":"{ version }"}]`))
	req.Header.Set("Content-Type", "application/json")

	ok, peek, err := dagql.PeekRootFields(req)
	require.NoError(t, err)
	require.False(t, ok)
	require.Zero(t, peek)
}

func TestPeekRootFieldsRecoversSingleOperation(t *testing.T) {
	t.Parallel()

	// operationName omitted from the envelope: the sole operation is used.
	body := `{"query":"query Introspect { currentTypeDefs { name } }"}`
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ok, peek, err := dagql.PeekRootFields(req)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"currentTypeDefs"}, peek.Fields)
}

func TestPeekRootFieldsNodeIDs(t *testing.T) {
	t.Parallel()

	body := `{"query":"query Q($a: ID!) { group: node(id: $a) { __typename } other: node(id: \"inline\") { __typename } }","variables":{"a":"from-variable"}}`
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ok, peek, err := dagql.PeekRootFields(req)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"node"}, peek.Fields)
	require.Equal(t, []string{"from-variable", "inline"}, peek.NodeIDs)
	require.False(t, peek.UnresolvedNodeIDs)
}

func TestPeekRootFieldsUnresolvedNodeID(t *testing.T) {
	t.Parallel()

	// The variable the id references is not in the envelope, so the peek
	// cannot account for what this node selection demands.
	body := `{"query":"query Q($a: ID!) { node(id: $a) { __typename } }"}`
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ok, peek, err := dagql.PeekRootFields(req)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"node"}, peek.Fields)
	require.Empty(t, peek.NodeIDs)
	require.True(t, peek.UnresolvedNodeIDs)
}
