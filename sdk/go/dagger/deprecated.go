package dagger

import (
	"context"
	"fmt"

	"github.com/Khan/genqlient/graphql"
)

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

func (m *Map) String(key string) string {
	raw, ok := m.Data[key].(string)
	if !ok {
		panic(fmt.Errorf("invalid type for string: %T", m.Data[key]))
	}
	return raw
}
