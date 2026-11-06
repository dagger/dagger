package daggercmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/internal/cloud"
)

// fakeDiscoverCloud stands in for Dagger Cloud's GraphQL endpoint, answering
// discoverModules with a fixed response and recording each request.
type fakeDiscoverCloud struct {
	t        *testing.T
	status   int
	response string

	mu       sync.Mutex
	requests []fakeDiscoverRequest
}

type fakeDiscoverRequest struct {
	Header    http.Header
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func newFakeDiscoverCloud(t *testing.T, status int, response string) *fakeDiscoverCloud {
	t.Helper()
	fake := &fakeDiscoverCloud{t: t, status: status, response: response}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/query" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var req fakeDiscoverRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Error(err)
			return
		}
		req.Header = r.Header.Clone()
		fake.mu.Lock()
		fake.requests = append(fake.requests, req)
		fake.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(fake.status)
		_, _ = io.WriteString(w, fake.response)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("DAGGER_CLOUD_URL", srv.URL)
	t.Setenv("DAGGER_CLOUD_TOKEN", "dag_test_search_token")
	t.Setenv("DO_NOT_TRACK", "")
	return fake
}

func (f *fakeDiscoverCloud) only() fakeDiscoverRequest {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Len(f.t, f.requests, 1)
	return f.requests[0]
}

const discoverTwoResults = `{"data":{"discoverModules":{"id":"evt_1","results":[
  {"name":"ruff","kind":"MODULE","ref":"dagger.io/python/ruff","aliases":[],"description":"Lint and format Python projects with Ruff","docsUrl":"https://docs.dagger.io/reference/modules/python/ruff","publisher":"Dagger","official":true,"matchedOn":["name"],"coverage":[]},
  {"name":"python","kind":"SDK","ref":"dagger.io/sdk/python","aliases":["py"],"description":"Official Dagger SDK for Python","docsUrl":null,"publisher":"Dagger","official":true,"matchedOn":["description"],"coverage":[]}
]}}}`

// discoverWithCoverage answers a search for the capabilities "lint",
// "format code" and "sign an apk", the way the API does: one coverage entry
// per capability, in request order.
const discoverWithCoverage = `{"data":{"discoverModules":{"id":"evt_4","results":[
  {"name":"ruff","kind":"MODULE","ref":"dagger.io/python/ruff","aliases":[],"description":"Lint and format Python projects with Ruff","docsUrl":null,"publisher":"Dagger","official":true,"matchedOn":["artifacts"],"coverage":[
    {"capability":"lint","status":"COVERED","score":0.93,"role":"CHECK","uri":"dag+check://?check=ruff/projects/lint","type":"Check","object":null,"function":null,"setting":null,
     "dimensions":[{"name":"ruff-project","keyName":"path","keyDescription":"Path of the project, relative to the workspace."}],"description":"Lint this project with ruff.","matchedOn":["name"]},
    {"capability":"format code","status":"PARTIAL","score":0.41,"role":"GENERATOR","uri":"dag+generator://?generator=ruff/projects/format","type":"Generator","object":null,"function":null,"setting":null,
     "dimensions":[{"name":"ruff-project","keyName":"path","keyDescription":"Path of the project, relative to the workspace."}],"description":"Format this project with ruff. Rewrites files in place.","matchedOn":["description"]},
    {"capability":"sign an apk","status":"MISSING","score":0.0,"role":null,"uri":null,"type":null,"object":null,"function":null,"setting":null,"dimensions":[],"description":null,"matchedOn":[]}
  ]}
]}}}`

const discoverNoResults = `{"data":{"discoverModules":{"id":"evt_2","results":[]}}}`

func tracedContext(t *testing.T) (context.Context, trace.TraceID) {
	t.Helper()
	traceID, err := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("0102030405060708")
	require.NoError(t, err)
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled})
	return trace.ContextWithSpanContext(t.Context(), sc), traceID
}

func runModuleSearchForTest(t *testing.T, ctx context.Context, opts moduleSearchOpts) (string, error) {
	t.Helper()
	client, err := moduleSearchClient(ctx)
	require.NoError(t, err)
	var out bytes.Buffer
	err = moduleSearch(ctx, client, &out, opts)
	return out.String(), err
}

func TestModuleSearchRendersResults(t *testing.T) {
	fake := newFakeDiscoverCloud(t, http.StatusOK, discoverTwoResults)
	ctx, traceID := tracedContext(t)

	out, err := runModuleSearchForTest(t, ctx, moduleSearchOpts{
		Query:   "python",
		Purpose: "lint my Python code in CI",
	})
	require.NoError(t, err)
	require.Equal(t, ""+
		"SOURCE                 DESCRIPTION\n"+
		"dagger.io/python/ruff  Lint and format Python projects with Ruff\n"+
		"dagger.io/sdk/python   Official Dagger SDK for Python\n", out)

	req := fake.only()
	require.Contains(t, req.Query, "discoverModules(query: $query, purpose: $purpose, capabilities: $capabilities)")
	require.Equal(t, map[string]any{
		"query":   "python",
		"purpose": "lint my Python code in CI",
	}, req.Variables)
	require.Regexp(t, `^dagger/\S+$`, req.Header.Get("User-Agent"))
	require.Contains(t, req.Header.Get("Traceparent"), traceID.String())
	require.Empty(t, req.Header.Get("DNT"))
	require.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte("dag_test_search_token:")), req.Header.Get("Authorization"))
}

func TestModuleSearchRendersCoverage(t *testing.T) {
	fake := newFakeDiscoverCloud(t, http.StatusOK, discoverWithCoverage)
	out, err := runModuleSearchForTest(t, t.Context(), moduleSearchOpts{
		Purpose:      "keep my Python code tidy",
		Capabilities: []string{"lint", " format code ", "", "sign an apk"},
	})
	require.NoError(t, err)
	require.Equal(t, ""+
		"SOURCE                 DESCRIPTION\n"+
		"dagger.io/python/ruff  Lint and format Python projects with Ruff\n"+
		`  covered  "lint"         check      dag+check://?check=ruff/projects/lint [ruff-project=<path>]`+"\n"+
		`  partial  "format code"  generator  dag+generator://?generator=ruff/projects/format [ruff-project=<path>] — Format this project with ruff.`+"\n"+
		`  missing  "sign an apk"`+"\n", out)
	require.NotContains(t, out, "dagger install", "no command suggestions")

	require.Equal(t, map[string]any{
		"purpose":      "keep my Python code tidy",
		"capabilities": []any{"lint", "format code", "sign an apk"},
	}, fake.only().Variables)
}

func TestModuleSearchNoArgsSendsNoFilters(t *testing.T) {
	fake := newFakeDiscoverCloud(t, http.StatusOK, discoverTwoResults)
	_, err := runModuleSearchForTest(t, t.Context(), moduleSearchOpts{})
	require.NoError(t, err)
	require.Empty(t, fake.only().Variables)
}

func TestModuleSearchJSON(t *testing.T) {
	newFakeDiscoverCloud(t, http.StatusOK, discoverTwoResults)
	out, err := runModuleSearchForTest(t, t.Context(), moduleSearchOpts{Query: "python", JSON: true})
	require.NoError(t, err)

	var got cloud.ModuleDiscovery
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Equal(t, "evt_1", got.ID)
	require.Len(t, got.Results, 2)
	require.Equal(t, cloud.ModuleListing{
		Name:        "ruff",
		Kind:        cloud.ModuleKindModule,
		Ref:         "dagger.io/python/ruff",
		Aliases:     []string{},
		Description: "Lint and format Python projects with Ruff",
		DocsURL:     "https://docs.dagger.io/reference/modules/python/ruff",
		Publisher:   "Dagger",
		Official:    true,
		MatchedOn:   []string{"name"},
		Coverage:    []cloud.CapabilityCoverage{},
	}, got.Results[0])
	require.Contains(t, out, `"docsUrl": "https://docs.dagger.io/reference/modules/python/ruff"`)
	require.Contains(t, out, `"matchedOn": [`)
	require.Contains(t, out, `"coverage": []`)
}

func TestModuleSearchJSONCoverage(t *testing.T) {
	newFakeDiscoverCloud(t, http.StatusOK, discoverWithCoverage)
	out, err := runModuleSearchForTest(t, t.Context(), moduleSearchOpts{
		Capabilities: []string{"lint", "format code", "sign an apk"},
		JSON:         true,
	})
	require.NoError(t, err)

	var got cloud.ModuleDiscovery
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got.Results, 1)
	coverage := got.Results[0].Coverage
	require.Len(t, coverage, 3)
	require.Equal(t, cloud.CapabilityCoverage{
		Capability: "lint",
		Status:     cloud.CoverageCovered,
		Score:      0.93,
		Role:       cloud.ArtifactRoleCheck,
		URI:        "dag+check://?check=ruff/projects/lint",
		Type:       "Check",
		Dimensions: []cloud.ModuleDimension{{
			Name: "ruff-project", KeyName: "path", KeyDescription: "Path of the project, relative to the workspace.",
		}},
		Description: "Lint this project with ruff.",
		MatchedOn:   []string{"name"},
	}, coverage[0])
	require.Equal(t, cloud.CapabilityCoverage{
		Capability: "sign an apk",
		Status:     cloud.CoverageMissing,
		Dimensions: []cloud.ModuleDimension{},
		MatchedOn:  []string{},
	}, coverage[2])
}

func TestModuleSearchSDKOnly(t *testing.T) {
	newFakeDiscoverCloud(t, http.StatusOK, discoverTwoResults)
	out, err := runModuleSearchForTest(t, t.Context(), moduleSearchOpts{Query: "python", SDKOnly: true})
	require.NoError(t, err)
	require.Contains(t, out, "dagger.io/sdk/python")
	require.NotContains(t, out, "dagger.io/python/ruff")

	out, err = runModuleSearchForTest(t, t.Context(), moduleSearchOpts{Query: "python", SDKOnly: true, JSON: true})
	require.NoError(t, err)
	var got cloud.ModuleDiscovery
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got.Results, 1)
	require.Equal(t, cloud.ModuleKindSDK, got.Results[0].Kind)
}

func TestModuleSearchSDKOnlyWithoutSDKMatches(t *testing.T) {
	newFakeDiscoverCloud(t, http.StatusOK, `{"data":{"discoverModules":{"id":"evt_3","results":[
	  {"name":"ruff","kind":"MODULE","ref":"dagger.io/python/ruff","aliases":[],"description":"Lint Python","publisher":"Dagger","official":true,"matchedOn":["name"]}
	]}}}`)
	out, err := runModuleSearchForTest(t, t.Context(), moduleSearchOpts{Query: "ruff", SDKOnly: true})
	require.NoError(t, err)
	require.Equal(t, "No published SDK matches, though other modules do.\n", out)
}

func TestModuleSearchNoResults(t *testing.T) {
	newFakeDiscoverCloud(t, http.StatusOK, discoverNoResults)
	out, err := runModuleSearchForTest(t, t.Context(), moduleSearchOpts{Query: "cobol"})
	require.NoError(t, err)
	require.Equal(t, "No published module matches. This search was logged so Dagger can see what's missing.\n", out)

	out, err = runModuleSearchForTest(t, t.Context(), moduleSearchOpts{Query: "cobol", JSON: true})
	require.NoError(t, err)
	require.Contains(t, out, `"results": []`)
}

func TestModuleSearchCloudError(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		response string
		want     string
	}{
		{"http error", http.StatusBadGateway, "upstream unavailable", "502 Bad Gateway: upstream unavailable"},
		{"graphql error", http.StatusOK, `{"errors":[{"message":"rate limited"}]}`, "rate limited"},
		{"rejected credentials", http.StatusUnauthorized, "invalid token", "rejected the Cloud credentials"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newFakeDiscoverCloud(t, tc.status, tc.response)
			out, err := runModuleSearchForTest(t, t.Context(), moduleSearchOpts{Query: "python"})
			require.Error(t, err)
			require.Empty(t, out, "a failed search prints no results")
			require.Contains(t, err.Error(), "search the Dagger module index")
			require.Contains(t, err.Error(), tc.want)
			if tc.status != http.StatusUnauthorized {
				require.Contains(t, err.Error(), "no offline fallback")
			}
			require.NotContains(t, err.Error(), "dag_test_search_token")
		})
	}

	t.Run("unreachable", func(t *testing.T) {
		newFakeDiscoverCloud(t, http.StatusOK, discoverTwoResults)
		srv := httptest.NewServer(http.NotFoundHandler())
		srv.Close()
		t.Setenv("DAGGER_CLOUD_URL", srv.URL)
		_, err := runModuleSearchForTest(t, t.Context(), moduleSearchOpts{Query: "python"})
		require.ErrorContains(t, err, "no offline fallback")
	})
}

func TestModuleSearchDoNotTrack(t *testing.T) {
	fake := newFakeDiscoverCloud(t, http.StatusOK, discoverTwoResults)
	t.Setenv("DO_NOT_TRACK", "1")
	ctx, _ := tracedContext(t)

	_, err := runModuleSearchForTest(t, ctx, moduleSearchOpts{Query: "python", Purpose: "lint"})
	require.NoError(t, err)

	req := fake.only()
	require.Equal(t, "1", req.Header.Get("DNT"))
	require.Empty(t, req.Header.Get("Traceparent"), "no trace identity on opt-out")
	require.Regexp(t, `^dagger/\S+$`, req.Header.Get("User-Agent"))
	require.Equal(t, "python", req.Variables["query"], "the search itself still reaches Cloud")
}

func TestModuleSearchHelpDisclosesLogging(t *testing.T) {
	require.Contains(t, searchCmd.Long, "Searches are logged to help Dagger see which modules people need")
	require.Contains(t, searchCmd.Long, "DO_NOT_TRACK=1")
	require.Contains(t, searchCmd.Long, "no offline fallback")
	for _, flag := range []string{"for", "capability", "json", "sdk"} {
		require.NotNil(t, searchCmd.Flags().Lookup(flag), flag)
	}
	require.Nil(t, searchCmd.Flags().Lookup("function"), "--capability replaces --function")
}
