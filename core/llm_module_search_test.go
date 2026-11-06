package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/oauth2"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/cloud/auth"
)

type searchModulesRequest struct {
	header    http.Header
	Variables map[string]any `json:"variables"`
}

func searchModulesCloud(t *testing.T, status int, response string) (string, func() []searchModulesRequest) {
	t.Helper()
	var mu sync.Mutex
	var requests []searchModulesRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/query" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var req searchModulesRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Error(err)
			return
		}
		req.header = r.Header.Clone()
		mu.Lock()
		requests = append(requests, req)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() []searchModulesRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]searchModulesRequest(nil), requests...)
	}
}

func TestSearchModulesTool(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	const secret = "agent-search-credential-never-in-output"
	const results = `{"data":{"discoverModules":{"id":"evt_1","results":[
	  {"name":"terraform","kind":"MODULE","ref":"dagger.io/terraform","aliases":["tf"],"description":"Plan and apply Terraform","docsUrl":"https://docs.dagger.io/reference/modules/terraform","publisher":"Dagger","official":true,"matchedOn":["name","alias"]},
	  {"name":"opentofu","kind":"MODULE","ref":"dagger.io/opentofu","aliases":[],"description":"Plan and apply OpenTofu","docsUrl":null,"publisher":"Dagger","official":true,"matchedOn":["description"]}
	]}}}`
	cloudURL, requests := searchModulesCloud(t, http.StatusOK, results)

	traceID, err := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("0102030405060708")
	require.NoError(t, err)
	baseCtx := trace.ContextWithSpanContext(t.Context(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	}))

	meta := &engine.ClientMetadata{
		ClientVersion: "v0.99.0",
		CloudURL:      cloudURL,
		CloudAuth:     &auth.Cloud{Token: &oauth2.Token{TokenType: "Bearer", AccessToken: secret}},
	}
	ctx := ContextWithQuery(baseCtx, &Query{Server: &mockServer{clientMetadata: meta}})

	m := newMCP()
	tools := NewLLMToolSet()
	m.loadBuiltins(&dagql.Server{}, tools)
	tool, err := m.LookupTool(searchModulesToolName, tools.Order)
	require.NoError(t, err)
	require.True(t, tool.ReadOnly)
	require.Contains(t, tool.Description, "Every search is logged by Dagger Cloud")
	require.Contains(t, tool.Description, "installs nothing")
	mcpTool, err := genMcpTool(*tool)
	require.NoError(t, err)
	require.Equal(t, searchModulesToolName, mcpTool.Name)

	t.Run("results", func(t *testing.T) {
		out, err := tool.Call(ctx, map[string]any{
			"query":     "terraform",
			"purpose":   "plan infrastructure changes in CI",
			"functions": []any{"plan", "apply"},
		})
		require.NoError(t, err)
		text := out.(string)
		require.NotContains(t, text, secret)

		var got []map[string]any
		require.NoError(t, json.Unmarshal([]byte(text), &got))
		require.Equal(t, []map[string]any{
			{
				"name":        "terraform",
				"ref":         "dagger.io/terraform",
				"description": "Plan and apply Terraform",
				"docsUrl":     "https://docs.dagger.io/reference/modules/terraform",
				"official":    true,
				"matchedOn":   []any{"name", "alias"},
			},
			{
				"name":        "opentofu",
				"ref":         "dagger.io/opentofu",
				"description": "Plan and apply OpenTofu",
				"official":    true,
				"matchedOn":   []any{"description"},
			},
		}, got)

		reqs := requests()
		req := reqs[len(reqs)-1]
		require.Equal(t, map[string]any{
			"query":     "terraform",
			"purpose":   "plan infrastructure changes in CI",
			"functions": []any{"plan", "apply"},
		}, req.Variables)
		require.Equal(t, "dagger-agent/v0.99.0", req.header.Get("User-Agent"))
		require.Equal(t, "Bearer "+secret, req.header.Get("Authorization"), "the connecting client's credentials are sent")
		require.Contains(t, req.header.Get("Traceparent"), traceID.String())
		require.Empty(t, req.header.Get("DNT"))
	})

	t.Run("purpose is required", func(t *testing.T) {
		before := len(requests())
		_, err := tool.Call(ctx, map[string]any{"query": "terraform"})
		require.ErrorContains(t, err, "purpose is required")
		require.Len(t, requests(), before, "nothing is sent without a purpose")
	})

	t.Run("anonymous without credentials", func(t *testing.T) {
		anonMeta := *meta
		anonMeta.CloudAuth = nil
		anonCtx := ContextWithQuery(baseCtx, &Query{Server: &mockServer{clientMetadata: &anonMeta}})
		_, err := tool.Call(anonCtx, map[string]any{"purpose": "deploy with terraform"})
		require.NoError(t, err)
		reqs := requests()
		req := reqs[len(reqs)-1]
		require.Empty(t, req.header.Get("Authorization"))
		require.Equal(t, map[string]any{"purpose": "deploy with terraform"}, req.Variables)
	})

	t.Run("do not track", func(t *testing.T) {
		dntMeta := *meta
		dntMeta.DoNotTrack = true
		dntCtx := ContextWithQuery(baseCtx, &Query{Server: &mockServer{clientMetadata: &dntMeta}})
		_, err := tool.Call(dntCtx, map[string]any{"purpose": "deploy with terraform"})
		require.NoError(t, err)
		reqs := requests()
		req := reqs[len(reqs)-1]
		require.Equal(t, "1", req.header.Get("DNT"))
		require.Empty(t, req.header.Get("Traceparent"))
	})
}

func TestSearchModulesToolNoResults(t *testing.T) {
	cloudURL, _ := searchModulesCloud(t, http.StatusOK, `{"data":{"discoverModules":{"id":"evt_2","results":[]}}}`)
	meta := &engine.ClientMetadata{CloudURL: cloudURL}
	ctx := ContextWithQuery(t.Context(), &Query{Server: &mockServer{clientMetadata: meta}})
	out, err := (&MCP{}).searchModulesTool(&dagql.Server{})(ctx, map[string]any{"purpose": "build COBOL"})
	require.NoError(t, err)
	require.Equal(t, "No published module matches. This search was logged so Dagger can see what's missing. Don't retry with synonyms.", out)
}

func TestSearchModulesToolCloudError(t *testing.T) {
	cloudURL, _ := searchModulesCloud(t, http.StatusServiceUnavailable, "down for maintenance")
	meta := &engine.ClientMetadata{CloudURL: cloudURL}
	ctx := ContextWithQuery(t.Context(), &Query{Server: &mockServer{clientMetadata: meta}})
	_, err := (&MCP{}).searchModulesTool(&dagql.Server{})(ctx, map[string]any{"purpose": "build COBOL"})
	require.ErrorContains(t, err, "503 Service Unavailable")
	require.ErrorContains(t, err, "no offline fallback")
}
