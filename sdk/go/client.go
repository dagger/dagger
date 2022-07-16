package dagger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/dagger/cloak/api"
)

type clientKey struct{}

func Do(ctx context.Context, payload string) (string, error) {
	client, ok := ctx.Value(clientKey{}).(*http.Client)
	if !ok {
		return "", fmt.Errorf("no client in context")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "http://fake.invalid/graphql", bytes.NewBufferString(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/graphql")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("unexpected status code: %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	output := map[string]interface{}{}
	if err := json.Unmarshal(body, &output); err != nil {
		return "", err
	}
	if output["errors"] != nil {
		return "", fmt.Errorf("errors: %s", output["errors"])
	}

	// TODO: remove outer "data" field just for convenience until we have nicer helpers for reading these results
	output = output["data"].(map[string]interface{})
	outputBytes, err := json.Marshal(output)
	if err != nil {
		return "", err
	}
	return string(outputBytes), nil
}

func WithUnixSocketAPIClient(ctx context.Context, socketPath string) context.Context {
	return context.WithValue(ctx, clientKey{}, &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	})
}

func WithInMemoryAPIClient(ctx context.Context, server api.Server) context.Context {
	return context.WithValue(ctx, clientKey{}, &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				// TODO: not efficient, but whatever
				serverConn, clientConn := net.Pipe()
				go server.ServeConn(ctx, serverConn)
				return clientConn, nil
			},
		},
	})
}
