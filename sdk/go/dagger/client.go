package dagger

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/cloak/api"
)

type SecretID string

type FSID string

type Filesystem struct {
	ID          FSID        `json:"id"`
	Exec        *Exec       `json:"exec"`
	Dockerbuild *Filesystem `json:"dockerbuild"`
	File        *string     `json:"file"`
}

type Exec struct {
	Fs       *Filesystem `json:"fs"`
	Stdout   *string     `json:"stdout"`
	Stderr   *string     `json:"stderr"`
	ExitCode *int        `json:"exitCode"`
	Mount    *Filesystem `json:"mount"`
}

type clientKey struct{}

func Client(ctx context.Context) (graphql.Client, error) {
	client, ok := ctx.Value(clientKey{}).(*http.Client)
	if !ok {
		return nil, errors.New("no dagger client in context")
	}
	return graphql.NewClient("http://fake.invalid", client), nil
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
