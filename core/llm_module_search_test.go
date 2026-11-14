package core

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/oauth2"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/cloud"
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
	  {"name":"terraform","kind":"MODULE","ref":"dagger.io/terraform","aliases":["tf"],"description":"Plan and apply Terraform","docsUrl":null,"publisher":"Dagger","official":true,"matchedOn":["name","alias"],"coverage":[
	    {"capability":"plan","status":"COVERED","score":0.9,"role":"CHECK","uri":"dag+check://?check=terraform/projects/plan","type":"Check","object":null,"function":null,"setting":null,
	     "dimensions":[{"name":"terraform-project","keyName":"path","keyDescription":"Path of the project."}],"description":"Plan this project.","matchedOn":["name"]},
	    {"capability":"apply a saved plan","status":"PARTIAL","score":0.4,"role":"FUNCTION","uri":null,"type":null,"object":"TerraformProject","function":"apply","setting":null,
	     "dimensions":[{"name":"terraform-project","keyName":"path","keyDescription":"Path of the project."}],"description":"Apply a saved plan to the workspace's infrastructure. Needs credentials.","matchedOn":["description"]},
	    {"capability":"pin the terraform version","status":"PARTIAL","score":0.3,"role":"SETTING","uri":null,"type":"String","object":null,"function":null,"setting":"version",
	     "dimensions":[],"description":"Terraform version to install. Defaults to the latest release.","matchedOn":["name"]},
	    {"capability":"lint hcl","status":"MISSING","score":0.05,"role":null,"uri":null,"type":null,"object":null,"function":null,"setting":null,"dimensions":[],"description":null,"matchedOn":[]}
	  ]},
	  {"name":"opentofu","kind":"MODULE","ref":"dagger.io/opentofu","aliases":[],"description":"Plan and apply OpenTofu","docsUrl":null,"publisher":"Dagger","official":true,"matchedOn":["description"],"coverage":[
	    {"capability":"plan","status":"PARTIAL","score":0.3,"role":"CHECK","uri":"dag+check://?check=opentofu/plan","type":"Check","object":null,"function":null,"setting":null,"dimensions":[],"description":"Plan changes with OpenTofu.","matchedOn":["description"]},
	    {"capability":"apply a saved plan","status":"MISSING","score":0.1,"role":null,"uri":null,"type":null,"object":null,"function":null,"setting":null,"dimensions":[],"description":null,"matchedOn":[]},
	    {"capability":"pin the terraform version","status":"MISSING","score":0.0,"role":null,"uri":null,"type":null,"object":null,"function":null,"setting":null,"dimensions":[],"description":null,"matchedOn":[]},
	    {"capability":"lint hcl","status":"MISSING","score":0.0,"role":null,"uri":null,"type":null,"object":null,"function":null,"setting":null,"dimensions":[],"description":null,"matchedOn":[]}
	  ]}
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
	require.Contains(t, tool.Schema["properties"], "capabilities")
	require.NotContains(t, tool.Schema["properties"], "functions")
	mcpTool, err := genMcpTool(*tool)
	require.NoError(t, err)
	require.Equal(t, searchModulesToolName, mcpTool.Name)

	t.Run("results", func(t *testing.T) {
		out, err := tool.Call(ctx, map[string]any{
			"query":        "terraform",
			"purpose":      "plan infrastructure changes in CI",
			"capabilities": []any{"plan", "apply a saved plan", "pin the terraform version", "lint hcl"},
		})
		require.NoError(t, err)
		text := out.(string)
		require.NotContains(t, text, secret)

		sp := func(n int) string { return strings.Repeat(" ", n) }
		require.Equal(t, strings.Join([]string{
			"terraform  dagger.io/terraform  Plan and apply Terraform",
			`  covered  "plan"` + sp(23) + "check" + sp(5) + "dag+check://?check=terraform/projects/plan [terraform-project=<path>]",
			`  partial  "apply a saved plan"` + sp(9) + "function" + sp(2) + "TerraformProject.apply [terraform-project=<path>] — Apply a saved plan to the workspace's infrastructure.",
			`  partial  "pin the terraform version"` + sp(2) + "setting" + sp(3) + "[modules.terraform] settings.version — Terraform version to install.",
			`  missing  "lint hcl"`,
			"opentofu  dagger.io/opentofu  Plan and apply OpenTofu",
			`  partial  "plan"` + sp(23) + "check" + sp(2) + "dag+check://?check=opentofu/plan — Plan changes with OpenTofu.",
			`  missing  "apply a saved plan"`,
			`  missing  "pin the terraform version"`,
			`  missing  "lint hcl"`,
			"",
			cloud.DiscoverFooter,
		}, "\n"), text)
		require.Equal(t, 1, strings.Count(text, cloud.DiscoverFooter), "the footer is printed once")
		require.NotContains(t, text, "dagger install", "no commands")

		reqs := requests()
		req := reqs[len(reqs)-1]
		require.Equal(t, map[string]any{
			"query":        "terraform",
			"purpose":      "plan infrastructure changes in CI",
			"capabilities": []any{"plan", "apply a saved plan", "pin the terraform version", "lint hcl"},
		}, req.Variables)
		require.Equal(t, "dagger-agent/v0.99.0", req.header.Get("User-Agent"))
		require.Equal(t, "Bearer "+secret, req.header.Get("Authorization"), "the connecting client's credentials are sent")
		require.Contains(t, req.header.Get("Traceparent"), traceID.String())
		require.Empty(t, req.header.Get("DNT"))
	})

	t.Run("no capabilities", func(t *testing.T) {
		noCoverage := `{"data":{"discoverModules":{"id":"evt_5","results":[
		  {"name":"terraform","kind":"MODULE","ref":"dagger.io/terraform","aliases":[],"description":"Plan and apply Terraform","docsUrl":null,"publisher":"Dagger","official":true,"matchedOn":["purpose"],"coverage":[]}
		]}}}`
		url, _ := searchModulesCloud(t, http.StatusOK, noCoverage)
		plainMeta := *meta
		plainMeta.CloudURL = url
		plainCtx := ContextWithQuery(baseCtx, &Query{Server: &mockServer{clientMetadata: &plainMeta}})
		out, err := tool.Call(plainCtx, map[string]any{"purpose": "deploy with terraform"})
		require.NoError(t, err)
		require.Equal(t, "terraform  dagger.io/terraform  Plan and apply Terraform\n\n"+cloud.DiscoverFooter, out)
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
	for _, tc := range []struct {
		name     string
		status   int
		response string
		want     string
	}{
		{"http error", http.StatusServiceUnavailable, "down for maintenance at internal-host:8080", "Dagger Cloud answered 503 Service Unavailable"},
		{"graphql error", http.StatusOK, `{"errors":[{"message":"rate limited"}]}`, "Dagger Cloud returned an error: rate limited"},
		{"rejected credentials", http.StatusUnauthorized, "token xyz invalid", "rejected the Cloud credentials"},
		{"unreadable answer", http.StatusOK, `{"data":{"discoverModules":{"id":"evt","results":[{"name":1}]}}}`, "Dagger Cloud's answer could not be read"},
		{"no result", http.StatusOK, `{"data":{"discoverModules":null}}`, "Dagger Cloud's answer could not be read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cloudURL, _ := searchModulesCloud(t, tc.status, tc.response)
			meta := &engine.ClientMetadata{CloudURL: cloudURL}
			ctx := ContextWithQuery(t.Context(), &Query{Server: &mockServer{clientMetadata: meta}})
			_, err := (&MCP{}).searchModulesTool(&dagql.Server{})(ctx, map[string]any{"purpose": "build COBOL"})
			require.ErrorContains(t, err, tc.want)
			require.NotContains(t, err.Error(), cloudURL, "the Cloud URL stays out of the model's view")
			if tc.status != http.StatusOK {
				require.NotContains(t, err.Error(), tc.response, "response bodies stay out of the model's view")
			}
			if tc.status != http.StatusUnauthorized {
				require.ErrorContains(t, err, "no offline fallback")
			}
		})
	}

	t.Run("unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		srv.Close()
		meta := &engine.ClientMetadata{CloudURL: srv.URL}
		ctx := ContextWithQuery(t.Context(), &Query{Server: &mockServer{clientMetadata: meta}})
		_, err := (&MCP{}).searchModulesTool(&dagql.Server{})(ctx, map[string]any{"purpose": "build COBOL"})
		require.EqualError(t, err, "search the Dagger module index: could not reach Dagger Cloud; module search needs Dagger Cloud and has no offline fallback")
	})
}

// An OAuth login is sent without its refresh token, so a search can never
// rotate the refresh token in the user's credentials file; one already expired
// searches anonymously instead of refreshing.
func TestSearchModulesToolNeverRefreshesOAuth(t *testing.T) {
	cloudURL, requests := searchModulesCloud(t, http.StatusOK, `{"data":{"discoverModules":{"id":"evt_3","results":[]}}}`)
	search := func(token *oauth2.Token) searchModulesRequest {
		t.Helper()
		meta := &engine.ClientMetadata{CloudURL: cloudURL, CloudAuth: &auth.Cloud{Token: token}}
		ctx := ContextWithQuery(t.Context(), &Query{Server: &mockServer{clientMetadata: meta}})
		_, err := (&MCP{}).searchModulesTool(&dagql.Server{})(ctx, map[string]any{"purpose": "lint Python"})
		require.NoError(t, err)
		reqs := requests()
		return reqs[len(reqs)-1]
	}

	expired := &oauth2.Token{TokenType: "Bearer", AccessToken: "stale", RefreshToken: "refresh-me", Expiry: time.Now().Add(-time.Hour)}
	require.Empty(t, search(expired).header.Get("Authorization"))
	require.Equal(t, "refresh-me", expired.RefreshToken, "the client's credential is left as it was")

	valid := &oauth2.Token{TokenType: "Bearer", AccessToken: "fresh", RefreshToken: "refresh-me", Expiry: time.Now().Add(time.Hour)}
	require.Equal(t, "Bearer fresh", search(valid).header.Get("Authorization"))
	require.Equal(t, "refresh-me", valid.RefreshToken)

	require.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte("dag_token:")),
		search(&oauth2.Token{TokenType: "Basic", AccessToken: "dag_token"}).header.Get("Authorization"))
}
