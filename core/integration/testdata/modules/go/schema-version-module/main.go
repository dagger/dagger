package main

import (
	"context"

	"github.com/Khan/genqlient/graphql"
)

type Foo struct{}

func (m *Foo) GetVersion(ctx context.Context) (string, error) {
	return schemaVersion(ctx)
}

func schemaVersion(ctx context.Context) (string, error) {
	resp := &graphql.Response{}
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{__schemaVersion}",
	}, resp)
	if err != nil {
		return "", err
	}
	return resp.Data.(map[string]any)["__schemaVersion"].(string), nil
}
