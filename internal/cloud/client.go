package cloud

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/shurcooL/graphql"
	"golang.org/x/oauth2"
)

type Client struct {
	Trace bool

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

type OrgResponse struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

func (c *Client) Org(ctx context.Context, name string) (*OrgResponse, error) {
	var q struct {
		Org OrgResponse `graphql:"org(name: $name)"`
	}
	err := c.c.Query(ctx, &q, map[string]any{
		"name": graphql.String(name),
	})
	if err != nil {
		return nil, err
	}

	return &q.Org, nil
}

type CreateOrgRequest struct {
	Name string
}

type CreateOrgResponse struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

func (c *Client) CreateOrg(ctx context.Context, req *CreateOrgRequest) (*CreateOrgResponse, error) {
	var m struct {
		CreateOrg CreateOrgResponse `graphql:"createOrg(name: $name)"`
	}
	err := c.c.Mutate(ctx, &m, map[string]any{
		"name": graphql.String(req.Name),
	})
	if err != nil {
		return nil, err
	}

	return &m.CreateOrg, nil
}

type Role string

func NewRole(str string) Role {
	return Role(strings.ToUpper(str))
}

func (role Role) String() string {
	return strings.ToLower(string(role))
}

type AddOrgUserRoleRequest struct {
	OrgID  string
	UserID string
	Role   Role
}

type AddOrgUserRoleResponse struct {
	OrgID     string
	UserID    string
	Role      Role
	CreatedAt time.Time
}

func (c *Client) AddOrgUserRole(ctx context.Context, req *AddOrgUserRoleRequest) (*AddOrgUserRoleResponse, error) {
	var m struct {
		AddOrgUserRole AddOrgUserRoleResponse `graphql:"addOrgUserRole(org: $org, user: $user, role: $role)"`
	}
	err := c.c.Mutate(ctx, &m, map[string]any{
		"org":  graphql.ID(req.OrgID),
		"user": graphql.ID(req.UserID),
		"role": req.Role,
	})
	if err != nil {
		return nil, err
	}

	return &m.AddOrgUserRole, nil
}

type RemoveOrgUserRoleRequest struct {
	OrgID  string
	UserID string
	Role   Role
}

type RemoveOrgUserRoleResponse struct {
	Existed bool
}

func (c *Client) RemoveOrgUserRole(ctx context.Context, req *RemoveOrgUserRoleRequest) (*RemoveOrgUserRoleResponse, error) {
	var m struct {
		RemoveOrgUserRole bool `graphql:"removeOrgUserRole(org: $org, user: $user, role: $role)"`
	}
	err := c.c.Mutate(ctx, &m, map[string]any{
		"org":  graphql.ID(req.OrgID),
		"user": graphql.ID(req.UserID),
		"role": req.Role,
	})
	if err != nil {
		return nil, err
	}

	return &RemoveOrgUserRoleResponse{
		Existed: m.RemoveOrgUserRole,
	}, nil
}

type CreateOrgEngineIngestionTokenRequest struct {
	OrgID string
	Name  string
}

type CreateOrgEngineIngestionTokenResponse struct {
	OrgID     string
	Token     string
	CreatedAt time.Time
}

func (c *Client) CreateOrgEngineIngestionToken(ctx context.Context, req *CreateOrgEngineIngestionTokenRequest) (*CreateOrgEngineIngestionTokenResponse, error) {
	var m struct {
		CreateOrgEngineIngestionToken CreateOrgEngineIngestionTokenResponse `graphql:"createOrgEngineIngestionToken(org: $org, name: $name)"`
	}
	err := c.c.Mutate(ctx, &m, map[string]any{
		"org":  graphql.ID(req.OrgID),
		"name": graphql.String(req.Name),
	})
	if err != nil {
		return nil, err
	}

	return &m.CreateOrgEngineIngestionToken, nil
}

type DeleteOrgEngineIngestionTokenRequest struct {
	OrgID string
	Token string
}

type DeleteOrgEngineIngestionTokenResponse struct {
	Existed bool
}

func (c *Client) DeleteOrgEngineIngestionToken(ctx context.Context, req *DeleteOrgEngineIngestionTokenRequest) (*DeleteOrgEngineIngestionTokenResponse, error) {
	var m struct {
		DeleteOrgEngineIngestionToken bool `graphql:"deleteOrgEngineIngestionToken(org: $org, token: $token)"`
	}
	err := c.c.Mutate(ctx, &m, map[string]any{
		"org":   graphql.ID(req.OrgID),
		"token": graphql.String(req.Token),
	})
	if err != nil {
		return nil, err
	}

	return &DeleteOrgEngineIngestionTokenResponse{
		Existed: m.DeleteOrgEngineIngestionToken,
	}, nil
}
