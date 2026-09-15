package main

import (
	"context"
	"fmt"
	"github.com/Khan/genqlient/graphql"
)

type Test struct{}

type SocketIDResponse struct {
	Host struct {
		UnixSocket struct {
			Id string
		}
	}
}

func (m *Test) Fn(ctx context.Context, sockPath string) (string, error) {
	sockResp := &SocketIDResponse{}
	resp := &graphql.Response{Data: sockResp}
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{host{unixSocket(path:\"" + sockPath + "\"){id}}}",
	}, resp)
	if err != nil {
		return "", fmt.Errorf("get socket id req: %w", err)
	}
	id := sockResp.Host.UnixSocket.Id
	if id == "" {
		return "", fmt.Errorf("unexpected response: %+v", resp)
	}
	return id, nil
}
