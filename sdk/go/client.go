package dagger

import (
	"context"
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

	req, err := http.NewRequestWithContext(ctx, "GET", "http://fake.invalid/graphql", nil)
	if err != nil {
		return "", err
	}
	q := req.URL.Query()
	q.Set("payload", payload)
	req.URL.RawQuery = q.Encode()

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
	return string(body), nil
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
	serverConn, clientConn := net.Pipe()
	go server.ServeConn(ctx, serverConn)
	return context.WithValue(ctx, clientKey{}, &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return clientConn, nil
			},
		},
	})
}
