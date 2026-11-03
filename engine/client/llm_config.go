package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dagger/dagger/engine"
)

// UpdateLLMConfig replaces this client's live routing configuration. Nil clears
// it. Bootstrap metadata stays unchanged, so concurrent requests and nested
// clients can continue using their authenticated session without reconnecting.
// Credentials must be secret URIs served by this client's attachables.
func (c *Client) UpdateLLMConfig(ctx context.Context, cfg *engine.LLMConfig) error {
	body, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	resp, err := c.llmConfigRequest(ctx, http.MethodPut, bytes.NewReader(body))
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// LLMConfigured reports whether the effective configuration has a provider
// credential or a local endpoint, including configuration inherited from the
// session's main client. It does not retrieve or validate credential values.
func (c *Client) LLMConfigured(ctx context.Context) (bool, error) {
	resp, err := c.llmConfigRequest(ctx, http.MethodGet, nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	var configured bool
	err = json.NewDecoder(resp.Body).Decode(&configured)
	return configured, err
}

func (c *Client) llmConfigRequest(ctx context.Context, method string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://dagger"+engine.LLMConfigEndpoint, body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.engineToken(), "")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("LLM configuration: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("LLM configuration: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return resp, nil
}
