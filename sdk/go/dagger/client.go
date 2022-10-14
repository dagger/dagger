//go:generate go run ../../../cmd/dagger client-gen -o ./api/api.gen.go
package dagger

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/Khan/genqlient/graphql"
)

type SecretID string

type FSID string

type Filesystem struct {
	ID FSID `json:"id"`
}

type clientKey struct{}

func Client(ctx context.Context) (graphql.Client, error) {
	client, ok := ctx.Value(clientKey{}).(*http.Client)
	if !ok {
		return nil, errors.New("no dagger client in context")
	}
	return graphql.NewClient("http://fake.invalid/query", client), nil
}

func WithHTTPClient(ctx context.Context, c *http.Client) context.Context {
	return context.WithValue(ctx, clientKey{}, c)
}

func WithUnixSocketAPIClient(ctx context.Context, socketPath string) context.Context {
	return WithHTTPClient(ctx, &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	})
}
