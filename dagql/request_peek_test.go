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

func TestPeekWorkspaceSelectorIncludeMatchesGenerators(t *testing.T) {
	t.Parallel()

	body := `{"query":"{ currentWorkspace { generators(include: [\"go-sdk\", \"php-sdk:api\"]) { id } } }"}`
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ok, include, err := dagql.PeekWorkspaceSelectorInclude(req)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"go-sdk", "php-sdk:api"}, include)

	restored, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, body, string(restored))
}

func TestPeekWorkspaceSelectorIncludeMatchesChecks(t *testing.T) {
	t.Parallel()

	// `dagger check` roots at the same shape via the `checks` field.
	req := httptest.NewRequest(http.MethodPost, "/query",
		strings.NewReader(`{"query":"{ currentWorkspace { checks(include: [\"go-sdk:lint\"]) { id } } }"}`))
	req.Header.Set("Content-Type", "application/json")

	ok, include, err := dagql.PeekWorkspaceSelectorInclude(req)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"go-sdk:lint"}, include)
}

func TestPeekWorkspaceSelectorIncludeMatchesServices(t *testing.T) {
	t.Parallel()

	// `dagger up` roots at the same shape via the `services` field.
	req := httptest.NewRequest(http.MethodPost, "/query",
		strings.NewReader(`{"query":"{ currentWorkspace { services(include: [\"web\"]) { id } } }"}`))
	req.Header.Set("Content-Type", "application/json")

	ok, include, err := dagql.PeekWorkspaceSelectorInclude(req)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"web"}, include)
}

func TestPeekWorkspaceSelectorIncludeIgnoresSiblingArgs(t *testing.T) {
	t.Parallel()

	// `checks` carries extra args (noGenerate/onlyGenerate); they must not
	// disturb extraction of the include list.
	req := httptest.NewRequest(http.MethodPost, "/query",
		strings.NewReader(`{"query":"{ currentWorkspace { checks(include: [\"go-sdk\"], noGenerate: true) { id } } }"}`))
	req.Header.Set("Content-Type", "application/json")

	ok, include, err := dagql.PeekWorkspaceSelectorInclude(req)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"go-sdk"}, include)
}

func TestPeekWorkspaceSelectorIncludeNonSelectorQuery(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/query",
		strings.NewReader(`{"query":"{ currentWorkspace { git { head { commit } } } }"}`))
	req.Header.Set("Content-Type", "application/json")

	ok, include, err := dagql.PeekWorkspaceSelectorInclude(req)
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, include)
}

func TestPeekWorkspaceSelectorIncludeWithoutIncludeArg(t *testing.T) {
	t.Parallel()

	// No include argument means "all items" -- we can't narrow safely.
	req := httptest.NewRequest(http.MethodPost, "/query",
		strings.NewReader(`{"query":"{ currentWorkspace { generators { list { name } } } }"}`))
	req.Header.Set("Content-Type", "application/json")

	ok, include, err := dagql.PeekWorkspaceSelectorInclude(req)
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, include)
}

func TestPeekWorkspaceSelectorIncludeExtraRootField(t *testing.T) {
	t.Parallel()

	// A sibling root field beyond currentWorkspace means the query needs more
	// than the requested items' modules, so narrowing must be skipped.
	req := httptest.NewRequest(http.MethodPost, "/query",
		strings.NewReader(`{"query":"{ currentWorkspace { generators(include: [\"go-sdk\"]) { id } } version }"}`))
	req.Header.Set("Content-Type", "application/json")

	ok, include, err := dagql.PeekWorkspaceSelectorInclude(req)
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, include)
}
