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

	ok, fields, err := dagql.PeekRootFields(req)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"container", "version"}, fields)

	restored, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, body, string(restored))
}

func TestPeekRootFieldsGET(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/query?query=%7B+__schema+%7B+queryType+%7B+name+%7D+%7D+%7D", nil)
	ok, fields, err := dagql.PeekRootFields(req)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"__schema"}, fields)
}

func TestPeekRootFieldsAmbiguousOperation(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(`{"query":"query A { version } query B { container { id } }"}`))
	req.Header.Set("Content-Type", "application/json")

	ok, fields, err := dagql.PeekRootFields(req)
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, fields)
}

func TestPeekRootFieldsRejectsBatch(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(`[{"query":"{ version }"}]`))
	req.Header.Set("Content-Type", "application/json")

	ok, fields, err := dagql.PeekRootFields(req)
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, fields)
}
