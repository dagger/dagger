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

func Do(ctx context.Context, payload string) (*Map, error) {
	client, ok := ctx.Value(clientKey{}).(*http.Client)
	if !ok {
		return nil, fmt.Errorf("no client in context")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "http://fake.invalid/graphql", bytes.NewBufferString(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/graphql")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("unexpected status code: %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	output := map[string]interface{}{}
	if err := json.Unmarshal(body, &output); err != nil {
		return nil, err
	}
	if output["errors"] != nil {
		return nil, fmt.Errorf("errors: %s", output["errors"])
	}

	// TODO: remove outer "data" field just for convenience until we have nicer helpers for reading these results
	return &Map{output["data"].(map[string]interface{})}, nil
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

type Map struct {
	Data map[string]interface{}
}

func (m *Map) Map(key string) *Map {
	return &Map{m.Data[key].(map[string]interface{})}
}

// TODO: all these methods are silly, do a full marshal/unmarshal cycle for convenience for now
func (m *Map) String(key string) api.DaggerString {
	raw := m.Data[key]
	bytes, err := json.Marshal(raw)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal %+v: %v", raw, err))
	}
	var s api.DaggerString
	if err := json.Unmarshal(bytes, &s); err != nil {
		panic(fmt.Errorf("failed to unmarshal dagger string during parse: %w", err))
	}
	return s
}

func (m *Map) FS(key string) api.FS {
	raw := m.Data[key]
	bytes, err := json.Marshal(raw)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal %+v: %v", raw, err))
	}
	var fs api.FS
	if err := json.Unmarshal(bytes, &fs); err != nil {
		panic(fmt.Errorf("failed to unmarshal fs during parse: %w", err))
	}
	return fs
}

func (m *Map) StringList(key string) []api.DaggerString {
	raw := m.Data[key]
	bytes, err := json.Marshal(raw)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal %+v: %v", raw, err))
	}
	var s []api.DaggerString
	if err := json.Unmarshal(bytes, &s); err != nil {
		panic(fmt.Errorf("failed to unmarshal dagger string list during parse: %w", err))
	}
	return s
}
