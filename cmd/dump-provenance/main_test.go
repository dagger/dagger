package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dagger "dagger.io/dagger"
	"github.com/dagger/dagger/core/provenance"
	"github.com/stretchr/testify/require"
)

type testConnection struct {
	*http.Client
	host string
}

func (c *testConnection) Host() string { return c.host }
func (*testConnection) Close() error   { return nil }

func apiClient(t *testing.T, handler http.HandlerFunc) *dagger.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := dagger.Connect(t.Context(), dagger.WithConn(&testConnection{Client: server.Client(), host: strings.TrimPrefix(server.URL, "http://")}))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return client
}

const publicItem = `{"path":["check"],"uri":"dag://?check=check","provenance":{"issues":[],"selectors":[{"path":"/src","include":[],"exclude":[],"conservative":false,"kind":"WORKSPACE_READ","origin":"argument source"}]}}`

func TestCollectOnePublicRequest(t *testing.T) {
	requests := 0
	uri := "dag://?check=test"
	client := apiClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "/query", r.URL.Path)
		var query dagger.Request
		require.NoError(t, json.NewDecoder(r.Body).Decode(&query))
		require.Contains(t, query.Query, "currentWorkspace { snapshot")
		require.Contains(t, query.Query, "filterTypes(types: $types)")
		require.Contains(t, query.Query, "filterUri(uri: $uri)")
		require.NotContains(t, query.Query, uri)
		require.Contains(t, query.Query, "path include exclude conservative kind origin")
		require.Equal(t, map[string]any{"types": []any{"Check"}, "uri": uri}, query.Variables)
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"data":{"currentWorkspace":{"snapshot":{"address":"file:///fixture","cwd":".","artifacts":{"selection":{"selection":{"items":[` + publicItem + `]}}}}}}}`))
		require.NoError(t, err)
	})
	result, err := collect(t.Context(), client, options{artifactType: "Check", uri: uri})
	require.NoError(t, err)
	require.Equal(t, 1, requests)
	require.Equal(t, "engine-backed", result.Collection.Mode)
	require.Equal(t, "file:///fixture", result.Collection.Workspace)
	require.Len(t, result.Artifacts, 1)
	require.Equal(t, "/src", result.Artifacts[0].Provenance.Selectors[0].Path)
}

func TestCollectRejectsMissingOrFailedData(t *testing.T) {
	for _, test := range []struct{ name, body string }{
		{"malformed", `{`},
		{"missing Workspace", `{"data":{}}`},
		{"missing items", `{"data":{"currentWorkspace":{"snapshot":{"address":"file:///fixture","cwd":".","artifacts":{}}}}}`},
		{"partial provenance", `{"data":{"currentWorkspace":{"snapshot":{"address":"file:///fixture","cwd":".","artifacts":{"items":[{"path":["check"],"uri":"dag://?check=check","provenance":{"selectors":[]}}]}}}}}`},
		{"null conservative", `{"data":{"currentWorkspace":{"snapshot":{"address":"file:///fixture","cwd":".","artifacts":{"items":[` + strings.Replace(publicItem, `"conservative":false`, `"conservative":null`, 1) + `]}}}}}`},
		{"API error with data", `{"errors":[{"message":"source lookup failed"}],"data":{"currentWorkspace":{"snapshot":{"address":"file:///fixture","cwd":".","artifacts":{"items":[]}}}}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := apiClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(test.body))
				require.NoError(t, err)
			})
			result, err := collect(t.Context(), client, options{})
			require.Error(t, err)
			require.Nil(t, result)
		})
	}
	t.Run("explicit empty selection", func(t *testing.T) {
		client := apiClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"data":{"currentWorkspace":{"snapshot":{"address":"file:///fixture","cwd":".","artifacts":{"items":[]}}}}}`))
			require.NoError(t, err)
		})
		result, err := collect(t.Context(), client, options{})
		require.NoError(t, err)
		require.Empty(t, result.Artifacts)
	})
}

func TestInheritedSessionScope(t *testing.T) {
	for _, opts := range []options{{workspace: "/other"}, {workdir: "/other"}} {
		_, err := connectionOptions(opts, true)
		require.ErrorContains(t, err, "cannot honor -workspace or -workdir")
	}
	_, err := connectionOptions(options{}, true)
	require.NoError(t, err)
}

func TestSavedLocalMatches(t *testing.T) {
	// Offline input must not open the inherited session, even if it is invalid.
	t.Setenv("DAGGER_SESSION_PORT", "invalid")
	var stdout, stderr bytes.Buffer
	require.NoError(t, run(t.Context(), []string{"-input", "testdata/report.json", "-match-file", "/.env", "-match-file", "/README.md", "-match-path", "/docs"}, strings.NewReader(""), &stdout, &stderr))
	result, err := readReport(&stdout)
	require.NoError(t, err)
	require.Equal(t, "engine-backed", result.Collection.Mode, "saved input retains its original collection method")
	require.Len(t, result.Artifacts, 2)
	selectors, issues, hostRules := 0, 0, 0
	for _, a := range result.Artifacts {
		selectors += len(a.Provenance.Selectors)
		issues += len(a.Provenance.Issues)
		require.Len(t, a.Matches, 3)
		for _, match := range a.Matches {
			require.True(t, match.Affected, "the saved issues retain conservative root dependencies")
		}
		for _, s := range a.Provenance.Selectors {
			if strings.Contains(s.Origin, "Host.findUp") {
				hostRules++
				for _, file := range []string{"/child/.env", "/.env/child"} {
					affected, err := provenance.AffectedByFile([]provenance.Selector{provenance.Selector(s)}, file, nil)
					require.NoError(t, err)
					require.False(t, affected)
				}
			}
		}
	}
	require.Equal(t, 4, selectors)
	require.Equal(t, 2, issues)
	require.Equal(t, 2, hostRules)
	for _, value := range []string{`{}`, `{"collection":{"mode":"engine-backed"},"artifacts":[]}`, `{"collection":{"mode":"engine-backed","snapshot":true},"artifacts":[{"path":[],"uri":"dag://","provenance":{"selectors":[]}}]}`} {
		_, err := readReport(strings.NewReader(value))
		require.Error(t, err)
	}
}

func TestMatchPathsWithEmptySelection(t *testing.T) {
	result := &report{Artifacts: []artifact{}}
	for _, path := range []string{"../escape", "nul\x00path"} {
		require.Error(t, result.match([]string{path}, nil))
		require.Error(t, result.match(nil, []string{path}))
	}
}
