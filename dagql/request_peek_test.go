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

func TestPeekRootFieldsAndOperationRecoversNameFromDocument(t *testing.T) {
	t.Parallel()

	// operationName omitted from the envelope -- recovered from the document.
	body := `{"query":"query DaggerModuleScope_good { currentTypeDefs { name } }"}`
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ok, operationName, fields, err := dagql.PeekRootFieldsAndOperation(req)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "DaggerModuleScope_good", operationName)
	require.Equal(t, []string{"currentTypeDefs"}, fields)

	scope, scoped := dagql.ModuleScopeFromOperationName(operationName)
	require.True(t, scoped)
	require.Equal(t, "good", scope)
}

func TestModuleScopeOperationNameRoundTrip(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		scope  string
		opName string
	}{
		{"good", "DaggerModuleScope_good"},
		// kebab-case command names become identifier-safe; the server
		// kebab-normalizes the decoded token, so the original still matches.
		{"good-mod", "DaggerModuleScope_good_mod"},
		{"greet", "DaggerModuleScope_greet"},
	} {
		opName := dagql.ModuleScopeOperationName(tc.scope)
		require.Equal(t, tc.opName, opName)

		decoded, ok := dagql.ModuleScopeFromOperationName(opName)
		require.True(t, ok)
		require.Equal(t, dagql.ModuleScopeOperationName(decoded), opName)
	}
}

func TestModuleScopeFromOperationNameIgnoresUnscoped(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "TypeDefs", "Picked", "DaggerModuleScope_"} {
		_, ok := dagql.ModuleScopeFromOperationName(name)
		require.False(t, ok, name)
	}
}
