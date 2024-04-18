package cloud

import (
	"context"
	"net/url"
	"os"

	"github.com/shurcooL/graphql"
	"golang.org/x/oauth2"

	"github.com/dagger/dagger/internal/cloud/auth"
)

type Client struct {
	u *url.URL
	c *graphql.Client
}

func NewClient(ctx context.Context) (*Client, error) {
	api := "https://api.dagger.cloud"
	if cloudURL := os.Getenv("DAGGER_CLOUD_URL"); cloudURL != "" {
		api = cloudURL
	}

	u, err := url.Parse(api)
	if err != nil {
		return nil, err
	}

	tokenSource, err := auth.TokenSource(ctx)
	if err != nil {
		return nil, err
	}

	httpClient := oauth2.NewClient(ctx, tokenSource)

	return &Client{
		u: u,
		c: graphql.NewClient(api+"/query", httpClient),
	}, nil
}

type OrgResponse struct {
	ID   string
	Name string
}

type UserResponse struct {
	ID   string
	Orgs []OrgResponse
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
