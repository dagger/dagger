package dagger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func Client(ctx context.Context) (graphql.Client, error) {
	client, ok := ctx.Value(clientKey{}).(*http.Client)
	if !ok {
		return nil, errors.New("no dagger client in context")
	}
	return graphql.NewClient("http://fake.invalid", client), nil
}

func Do(ctx context.Context, payload string) (*Map, error) {
	client, err := Client(ctx)
	if err != nil {
		return nil, err
	}
	var resp graphql.Response
	if err := client.MakeRequest(ctx, &graphql.Request{
		Query: payload,
	}, &resp); err != nil {
		return nil, err
	}
	if resp.Errors != nil {
		return nil, fmt.Errorf("graphql error: %w", resp.Errors)
	}
	return &Map{resp.Data.(map[string]interface{})}, nil
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
