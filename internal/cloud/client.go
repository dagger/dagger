package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/dagger/dagger/internal/cloud/auth"
)

type Client struct {
	Trace bool

	u *url.URL
	c *http.Client

	l sync.Mutex

	retryLogin bool
}

func NewClient(api string) (*Client, error) {
	if api == "" {
		api = "https://api.dagger.cloud"
	}

	u, err := url.Parse(api)
	if err != nil {
		return nil, err
	}

	return &Client{
		u: u,
		c: http.DefaultClient,
	}, nil
}

type UserResponse struct {
	UserID string `json:"user_id"`
}

func (c *Client) User(ctx context.Context) (*UserResponse, error) {
	var res UserResponse
	if err := c.apiReq(ctx, "GET", "/user", nil, &res); err != nil {
		return nil, err
	}

	return &res, nil
}

type CreateOrgRequest struct {
	Name string `json:"name"`
}

type CreateOrgResponse struct {
	OrgID     string    `json:"org_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

func (c *Client) CreateOrg(ctx context.Context, req *CreateOrgRequest) (*CreateOrgResponse, error) {
	var res CreateOrgResponse
	if err := c.apiReq(ctx, "POST", "/orgs", req, &res); err != nil {
		return nil, err
	}

	return &res, nil
}

type AddOrgUserRoleRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

type AddOrgUserRoleResponse struct {
	OrgID     string    `json:"org_id"`
	UserID    string    `json:"user_id"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func (c *Client) AddOrgUserRole(ctx context.Context, orgName string, req *AddOrgUserRoleRequest) (*AddOrgUserRoleResponse, error) {
	var res AddOrgUserRoleResponse
	if err := c.apiReq(ctx, "POST", "/orgs/"+orgName+"/users", req, &res); err != nil {
		return nil, err
	}

	return &res, nil
}

type RemoveOrgUserRoleRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

type RemoveOrgUserRoleResponse struct {
	Existed bool `json:"existed"`
}

func (c *Client) RemoveOrgUserRole(ctx context.Context, orgName string, req *RemoveOrgUserRoleRequest) (*RemoveOrgUserRoleResponse, error) {
	var res RemoveOrgUserRoleResponse
	if err := c.apiReq(ctx, "DELETE", "/orgs/"+orgName+"/users", req, &res); err != nil {
		return nil, err
	}

	return &res, nil
}

func (c *Client) apiReq(ctx context.Context, method, path string, reqBody, resBody any) error {
	var body io.Reader
	if reqBody != nil {
		payload, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}

		body = bytes.NewBuffer(payload)
	}

	req, err := http.NewRequest(method, c.u.JoinPath(path).String(), body)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}

	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.doWithAuth(ctx, req)
	if err != nil {
		return fmt.Errorf("do: %w", err)
	}

	defer res.Body.Close()

	if res.StatusCode >= 400 {
		body, err := io.ReadAll(res.Body)
		if err == nil {
			return fmt.Errorf("bad response: %s\n\n%s", res.Status, string(body))
		}
		return fmt.Errorf("bad response: %s", res.Status)
	}

	if resBody != nil {
		if err := json.NewDecoder(res.Body).Decode(resBody); err != nil {
			return fmt.Errorf("unmarshal: %w", err)
		}
	}

	return nil
}

func (c *Client) doWithAuth(ctx context.Context, req *http.Request) (*http.Response, error) {
	req = req.WithContext(ctx)

	// OAuth2 authentication
	if err := auth.SetAuthHeader(ctx, req); err != nil {
		// if token is invalid or expired, we should handle re-auth in sync
		// fashion.
		c.l.Lock()
		defer c.l.Unlock()

		// only client trying to re-auth. other waiting clients shouldn't
		// re-trigger auth
		if c.retryLogin {
			// If we fail to refresh an access token, try to log in again.
			c.retryLogin = false
			err := auth.Login(ctx)
			if err != nil {
				return nil, err
			}
		}

		if err := auth.SetAuthHeader(ctx, req); err != nil {
			return nil, err
		}
	}

	// req.Header.Set("User-Agent", version.Long())

	if c.Trace {
		req.Header.Write(os.Stderr)
		fmt.Fprintln(os.Stderr)
	}

	resp, err := c.c.Do(req)
	if err != nil {
		return resp, err
	}

	if c.Trace {
		resp.Header.Write(os.Stderr)
		fmt.Fprintln(os.Stderr)
	}

	// If there's an auth problem, login and retry the request
	if resp.StatusCode == http.StatusUnauthorized && c.retryLogin {
		c.retryLogin = false
		err := auth.Login(ctx)
		if err != nil {
			return resp, err
		}
		return c.doWithAuth(ctx, req)
	}

	return resp, err
}
