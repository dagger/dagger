package dagger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/cloak/api"
)

type FS string

func (fs FS) String() string {
	bytes, err := json.Marshal(fs)
	if err != nil {
		panic(err)
	}
	return string(bytes)
}

type DaggerString struct {
	data any
}

func (s DaggerString) String() string {
	bytes, err := json.Marshal(s.data)
	if err != nil {
		panic(err)
	}
	return string(bytes)
}

type clientKey struct{}

func Client(ctx context.Context) graphql.Client {
	client, ok := ctx.Value(clientKey{}).(*http.Client)
	if !ok {
		panic("no client in context")
	}
	return graphql.NewClient("http://fake.invalid", client)
}

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

func (m *Map) FS(key string) FS {
	raw, ok := m.Data[key].(string)
	if !ok {
		panic(fmt.Errorf("invalid type for fs: %T", m.Data[key]))
	}
	return FS(raw)
}

/* TODO: switch back to DaggerString once re-integrated with generated clients
func (m *Map) String(key string) DaggerString {
	raw, ok := m.Data[key]
	if !ok {
		panic(fmt.Errorf("invalid type for string: %T", m.Data[key]))
	}
	return DaggerString{raw}
}

func (m *Map) StringList(key string) []DaggerString {
	list, ok := m.Data[key].([]interface{})
	if !ok {
		panic(fmt.Errorf("invalid type for string list: %T", m.Data[key]))
	}
	result := make([]DaggerString, len(list))
	for i, item := range list {
		result[i] = DaggerString{item}
	}
	return result
}
*/

func (m *Map) String(key string) string {
	raw, ok := m.Data[key].(string)
	if !ok {
		panic(fmt.Errorf("invalid type for string: %T", m.Data[key]))
	}
	return raw
}

func (m *Map) StringList(key string) []string {
	list, ok := m.Data[key].([]interface{})
	if !ok {
		panic(fmt.Errorf("invalid type for string list: %T", m.Data[key]))
	}
	result := make([]string, len(list))
	for i, item := range list {
		result[i] = item.(string)
	}
	return result
}
