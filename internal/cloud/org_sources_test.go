package cloud

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSourcesProviderMetadataCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, apiError string
		fallback       bool
	}{
		{name: "old schema", apiError: `Cannot query field "provider" on type "Source".`, fallback: true},
		{name: "old instance field", apiError: `Cannot query field "instance" on type "Source".`, fallback: true},
		{name: "old arguments", apiError: `Unknown argument "providers" on field "Query.sources".`, fallback: true},
		{name: "old enum", apiError: `Unknown type "GitProvider".`, fallback: true},
		{name: "authorization failure", apiError: "unauthorized"},
		{name: "unrelated schema failure", apiError: `Cannot query field "sources" on type "Query".`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var queries []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Query     string
					Variables map[string]any
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				queries = append(queries, request.Query)
				w.Header().Set("Content-Type", "application/json")
				if len(queries) == 1 {
					require.Equal(t, []any{"GITHUB", "GITLAB"}, request.Variables["providers"])
					fmt.Fprintf(w, `{"errors":[{"message":%q}]}`, tc.apiError)
					return
				}
				fmt.Fprint(w, `{"data":{"sources":[{"id":"123","name":"acme","type":"Organization"}]}}`)
			}))
			t.Cleanup(server.Close)
			sources, err := testClient(t, server.URL).Sources(t.Context())
			if tc.fallback {
				require.NoError(t, err)
				require.Len(t, queries, 2)
				require.NotContains(t, queries[1], "\t\tprovider\n")
				require.NotContains(t, queries[1], "\t\tinstance\n")
				require.Len(t, sources, 1)
				require.Equal(t, "123", sources[0].ID)
				require.Equal(t, GitProviderGitHub, sources[0].Provider)
				require.Equal(t, "https://github.com", sources[0].Instance)
			} else {
				require.Error(t, err)
				require.True(t, strings.Contains(err.Error(), tc.apiError))
				require.Len(t, queries, 1)
			}
		})
	}
}

func TestSourcesForProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Variables struct{ Providers []GitProvider }
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Equal(t, []GitProvider{GitProviderGitHub}, request.Variables.Providers)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"sources":[]}}`)
	}))
	t.Cleanup(server.Close)
	sources, err := testClient(t, server.URL).SourcesForProvider(t.Context(), GitProviderGitHub)
	require.NoError(t, err)
	require.Empty(t, sources)
}

func TestGitLabSourcesRequireProviderAwareAPI(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"errors":[{"message":"Unknown type \"GitProvider\"."}]}`)
	}))
	t.Cleanup(server.Close)
	_, err := testClient(t, server.URL).SourcesForProvider(t.Context(), GitProviderGitLab)
	require.EqualError(t, err, "this Cloud API supports only GitHub sources")
	require.Equal(t, 1, calls, "GitLab discovery must not fall back to a GitHub-only query")
}
