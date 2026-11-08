package client

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestClientLiveLLMConfig(t *testing.T) {
	bootstrap := &engine.LLMConfig{DefaultProvider: "anthropic"}
	c := &Client{Params: Params{SecretToken: "session-token", LLMConfig: bootstrap}}
	var got *engine.LLMConfig
	status := http.StatusOK
	c.httpClient = &httpClient{secretToken: c.engineToken(), inner: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, engine.LLMConfigEndpoint, req.URL.Path)
		token, _, ok := req.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "session-token", token)
		body := `{"configured":true,"models":{"providers":{"anthropic":{"model":"claude-sonnet-4-5"}}}}`
		if req.Method == http.MethodPut {
			require.Equal(t, "application/json", req.Header.Get("Content-Type"))
			require.NoError(t, json.NewDecoder(req.Body).Decode(&got))
			body = ""
		}
		if status == http.StatusForbidden {
			body = "denied"
		}
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}}
	cfg := &engine.LLMConfig{DefaultProvider: "openai"}
	require.NoError(t, c.UpdateLLMConfig(t.Context(), cfg))
	require.Equal(t, cfg, got)
	require.Same(t, bootstrap, c.LLMConfig, "request metadata must retain its sealed bootstrap value")
	require.NoError(t, c.UpdateLLMConfig(t.Context(), nil))
	require.Nil(t, got)
	configured, err := c.LLMConfigured(t.Context())
	require.NoError(t, err)
	require.True(t, configured)
	models, err := c.LLMModelConfig(t.Context())
	require.NoError(t, err)
	require.Equal(t, "claude-sonnet-4-5", models.Providers["anthropic"].Model)
	status = http.StatusForbidden
	require.ErrorContains(t, c.UpdateLLMConfig(t.Context(), cfg), "denied")
	_, err = c.LLMConfigured(t.Context())
	require.ErrorContains(t, err, "denied")
}
