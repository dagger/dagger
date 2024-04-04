package cloud

import (
	"context"
	"net/url"

	"github.com/shurcooL/graphql"
	"golang.org/x/oauth2"

	"github.com/dagger/dagger/internal/cloud/auth"
)

type Client struct {
	u *url.URL
	c *graphql.Client
}

func NewClient(ctx context.Context, api string) (*Client, error) {
	if api == "" {
		api = "https://api.dagger.cloud"
	}

	u, err := url.Parse(api)
	if err != nil {
		return nil, err
	}

	httpClient := oauth2.NewClient(ctx, auth.TokenSource(ctx))

	return &Client{
		u: u,
		c: graphql.NewClient(api+"/query", httpClient),
	}, nil
}

type UserResponse struct {
	ID string
}

func (c *Client) User(ctx context.Context) (*UserResponse, error) {
	var q struct {
		User UserResponse `graphql:"user"`
	}
	err := c.c.Query(ctx, &q, nil)
	if err != nil {
		return nil, err
	}

	return &q.User, nil
}
